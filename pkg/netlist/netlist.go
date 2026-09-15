// Package netlist は LTspice のネットリスト(.net)を読み取る。
//
// 対応素子は R, C, L, 独立電圧源 V, 独立電流源 I, 結合 K のみ。
// それ以外の素子・ディレクティブに出会った場合は黙って無視せずエラーにする。
package netlist

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Kind は素子の種類。
type Kind int

const (
	KindR Kind = iota
	KindC
	KindL
	KindV
	KindI
)

func (k Kind) String() string {
	switch k {
	case KindR:
		return "R"
	case KindC:
		return "C"
	case KindL:
		return "L"
	case KindV:
		return "V"
	case KindI:
		return "I"
	}
	return "?"
}

// Element は2端子素子。Nodes[0] が第1ノード（+端子）。
type Element struct {
	Name    string
	Kind    Kind
	Nodes   [2]string
	Value   float64 // R[Ω], C[F], L[H]
	ACMag   float64 // 源の AC 振幅
	ACPhase float64 // 源の AC 位相[deg]
	Line    int
	Raw     string // ネットリスト上の元の行
}

// Coupling は K 行（結合係数）。
type Coupling struct {
	Name      string
	Inductors []string
	K         float64
	Line      int
	Raw       string // ネットリスト上の元の行
}

// Netlist は読み取り結果。
type Netlist struct {
	Title      string
	Path       string
	AscPath    string
	Elements   []Element
	Couplings  []Coupling
	Directives []string
	Nodes      []string // 出現順（正規化済み、"0" を含む）
	Warnings   []string
}

// GroundNode は接地ノードの正規化名。
const GroundNode = "0"

// IsGround は接地ノードかどうか。
func IsGround(n string) bool { return n == GroundNode }

func normNode(s string) string {
	l := strings.ToLower(s)
	if l == "0" || l == "gnd" || l == "gnd!" {
		return GroundNode
	}
	return l
}

// Load はパスを読み取る。.asc を渡された場合は対応する .net を探す。
func Load(path string) (*Netlist, error) {
	ext := strings.ToLower(filepath.Ext(path))
	netPath := path
	ascPath := ""
	if ext == ".asc" {
		ascPath = path
		base := strings.TrimSuffix(path, filepath.Ext(path))
		cands := []string{base + "-generated.net", base + ".net"}
		found := ""
		for _, c := range cands {
			if _, err := os.Stat(c); err == nil {
				found = c
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("%s に対応するネットリストが見つかりません。\n"+
				"LTspice で View > update and view spice netlist を実行し、\n"+
				"%s または %s として保存してください。",
				filepath.Base(path), filepath.Base(cands[0]), filepath.Base(cands[1]))
		}
		netPath = found
	} else {
		base := strings.TrimSuffix(path, filepath.Ext(path))
		for _, c := range []string{base + ".asc", strings.TrimSuffix(base, "-generated") + ".asc"} {
			if _, err := os.Stat(c); err == nil {
				ascPath = c
				break
			}
		}
	}

	nl, err := parseFile(netPath)
	if err != nil {
		return nil, err
	}
	nl.AscPath = ascPath
	if ascPath != "" {
		if w, err := crossCheckAsc(nl, ascPath); err == nil {
			nl.Warnings = append(nl.Warnings, w...)
		}
	}
	return nl, nil
}

func parseFile(path string) (*Netlist, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, path)
}

// Parse はネットリストの中身を読む。path は「どこから来たか」を Netlist.Path に
// 入れるためだけのもので、開き直したりはしない。
//
// 回路図から組み立てた文字列を渡せるようにしてある。素子行の解釈を2か所に
// 書くと、片方だけ直る事故が起きる。
func Parse(r io.Reader, path string) (*Netlist, error) {
	nl := &Netlist{Path: path}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	var lineNos []int
	no := 0
	for sc.Scan() {
		no++
		raw := strings.TrimRight(sc.Text(), " \t\r")
		raw = strings.TrimPrefix(raw, string(rune(0xFEFF)))
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(raw), "*") {
			if nl.Title == "" {
				nl.Title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "*"))
			}
			continue
		}
		// 継続行
		if strings.HasPrefix(strings.TrimSpace(raw), "+") && len(lines) > 0 {
			lines[len(lines)-1] += " " + strings.TrimSpace(raw)[1:]
			continue
		}
		lines = append(lines, raw)
		lineNos = append(lineNos, no)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	nodeSeen := map[string]bool{}
	addNode := func(n string) {
		if !nodeSeen[n] {
			nodeSeen[n] = true
			nl.Nodes = append(nl.Nodes, n)
		}
	}

	for i, line := range lines {
		ln := lineNos[i]
		// 行内コメント
		if k := strings.IndexAny(line, ";"); k >= 0 {
			line = line[:k]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		head := fields[0]
		if strings.HasPrefix(head, ".") {
			d := strings.ToLower(head)
			switch d {
			case ".param", ".func", ".include", ".inc", ".lib", ".subckt", ".ends", ".step", ".global":
				return nil, fmt.Errorf("%s:%d: 未対応のディレクティブです: %s（第1版は R/C/L/V/I のみ対応）", filepath.Base(path), ln, head)
			}
			nl.Directives = append(nl.Directives, line)
			continue
		}

		switch unicode.ToUpper(rune(head[0])) {
		case 'R', 'C', 'L':
			if len(fields) < 4 {
				return nil, fmt.Errorf("%s:%d: 引数が足りません: %s", filepath.Base(path), ln, line)
			}
			var kind Kind
			switch unicode.ToUpper(rune(head[0])) {
			case 'R':
				kind = KindR
			case 'C':
				kind = KindC
			case 'L':
				kind = KindL
			}
			v, err := ParseValue(fields[3])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %s の値 %q を解釈できません: %v", filepath.Base(path), ln, head, fields[3], err)
			}
			n1, n2 := normNode(fields[1]), normNode(fields[2])
			addNode(n1)
			addNode(n2)
			nl.Elements = append(nl.Elements, Element{
				Name: head, Kind: kind, Nodes: [2]string{n1, n2}, Value: v, Line: ln,
				Raw: strings.TrimSpace(line),
			})
		case 'V', 'I':
			if len(fields) < 3 {
				return nil, fmt.Errorf("%s:%d: 引数が足りません: %s", filepath.Base(path), ln, line)
			}
			kind := KindV
			if unicode.ToUpper(rune(head[0])) == 'I' {
				kind = KindI
			}
			n1, n2 := normNode(fields[1]), normNode(fields[2])
			addNode(n1)
			addNode(n2)
			mag, phase, err := parseACSpec(fields[3:])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %s: %v", filepath.Base(path), ln, head, err)
			}
			nl.Elements = append(nl.Elements, Element{
				Name: head, Kind: kind, Nodes: [2]string{n1, n2},
				ACMag: mag, ACPhase: phase, Line: ln,
				Raw: strings.TrimSpace(line),
			})
		case 'K':
			if len(fields) < 4 {
				return nil, fmt.Errorf("%s:%d: K 行の引数が足りません: %s", filepath.Base(path), ln, line)
			}
			kv, err := ParseValue(fields[len(fields)-1])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: 結合係数 %q を解釈できません: %v", filepath.Base(path), ln, fields[len(fields)-1], err)
			}
			inds := make([]string, 0, len(fields)-2)
			for _, f := range fields[1 : len(fields)-1] {
				inds = append(inds, f)
			}
			nl.Couplings = append(nl.Couplings, Coupling{
				Name: head, Inductors: inds, K: kv, Line: ln, Raw: strings.TrimSpace(line),
			})
		default:
			return nil, fmt.Errorf("%s:%d: 未対応の素子です: %s（第1版は R/C/L/V/I/K のみ対応）", filepath.Base(path), ln, head)
		}
	}

	if err := nl.validate(); err != nil {
		return nil, err
	}
	return nl, nil
}

func (n *Netlist) validate() error {
	byName := map[string]*Element{}
	for i := range n.Elements {
		key := strings.ToLower(n.Elements[i].Name)
		if _, dup := byName[key]; dup {
			return fmt.Errorf("素子名が重複しています: %s", n.Elements[i].Name)
		}
		byName[key] = &n.Elements[i]
	}
	for _, c := range n.Couplings {
		for _, ind := range c.Inductors {
			e, ok := byName[strings.ToLower(ind)]
			if !ok {
				return fmt.Errorf("%s 行: インダクタ %s が見つかりません", c.Name, ind)
			}
			if e.Kind != KindL {
				return fmt.Errorf("%s 行: %s はインダクタではありません", c.Name, ind)
			}
		}
		if len(c.Inductors) < 2 {
			return fmt.Errorf("%s 行: 結合には2個以上のインダクタが必要です", c.Name)
		}
	}
	return nil
}

// FindElement は名前（大小文字無視）で素子を探す。
func (n *Netlist) FindElement(name string) *Element {
	for i := range n.Elements {
		if strings.EqualFold(n.Elements[i].Name, name) {
			return &n.Elements[i]
		}
	}
	return nil
}

// parseACSpec は電源行のトークン列から AC 振幅・位相を取り出す。
func parseACSpec(toks []string) (mag, phase float64, err error) {
	for i := 0; i < len(toks); i++ {
		if strings.EqualFold(toks[i], "AC") {
			if i+1 < len(toks) {
				m, e := ParseValue(toks[i+1])
				if e != nil {
					return 0, 0, fmt.Errorf("AC 振幅 %q を解釈できません", toks[i+1])
				}
				mag = m
			} else {
				mag = 1
			}
			if i+2 < len(toks) {
				if p, e := ParseValue(toks[i+2]); e == nil {
					phase = p
				}
			}
			return mag, phase, nil
		}
	}
	// AC 指定なし → .ac 解析では 0（短絡/開放）
	return 0, 0, nil
}

var suffixes = []struct {
	s string
	m float64
}{
	{"meg", 1e6}, {"mil", 25.4e-6},
	{"t", 1e12}, {"g", 1e9}, {"k", 1e3}, {"m", 1e-3},
	{"u", 1e-6}, {"µ", 1e-6}, {"μ", 1e-6}, {"n", 1e-9}, {"p", 1e-12}, {"f", 1e-15},
}

// ParseValue は SPICE の数値表記（接尾辞つき）を解釈する。
func ParseValue(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("空の値")
	}
	if strings.HasPrefix(s, "{") {
		return 0, fmt.Errorf("式 %s は未対応です（.param 非対応）", s)
	}
	// 数値部分を切り出す
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	// 指数部 e/E は接尾辞 'f'(femto) 等と衝突しないよう、直後に符号か数字がある場合のみ
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	numPart := s[:i]
	rest := strings.ToLower(strings.TrimSpace(s[i:]))
	v, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("数値として読めません: %q", s)
	}
	if rest == "" {
		return v, nil
	}
	for _, sf := range suffixes {
		if strings.HasPrefix(rest, sf.s) {
			// 100µ → 9.999999999999999e-05 のような丸め誤差を避ける
			r, err := strconv.ParseFloat(strconv.FormatFloat(v*sf.m, 'g', 15, 64), 64)
			if err != nil {
				return v * sf.m, nil
			}
			return r, nil
		}
	}
	// 単位だけの余分な文字（"F", "Ohm" 等）は無視
	return v, nil
}

// crossCheckAsc は .asc の SYMATTR を読み、.net と食い違いがないか確認する。
func crossCheckAsc(nl *Netlist, ascPath string) ([]string, error) {
	f, err := os.Open(ascPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type sym struct{ name, value string }
	var syms []sym
	var cur sym
	var warns []string

	flush := func() {
		if cur.name != "" {
			syms = append(syms, cur)
		}
		cur = sym{}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "SYMBOL "):
			flush()
		case strings.HasPrefix(line, "SYMATTR InstName"):
			cur.name = strings.TrimSpace(strings.TrimPrefix(line, "SYMATTR InstName"))
		case strings.HasPrefix(line, "SYMATTR Value2"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "SYMATTR Value2"))
			if cur.value == "" || strings.Contains(strings.ToUpper(v), "AC") {
				cur.value = v
			}
		case strings.HasPrefix(line, "SYMATTR Value"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "SYMATTR Value"))
			v = strings.Trim(v, `"`)
			if v != "" && cur.value == "" {
				cur.value = v
			}
		}
	}
	flush()

	for _, s := range syms {
		e := nl.FindElement(s.name)
		if e == nil {
			warns = append(warns, fmt.Sprintf("回路図(.asc)にある %s がネットリストにありません（ネットリストが古い可能性）", s.name))
			continue
		}
		if s.value == "" {
			continue
		}
		switch e.Kind {
		case KindR, KindC, KindL:
			v, err := ParseValue(normalizeMicro(s.value))
			if err == nil && relDiff(v, e.Value) > 1e-9 {
				warns = append(warns, fmt.Sprintf("%s の値が .asc(%v) と .net(%v) で異なります", s.name, v, e.Value))
			}
		case KindV, KindI:
			m, _, err := parseACSpec(strings.Fields(s.value))
			if err == nil && m != 0 && relDiff(m, e.ACMag) > 1e-9 {
				warns = append(warns, fmt.Sprintf("%s の AC 振幅が .asc(%v) と .net(%v) で異なります", s.name, m, e.ACMag))
			}
		}
	}
	for _, e := range nl.Elements {
		found := false
		for _, s := range syms {
			if strings.EqualFold(s.name, e.Name) {
				found = true
				break
			}
		}
		if !found {
			warns = append(warns, fmt.Sprintf("ネットリストの %s が回路図(.asc)に見当たりません", e.Name))
		}
	}
	return warns, nil
}

// normalizeMicro は .asc が Windows-1252 で書く µ(0xB5) などを "u" に直す。
func normalizeMicro(s string) string {
	s = strings.ReplaceAll(s, "\xb5", "u")
	s = strings.ReplaceAll(s, "µ", "u")
	s = strings.ReplaceAll(s, "μ", "u")
	s = strings.ReplaceAll(s, "�", "u")
	return s
}

func relDiff(a, b float64) float64 {
	d := a - b
	if d < 0 {
		d = -d
	}
	m := a
	if m < 0 {
		m = -m
	}
	if bb := b; bb < 0 && -bb > m {
		m = -bb
	} else if bb > m {
		m = bb
	}
	if m == 0 {
		return d
	}
	return d / m
}

// ACRange は .ac 行に書かれていた周波数範囲 [Hz] を返す。
//
// LTspice の .ac 行は「.ac dec <点数> <開始> <終了>」の形なので、末尾2つを読む。
// 見つからなければ ok が false になる。
func ACRange(n *Netlist) (start, stop float64, ok bool) {
	for _, d := range n.Directives {
		f := strings.Fields(d)
		if len(f) < 5 || !strings.EqualFold(f[0], ".ac") {
			continue
		}
		a, err1 := ParseValue(f[len(f)-2])
		b, err2 := ParseValue(f[len(f)-1])
		if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
			continue
		}
		if a > b {
			a, b = b, a
		}
		return a, b, true
	}
	return 0, 0, false
}
