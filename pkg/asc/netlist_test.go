package asc

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
)

// TestNetlistMatchesLTspice は、回路図から組み立てたネットリストが、
// **LTspice が同じ回路図から書いたものと同じ回路になる**ことを確かめる。
//
// これがこの仕組みの要である。LTspice に .net を書かせる手順を無くすのだから、
// 無くしたものと同じ答えになることを見ないと意味がない。
//
// ノードの名前は一致しない（LTspice の番号の振り方は公開されていない）ので、
// **名前の付け替えを許して**比べる。素子・値・結合・ディレクティブは
// そのまま一致するはず。
func TestNetlistMatchesLTspice(t *testing.T) {
	for _, name := range []string{"20260906wptSSSP2", "20260913wpt1to3"} {
		t.Run(name, func(t *testing.T) {
			mine, err := LoadNetlist(filepath.Join("..", "..", "testdata", name+".asc"))
			if err != nil {
				t.Fatal(err)
			}
			theirs, err := netlist.Load(filepath.Join("..", "..", "testdata", name+".net"))
			if err != nil {
				t.Fatal(err)
			}

			// 素子の数と並び
			if len(mine.Elements) != len(theirs.Elements) {
				t.Fatalf("素子 %d 個、LTspice は %d 個", len(mine.Elements), len(theirs.Elements))
			}

			// ノード名の対応表を作りながら、素子を1つずつ見る。
			m2t := map[string]string{}
			t2m := map[string]string{}
			link := func(a, b string) error {
				if x, ok := m2t[a]; ok && x != b {
					return fmt.Errorf("ノード %s が %s と %s の両方に対応している", a, x, b)
				}
				if x, ok := t2m[b]; ok && x != a {
					return fmt.Errorf("LTspice のノード %s が %s と %s の両方に対応している", b, x, a)
				}
				m2t[a], t2m[b] = b, a
				return nil
			}

			for i := range mine.Elements {
				a, b := mine.Elements[i], theirs.Elements[i]
				if a.Name != b.Name {
					t.Fatalf("%d 番の素子が %s、LTspice は %s（並びが違う）", i, a.Name, b.Name)
				}
				if a.Kind != b.Kind {
					t.Errorf("%s の種類が %s、LTspice は %s", a.Name, a.Kind, b.Kind)
				}
				if a.Value != b.Value {
					t.Errorf("%s の値が %g、LTspice は %g", a.Name, a.Value, b.Value)
				}
				if a.ACMag != b.ACMag || a.ACPhase != b.ACPhase {
					t.Errorf("%s の AC が %g∠%g、LTspice は %g∠%g",
						a.Name, a.ACMag, a.ACPhase, b.ACMag, b.ACPhase)
				}
				for k := 0; k < 2; k++ {
					// 接地はどちらも "0"。名前を付け替えてはいけない。
					if netlist.IsGround(a.Nodes[k]) != netlist.IsGround(b.Nodes[k]) {
						t.Fatalf("%s の %d 番ノード: %s と %s（接地の有無が違う）",
							a.Name, k+1, a.Nodes[k], b.Nodes[k])
					}
					if err := link(a.Nodes[k], b.Nodes[k]); err != nil {
						t.Fatalf("%s の %d 番ノード: %v", a.Name, k+1, err)
					}
				}
			}

			// 結合
			if len(mine.Couplings) != len(theirs.Couplings) {
				t.Fatalf("結合 %d 個、LTspice は %d 個", len(mine.Couplings), len(theirs.Couplings))
			}
			for i := range mine.Couplings {
				a, b := mine.Couplings[i], theirs.Couplings[i]
				if !strings.EqualFold(a.Name, b.Name) || math.Abs(a.K-b.K) > 0 {
					t.Errorf("結合 %d: %s k=%g、LTspice は %s k=%g", i, a.Name, a.K, b.Name, b.K)
				}
				if strings.Join(a.Inductors, ",") != strings.Join(b.Inductors, ",") {
					t.Errorf("結合 %s のコイル: %v、LTspice は %v", a.Name, a.Inductors, b.Inductors)
				}
			}

			// 掃引範囲（.ac 行が拾えているか）
			s1, e1, ok1 := netlist.ACRange(mine)
			s2, e2, ok2 := netlist.ACRange(theirs)
			if ok1 != ok2 || s1 != s2 || e1 != e2 {
				t.Errorf(".ac の範囲が %g〜%g (%v)、LTspice は %g〜%g (%v)", s1, e1, ok1, s2, e2, ok2)
			}

			// ノードの数
			if len(mine.Nodes) != len(theirs.Nodes) {
				t.Errorf("ノード %d 個、LTspice は %d 個", len(mine.Nodes), len(theirs.Nodes))
			}
			t.Logf("素子 %d・結合 %d・ノード %d が一致（名前の対応 %d 組）",
				len(mine.Elements), len(mine.Couplings), len(mine.Nodes), len(m2t))
		})
	}
}

// TestNetlistNamedNodesKept は、FLAG で付けた名前がそのまま残ることを見る。
// y1 などは論文の図と突き合わせるときに使うので、勝手に振り直されては困る。
func TestNetlistNamedNodesKept(t *testing.T) {
	nl, err := LoadNetlist(filepath.Join("..", "..", "testdata", "20260913wpt1to3.asc"))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, n := range nl.Nodes {
		have[n] = true
	}
	for _, want := range []string{"0", "y1", "y2", "y3", "y4"} {
		if !have[want] {
			t.Errorf("ノード %s が無い（%v）", want, nl.Nodes)
		}
	}
}

// TestNetlistIsDeterministic は、同じ回路図から何度組み立てても同じものが
// 出ることを見る。map を回る順に引きずられていると、ノード名が回ごとに
// 変わってしまう。
func TestNetlistIsDeterministic(t *testing.T) {
	p := filepath.Join("..", "..", "testdata", "20260906wptSSSP2.asc")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.NetlistText()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := s.NetlistText()
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("%d 回目で中身が変わった\n--- 1回目 ---\n%s\n--- %d回目 ---\n%s",
				i+1, first, i+1, got)
		}
	}
}

// TestMicroIsNormalised は、組み立てたネットリストに µ が残らないことを見る。
//
// µ を UTF-8 で書くと 2 バイトになり、LTspice が接尾辞として読まない。
// 100µ が 100 H と解釈されてコイルがほぼ開放になる、という黙った壊れ方を
// するので、組み立てる時点で u にしておく。
func TestMicroIsNormalised(t *testing.T) {
	s, err := Load(filepath.Join("..", "..", "testdata", "20260906wptSSSP2.asc"))
	if err != nil {
		t.Fatal(err)
	}
	// 元の回路図には µ が入っている（＝この確認に意味がある）。
	found := false
	for _, sym := range s.Symbols {
		if strings.ContainsAny(sym.Value(), "µμ") {
			found = true
		}
	}
	if !found {
		t.Skip("この回路図には µ が入っていないので確かめられない")
	}
	text, err := s.NetlistText()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(text, "µμ") {
		t.Errorf("組み立てたネットリストに µ が残っている:\n%s", text)
	}
	// それでも値は 100µ = 1e-4 のままであること。
	nl, err := s.Netlist("t.asc")
	if err != nil {
		t.Fatal(err)
	}
	e := nl.FindElement("L1")
	if e == nil || math.Abs(e.Value-1e-4) > 1e-18 {
		t.Errorf("L1 の値が %v（1e-4 のはず）", e)
	}
}

// TestUnconnectedPinIsCaught は、どこにもつながっていないピンを黙って
// 通さないことを見る。つながっていない素子があると、解は出るのに
// 回路が違う、という一番まずい壊れ方をする。
func TestUnconnectedPinIsCaught(t *testing.T) {
	src := "Version 4\nSHEET 1 880 680\n" +
		"WIRE 0 0 100 0\n" +
		"SYMBOL res 500 500 R0\nSYMATTR InstName R9\nSYMATTR Value 1k\n"
	s, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.NetlistText(); err == nil {
		t.Fatal("つながっていないピンがあるのに通った")
	} else {
		t.Logf("狙いどおり止まった: %v", err)
	}
}

// TestUnsupportedSymbolIsCaught は、扱えない部品を黙って落とさないことを見る。
func TestUnsupportedSymbolIsCaught(t *testing.T) {
	src := "Version 4\nSHEET 1 880 680\n" +
		"WIRE 0 0 100 0\n" +
		"SYMBOL diode 0 0 R0\nSYMATTR InstName D1\n"
	s, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.NetlistText()
	if err == nil {
		t.Fatal("未対応の部品があるのに通った")
	}
	if !strings.Contains(err.Error(), "diode") {
		t.Errorf("何が未対応かを言っていない: %v", err)
	}
	t.Logf("狙いどおり止まった: %v", err)
}

// TestOnSegment は「線の途中に載っている」の判定を見る。
func TestOnSegment(t *testing.T) {
	a, b := Point{0, 0}, Point{100, 0}
	for _, c := range []struct {
		p    Point
		want bool
		why  string
	}{
		{Point{50, 0}, true, "途中"},
		{Point{0, 0}, true, "端も含む（呼ぶ側が両端を除いている）"},
		{Point{100, 0}, true, "端"},
		{Point{150, 0}, false, "線の外"},
		{Point{-1, 0}, false, "線の外（手前）"},
		{Point{50, 1}, false, "少しずれている"},
	} {
		if got := onSegment(c.p, a, b); got != c.want {
			t.Errorf("onSegment(%v) = %v, 期待 %v（%s）", c.p, got, c.want, c.why)
		}
	}
	// 斜めの線でも通ること。
	if !onSegment(Point{5, 5}, Point{0, 0}, Point{10, 10}) {
		t.Error("斜めの線の途中を拾えていない")
	}
	if onSegment(Point{5, 6}, Point{0, 0}, Point{10, 10}) {
		t.Error("斜めの線から外れた点を拾ってしまった")
	}
}

// TestCrossingWiresDoNotConnect は、交差しているだけの結線をつながないことを
// 見る。LTspice も画面上そうなっている。ここを間違えると、回路が勝手に
// 短絡する。
func TestCrossingWiresDoNotConnect(t *testing.T) {
	// 横線と縦線が (50,50) で十字に交わる。どちらの端も交点には無い。
	// それぞれの線に抵抗を2本ずつ載せて、宙に浮いたピンを作らないようにする。
	//
	//   横の輪: R1 —— 横線 —— R2      （ノード a, b）
	//   縦の輪: R3 —— 縦線 —— R4      （ノード c, d）
	//
	// 交点でつながってしまうと、この2つの輪が1つになる。
	src := "Version 4\nSHEET 1 880 680\n" +
		// 横の輪
		"WIRE -16 50 116 50\n" + // 横線（交点 (50,50) を通る）
		"WIRE -16 130 116 130\n" +
		"SYMBOL res -32 34 R0\nSYMATTR InstName R1\nSYMATTR Value 1\n" + // ピン (-16,50)(-16,130)
		"SYMBOL res 100 34 R0\nSYMATTR InstName R2\nSYMATTR Value 2\n" + // ピン (116,50)(116,130)
		// 縦の輪
		"WIRE 50 -16 50 116\n" + // 縦線（交点 (50,50) を通る）
		"WIRE 210 -16 210 116\n" +
		"WIRE 50 -16 210 -16\n" +
		"WIRE 50 116 210 116\n" +
		"SYMBOL res 194 -16 R0\nSYMATTR InstName R3\nSYMATTR Value 3\n" // ピン (210,0)(210,80)
	s, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	names, err := s.nodeNames()
	if err != nil {
		t.Fatalf("この作りで止まった（テストの組み方が悪い）: %v", err)
	}

	h := names[Point{-16, 50}] // 横線の上
	v := names[Point{50, -16}] // 縦線の上
	if h == "" || v == "" {
		t.Fatalf("節点が拾えていない: 横=%q 縦=%q", h, v)
	}
	if h == v {
		t.Errorf("交差しているだけの2本がつながってしまった（どちらも %s）", h)
	}

	var ks []string
	for p, n := range names {
		ks = append(ks, fmt.Sprintf("%v=%s", p, n))
	}
	sort.Strings(ks)
	t.Logf("横線 %s / 縦線 %s（別の節点）", h, v)
	t.Logf("節点: %v", ks)
}
