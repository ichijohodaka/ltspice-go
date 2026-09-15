package asc

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func load(t *testing.T, name string) *Schematic {
	t.Helper()
	s, err := Load(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParse(t *testing.T) {
	s := load(t, "20260906wptSSSP2.asc")
	if len(s.Wires) == 0 {
		t.Fatal("結線が読めていない")
	}
	if len(s.Symbols) == 0 {
		t.Fatal("部品が読めていない")
	}
	if len(s.Unknown) != 0 {
		t.Errorf("絵を持っていない部品がある: %v", s.Unknown)
	}

	// 名前と値が拾えていること。
	got := map[string]string{}
	for _, sym := range s.Symbols {
		got[sym.Name()] = sym.Value()
	}
	for _, c := range []struct{ name, val string }{
		{"VS", "AC 5"}, // Value が "" なので Value2 を使う
		{"RS", "2.8"},
		{"L1", "100µ"},
		{"C1", "3.9n"},
		{"RL1", "50"},
	} {
		if got[c.name] != c.val {
			t.Errorf("%s の値 = %q, 期待 %q", c.name, got[c.name], c.val)
		}
	}

	// 接地が拾えていること。
	ng := 0
	for _, f := range s.Flags {
		if f.IsGround() {
			ng++
		}
	}
	if ng == 0 {
		t.Error("接地が1つも無い")
	}
}

// TestWiresMatchSVG は、書き出した SVG の直線が .asc の WIRE と
// **座標まで完全に一致する**ことを確かめる。
//
// 配置を決める仕事は要らない（LTspice が済ませてある）というのが
// このパッケージの前提なので、ここがずれたら前提が壊れている。
func TestWiresMatchSVG(t *testing.T) {
	for _, name := range []string{"20260906wptSSSP2.asc", "20260913wpt1to3.asc"} {
		t.Run(name, func(t *testing.T) {
			s := load(t, name)
			svg := s.SVG(SVGOptions{})

			want := map[string]bool{}
			for _, w := range s.Wires {
				want[pathOf(w.A, w.B)] = true
			}
			got := map[string]bool{}
			for _, m := range reLine.FindAllStringSubmatch(svg, -1) {
				got[m[0]] = true
			}
			for d := range want {
				if !got[d] {
					t.Errorf("WIRE が SVG に出ていない: %s", d)
				}
			}
			t.Logf("結線 %d 本（重複を除くと %d）、部品 %d 個", len(s.Wires), len(want), len(s.Symbols))
		})
	}
}

var reLine = regexp.MustCompile(`M-?\d+ -?\d+L-?\d+ -?\d+`)

func pathOf(a, b Point) string {
	return "M" + itoa(a.X) + " " + itoa(a.Y) + "L" + itoa(b.X) + " " + itoa(b.Y)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// TestJunctions は合流点の丸が、3本以上集まるところにだけ出ることを見る。
func TestJunctions(t *testing.T) {
	ws := []Wire{
		{Point{0, 0}, Point{10, 0}},
		{Point{10, 0}, Point{20, 0}}, // ここは2本なので丸は要らない
		{Point{20, 0}, Point{30, 0}},
		{Point{20, 0}, Point{20, 10}},
		{Point{20, 0}, Point{20, -10}}, // (20,0) は4本 → 丸
	}
	got := junctions(ws)
	if len(got) != 1 || got[0] != (Point{20, 0}) {
		t.Errorf("合流点 = %v, 期待 [{20 0}]", got)
	}
}

// TestRotate は向きの変換が筋の通ったものかを、参照無しで確かめる。
//
// 手元の回路図はすべて R0 なので、回転した図と突き合わせる材料がない。
// そこで「4回まわすと元に戻る」「鏡像を2回かけると元に戻る」「回転で
// 長さが変わらない」という性質のほうを見る。
func TestRotate(t *testing.T) {
	p := Point{16, 96}

	// R90 を 4 回で元に戻る。
	q := p
	for i := 0; i < 4; i++ {
		q = rotate(q, "R90")
	}
	if q != p {
		t.Errorf("R90 を4回まわすと %v、期待 %v", q, p)
	}

	// R180 は R90 を 2 回と同じ。
	if a, b := rotate(p, "R180"), rotate(rotate(p, "R90"), "R90"); a != b {
		t.Errorf("R180 = %v だが R90 二度は %v", a, b)
	}

	// R270 は R90 を 3 回と同じ。
	if a, b := rotate(p, "R270"), rotate(rotate(rotate(p, "R90"), "R90"), "R90"); a != b {
		t.Errorf("R270 = %v だが R90 三度は %v", a, b)
	}

	// 鏡像を2回かけると戻る。
	if q := rotate(rotate(p, "M0"), "M0"); q != p {
		t.Errorf("M0 を2回で %v、期待 %v", q, p)
	}

	// 原点からの距離は変わらない。
	d2 := func(q Point) int { return q.X*q.X + q.Y*q.Y }
	for _, r := range []string{"R0", "R90", "R180", "R270", "M0", "M90", "M180", "M270"} {
		if got := d2(rotate(p, r)); got != d2(p) {
			t.Errorf("%s で長さが変わった: %d → %d", r, d2(p), got)
		}
	}

	// R0 は何もしない。
	if q := rotate(p, "R0"); q != p {
		t.Errorf("R0 で %v になった", q)
	}
}

// TestUnknownSymbolIsNotDropped は、絵を持っていない部品を黙って
// 省かないことを見る。図から消えると、回路を読み違える。
func TestUnknownSymbolIsNotDropped(t *testing.T) {
	src := "Version 4\nSHEET 1 880 680\n" +
		"WIRE 0 0 100 0\n" +
		"SYMBOL diode 40 40 R0\nSYMATTR InstName D1\n"
	s, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Unknown) != 1 || s.Unknown[0] != "diode" {
		t.Errorf("Unknown = %v, 期待 [diode]", s.Unknown)
	}
	svg := s.SVG(SVGOptions{})
	if !strings.Contains(svg, "<rect") || !strings.Contains(svg, "diode") {
		t.Errorf("知らない部品が図から消えている:\n%s", svg)
	}
	if !strings.Contains(svg, "D1") {
		t.Error("名前が出ていない")
	}
}

// TestSVGWellFormed は書き出した SVG が形として通っていることを見る。
func TestSVGWellFormed(t *testing.T) {
	s := load(t, "20260913wpt1to3.asc")
	svg := s.SVG(SVGOptions{})
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatal("<svg> で囲まれていない")
	}
	if strings.Count(svg, "<svg") != 1 {
		t.Error("<svg> が2つ以上ある")
	}
	// 開いたタグはすべて自己終端か、text だけが閉じタグを持つ。
	open := strings.Count(svg, "<path") + strings.Count(svg, "<circle") + strings.Count(svg, "<rect")
	selfClose := strings.Count(svg, "/>")
	if selfClose < open {
		t.Errorf("自己終端していないタグがある（%d 個開いて %d 個閉じ）", open, selfClose)
	}
	if a, b := strings.Count(svg, "<text"), strings.Count(svg, "</text>"); a != b {
		t.Errorf("<text> が %d 個、</text> が %d 個", a, b)
	}
	// viewBox が中身を囲んでいること（負の座標があるので、0 始まりではない）。
	if !strings.Contains(svg, `viewBox="-`) {
		t.Errorf("viewBox が負から始まっていない: %.120s", svg)
	}
}

// TestEscaping は図面の文字に < & が混ざっても壊れないことを見る。
func TestEscaping(t *testing.T) {
	src := "Version 4\nSHEET 1 880 680\nWIRE 0 0 100 0\n" +
		"TEXT 0 40 Left 2 ;a<b & c>d\n"
	s, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	svg := s.SVG(SVGOptions{})
	if strings.Contains(svg, "a<b") {
		t.Error("< がそのまま出ている")
	}
	if !strings.Contains(svg, "&lt;") || !strings.Contains(svg, "&amp;") {
		t.Errorf("逃がせていない:\n%s", svg)
	}
}

// symbolTypes は手元の回路図に出てくる部品の種類を数える。
// 絵を足すべきものが増えていないかの見張り。
func TestSymbolTypes(t *testing.T) {
	seen := map[string]int{}
	for _, name := range []string{"20260906wptSSSP2.asc", "20260913wpt1to3.asc"} {
		for _, sym := range load(t, name).Symbols {
			seen[sym.Type]++
		}
	}
	var types []string
	for k := range seen {
		types = append(types, k)
	}
	sort.Strings(types)
	t.Logf("出てくる部品: %v", types)
	for _, k := range types {
		if _, ok := glyphs[k]; !ok {
			t.Errorf("%s の絵を持っていない（%d 個出てくる）", k, seen[k])
		}
	}
}
