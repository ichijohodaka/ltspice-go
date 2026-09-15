package asc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
)

// pins は部品のピンの位置（部品の原点からの相対）。並びがそのまま
// ネットリストの第1ノード・第2ノードになる。
//
// LTspice が書いた .net と突き合わせて確かめてある。
var pins = map[string][]Point{
	"res":     {{16, 16}, {16, 96}},
	"cap":     {{16, 0}, {16, 64}},
	"ind":     {{16, 16}, {16, 96}},
	"ind2":    {{16, 16}, {16, 96}},
	"voltage": {{0, 16}, {0, 96}},
	"current": {{0, 16}, {0, 96}},
}

// kindOf は部品の種類をネットリストの頭文字にする。
var kindOf = map[string]string{
	"res": "R", "cap": "C", "ind": "L", "ind2": "L",
	"voltage": "V", "current": "I",
}

// LoadNetlist は回路図を読んで、そのままネットリストにする。
//
// **LTspice に .net を書き出させる必要がない。** LTspice は .asc を閉じるときに
// 自分が書いた .net を消すので、「回路図を開くたびにネットリストが消える」
// という往復が起きていた。回路図には結線の座標が入っているのだから、
// こちらでたどればよい。
func LoadNetlist(path string) (*netlist.Netlist, error) {
	s, err := Load(path)
	if err != nil {
		return nil, err
	}
	nl, err := s.Netlist(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return nl, nil
}

// Netlist は回路図からネットリストを組み立てる。
//
// 組み立てるのは .net の**文字列**で、それを pkg/netlist に読ませる。
// 素子行の解釈（値の接尾辞、AC の振幅と位相、K 行）を2か所に書くと、
// 片方だけ直る事故が起きるため。
func (s *Schematic) Netlist(ascPath string) (*netlist.Netlist, error) {
	text, err := s.NetlistText()
	if err != nil {
		return nil, err
	}
	nl, err := netlist.Parse(strings.NewReader(text), ascPath)
	if err != nil {
		return nil, err
	}
	nl.AscPath = ascPath
	return nl, nil
}

// NetlistText は .net の形の文字列を組み立てる。
func (s *Schematic) NetlistText() (string, error) {
	names, err := s.nodeNames()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "* %s\n", s.title())
	fmt.Fprintf(&b, "* ltspice-go が回路図から組み立てたもの（LTspice には通していない）\n")

	for _, sym := range s.Symbols {
		k, ok := kindOf[sym.Type]
		if !ok {
			return "", fmt.Errorf("部品 %s（%s）は未対応です。扱えるのは %s",
				sym.Name(), sym.Type, strings.Join(knownTypes(), ", "))
		}
		name := sym.Name()
		if name == "" {
			return "", fmt.Errorf("%s の部品に InstName がありません（%v）", sym.Type, sym.At)
		}
		if !strings.EqualFold(name[:1], k) {
			// LTspice は頭文字で種類を決める。食い違っていたら黙って通さない。
			return "", fmt.Errorf("素子 %s は %s なので名前が %s で始まる必要があります",
				name, sym.Type, k)
		}
		ps := pins[sym.Type]
		var ns []string
		for _, p := range ps {
			q := rotate(p, sym.Rot)
			ns = append(ns, names[Point{sym.At.X + q.X, sym.At.Y + q.Y}])
		}
		fmt.Fprintf(&b, "%s %s %s\n", name, strings.Join(ns, " "), safeValue(sym.Value()))
	}

	// 図面に書いたディレクティブ（! で始まる TEXT）。K 行もここに来る。
	for _, t := range s.Texts {
		if !t.Cmd {
			continue
		}
		for _, line := range strings.Split(t.Body, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				fmt.Fprintf(&b, "%s\n", line)
			}
		}
	}
	b.WriteString(".end\n")
	return b.String(), nil
}

func (s *Schematic) title() string {
	return "ltspice-go: 回路図から組み立てたネットリスト"
}

func knownTypes() []string {
	var out []string
	for k := range kindOf {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- 結線をたどる -------------------------------------------------------

// nodeNames は図の上の点を節点にまとめ、名前を付ける。
//
// # つながるとみなす条件
//
//   - 結線の両端どうし
//   - ある点（ピン・ラベル・別の結線の端）が、結線の**途中に載っている**とき
//
// 結線が交差しているだけ（どちらの端でもない）はつながらない。LTspice の
// 画面上の約束と同じ。
//
// # 名前
//
// FLAG が付いていればその名前（"0" は接地）。付いていない節点は N001, N002,
// … と振る。**LTspice が振る番号とは一致しない**（向こうの付け方は公開されて
// いない）。つながり方は同じなので、解析の結果は変わらない。
func (s *Schematic) nodeNames() (map[Point]string, error) {
	uf := newUnionFind()

	// 結線の両端をつなぐ。
	for _, w := range s.Wires {
		uf.union(w.A, w.B)
	}

	// 気にすべき点をすべて集める。
	pts := map[Point]bool{}
	for _, w := range s.Wires {
		pts[w.A] = true
		pts[w.B] = true
	}
	for _, f := range s.Flags {
		pts[f.At] = true
		uf.find(f.At)
	}
	var pinPts []Point
	for _, sym := range s.Symbols {
		for _, p := range pins[sym.Type] {
			q := rotate(p, sym.Rot)
			a := Point{sym.At.X + q.X, sym.At.Y + q.Y}
			pts[a] = true
			pinPts = append(pinPts, a)
			uf.find(a)
		}
	}

	// 結線の途中に載っている点をつなぐ。
	for _, w := range s.Wires {
		for p := range pts {
			if p != w.A && p != w.B && onSegment(p, w.A, w.B) {
				uf.union(p, w.A)
			}
		}
	}

	// 名前を決める。
	name := map[int]string{}
	for _, f := range s.Flags {
		r := uf.find(f.At)
		if old, ok := name[r]; ok && old != f.Name {
			// 同じ節点に別の名前が2つ。どちらが正しいか決められない。
			return nil, fmt.Errorf("同じ節点に %s と %s の両方のラベルが付いています", old, f.Name)
		}
		name[r] = f.Name
	}

	// 名前の無い節点に N001, N002, … を振る。素子の並び順に見ていくので、
	// 同じ回路図なら毎回同じ名前になる。
	n := 0
	for _, a := range pinPts {
		r := uf.find(a)
		if _, ok := name[r]; ok {
			continue
		}
		n++
		name[r] = fmt.Sprintf("N%03d", n)
	}

	out := map[Point]string{}
	for p := range pts {
		r := uf.find(p)
		if nm, ok := name[r]; ok {
			out[p] = nm
		}
	}

	// **ピンが1本しか来ていない節点は、その素子が宙に浮いている。**
	//
	// 黙って通すと、その素子だけ別の節点にぶら下がったネットリストができる。
	// 解は出るのに回路が違う、という一番まずい壊れ方をするので、ここで止める。
	nPins := map[int]int{}
	for _, sym := range s.Symbols {
		for _, p := range pins[sym.Type] {
			q := rotate(p, sym.Rot)
			nPins[uf.find(Point{sym.At.X + q.X, sym.At.Y + q.Y})]++
		}
	}
	for _, sym := range s.Symbols {
		for i, p := range pins[sym.Type] {
			q := rotate(p, sym.Rot)
			a := Point{sym.At.X + q.X, sym.At.Y + q.Y}
			if nPins[uf.find(a)] < 2 {
				return nil, fmt.Errorf(
					"素子 %s の %d 番のピン (%d,%d) が、ほかのどの素子ともつながっていません。"+
						"結線が届いていないか、位置がずれています",
					sym.Name(), i+1, a.X, a.Y)
			}
		}
	}
	return out, nil
}

// onSegment は p が線分 ab の上に載っているか。端も含む。
//
// 端を含めても困らないのは、呼ぶ側が「線の両端そのものは別に union して
// いるので除く」と明示しているため。
func onSegment(p, a, b Point) bool {
	// 外積が 0 なら一直線上。
	if (b.X-a.X)*(p.Y-a.Y) != (b.Y-a.Y)*(p.X-a.X) {
		return false
	}
	// 内積で「間」にあることを見る。
	if (p.X-a.X)*(p.X-b.X) > 0 || (p.Y-a.Y)*(p.Y-b.Y) > 0 {
		return false
	}
	return true
}

type unionFind struct {
	id     map[Point]int
	parent []int
}

func newUnionFind() *unionFind { return &unionFind{id: map[Point]int{}} }

func (u *unionFind) find(p Point) int {
	i, ok := u.id[p]
	if !ok {
		i = len(u.parent)
		u.id[p] = i
		u.parent = append(u.parent, i)
	}
	for u.parent[i] != i {
		u.parent[i] = u.parent[u.parent[i]]
		i = u.parent[i]
	}
	return i
}

func (u *unionFind) union(a, b Point) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}

// safeValue は素子値を、LTspice に読ませても壊れない形にする。
//
// µ（U+00B5）を UTF-8 で書くと 2 バイトになり、単バイトの文字集合を前提に
// する LTspice では接尾辞として読まれない。100µ が 100、すなわち 100 H と
// 解釈されてコイルがほぼ開放になる。**黙って間違った回路になる**ので、
// 組み立てる時点で u に直しておく。
//
// pkg/netlist は µ も u も受けるので、こちらの読み取りには影響しない。
func safeValue(v string) string {
	return strings.NewReplacer("µ", "u", "μ", "u").Replace(v)
}
