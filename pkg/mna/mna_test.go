package mna

import (
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
	"github.com/ichijohodaka/ltspice-go/pkg/raw"
)

func load(t *testing.T, name string) *netlist.Netlist {
	t.Helper()
	nl, err := netlist.Load(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return nl
}

// parse はその場で書いたネットリストを読む。
func parse(t *testing.T, src string) *netlist.Netlist {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "t.net")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	nl, err := netlist.Load(p)
	if err != nil {
		t.Fatalf("%v\n--- ネットリスト ---\n%s", err, src)
	}
	return nl
}

func close2(t *testing.T, what string, got, want complex128, tol float64) {
	t.Helper()
	if d := cmplx.Abs(got - want); d > tol*(1+cmplx.Abs(want)) {
		t.Errorf("%s = %v, 期待 %v（差 %g）", what, got, want, d)
	}
}

// TestVsLTspice は LTspice が出した .raw と突き合わせる。
//
// 外の道具と突き合わせるので、こちらの実装に入り込んだ思い違いを
// そのまま見つけられる。testdata/20260913wpt1to3.raw は送電1・受電3 の
// WPT 回路を 100k〜500k で 700 点走らせたもの。
func TestVsLTspice(t *testing.T) {
	nl := load(t, "20260913wpt1to3.net")
	rd, err := raw.Read(filepath.Join("..", "..", "testdata", "20260913wpt1to3.raw"))
	if err != nil {
		t.Fatal(err)
	}
	vals := DefaultValues(nl)

	// 700 点のうち、端と真ん中あたりを見る。
	for _, pt := range []int{0, 1, 175, 350, 525, 698, 699} {
		f := rd.Freqs[pt]
		res, err := Solve(nl, vals, 2*math.Pi*f)
		if err != nil {
			t.Fatalf("%g Hz: %v", f, err)
		}
		// ノード電圧
		for _, node := range nl.Nodes {
			if netlist.IsGround(node) {
				continue
			}
			want, ok := rd.NodeVoltage(pt, node)
			if !ok {
				t.Fatalf("%g Hz: .raw に V(%s) がない", f, node)
			}
			close2(t, "V("+node+") @"+ftoa(f), res.NodeV[node], want, 1e-6)
		}
		// 素子電流。LTspice の I(...) は第1ノード→第2ノードの向きで、
		// これは独立源も含めて mna と同じ約束である（実測して確かめた）。
		for _, e := range nl.Elements {
			want, ok := rd.Get(pt, "I("+e.Name+")")
			if !ok {
				continue // LTspice が出していない素子は飛ばす
			}
			got := res.BranchI[strings.ToLower(e.Name)]
			close2(t, "I("+e.Name+") @"+ftoa(f), got, want, 1e-6)
		}
	}
}

// TestTellegen は、全素子が吸収する平均電力の総和が 0 になることを見る。
// 回路全体の整合を一発で捉えられる。
func TestTellegen(t *testing.T) {
	for _, name := range []string{"20260906wptSSSP2.net", "20260913wpt1to3.net"} {
		t.Run(name, func(t *testing.T) {
			nl := load(t, name)
			vals := DefaultValues(nl)
			for _, f := range []float64{100e3, 223.6e3, 500e3} {
				res, err := Solve(nl, vals, 2*math.Pi*f)
				if err != nil {
					t.Fatal(err)
				}
				sum, scale := 0.0, 0.0
				for _, e := range nl.Elements {
					p := res.Power(e)
					sum += p
					if math.Abs(p) > scale {
						scale = math.Abs(p)
					}
				}
				// 恒等的に 0 の量なので、相対誤差ではなく
				// 「同じ種類の量のうち最大のもの」を尺度にする。
				if math.Abs(sum) > 1e-12*scale {
					t.Errorf("%g Hz: 電力の総和 %g（最大の素子は %g）", f, sum, scale)
				}
			}
		})
	}
}

// TestRCDivider は手で解ける回路で合わせる。
//
//	V1 ─ R ─┬─ C ─ 0
//	        n2
//
// V(n2) = V1 / (1 + jωRC)
func TestRCDivider(t *testing.T) {
	nl := parse(t, "* RC\nV1 n1 0 AC 1\nR1 n1 n2 1k\nC1 n2 0 1n\n.ac dec 10 1k 1meg\n.end\n")
	const r, c = 1e3, 1e-9
	for _, f := range []float64{1e3, 159.1549e3, 1e6} {
		w := 2 * math.Pi * f
		res, err := Solve(nl, DefaultValues(nl), w)
		if err != nil {
			t.Fatal(err)
		}
		want := 1 / (1 + complex(0, w*r*c))
		close2(t, "V(n2) @"+ftoa(f), res.NodeV["n2"], want, 1e-12)
		// 抵抗を流れる電流は (V1 − V2)/R。
		close2(t, "I(R1) @"+ftoa(f), res.BranchI["r1"], (1-want)/complex(r, 0), 1e-12)
	}
}

// TestSeriesRLC は直列 RLC の共振点を見る。ω = 1/√(LC) で L と C の
// リアクタンスが打ち消し合い、電流は V/R ちょうどになる。
func TestSeriesRLC(t *testing.T) {
	nl := parse(t, "* RLC\nV1 n1 0 AC 2\nR1 n1 n2 5\nL1 n2 n3 100u\nC1 n3 0 3.9n\n.ac dec 10 1k 1meg\n.end\n")
	const l, c, r = 100e-6, 3.9e-9, 5.0
	w := 1 / math.Sqrt(l*c)
	res, err := Solve(nl, DefaultValues(nl), w)
	if err != nil {
		t.Fatal(err)
	}
	close2(t, "共振時の I(R1)", res.BranchI["r1"], complex(2/r, 0), 1e-9)

	// 共振時は L と C の電圧が打ち消し合うので V(n2) = 0。
	close2(t, "共振時の V(n2)", res.NodeV["n2"], 0, 1e-9)

	// 電力: 抵抗だけが吸収し、電源はその分を出す。
	pr := res.Power(nl.Elements[1])
	if math.Abs(pr-0.5*(2/r)*(2/r)*r) > 1e-9 {
		t.Errorf("R1 の吸収電力 %g, 期待 %g", pr, 0.5*(2/r)*(2/r)*r)
	}
	if math.Abs(res.Power(nl.Elements[0])+pr) > 1e-9 {
		t.Errorf("電源 %g と抵抗 %g が釣り合わない", res.Power(nl.Elements[0]), pr)
	}
}

// TestCoupledInductors は結合した 2 つのコイルを手計算と合わせる。
//
// 一次側を電流源 I で駆動し、二次側を開放すると、二次側の開放電圧は
// jωM·I になる（M = k√(L1L2)）。
func TestCoupledInductors(t *testing.T) {
	nl := parse(t, "* coupled\nI1 0 n1 AC 1\nL1 n1 0 100u\nL2 n2 0 400u\nR2 n2 0 1g\nK1 L1 L2 0.5\n.ac dec 10 1k 1meg\n.end\n")
	const l1, l2, k = 100e-6, 400e-6, 0.5
	m := k * math.Sqrt(l1*l2)
	w := 2 * math.Pi * 100e3
	res, err := Solve(nl, DefaultValues(nl), w)
	if err != nil {
		t.Fatal(err)
	}
	// R2 は 1GΩ なのでほぼ開放。相対誤差で 1e-6 くらいには合う。
	close2(t, "V(n2)", res.NodeV["n2"], complex(0, w*m), 1e-5)
	// 一次側は自分のリアクタンスだけを見る（二次が開放なので反射がない）。
	close2(t, "V(n1)", res.NodeV["n1"], complex(0, w*l1), 1e-5)
}

// TestMissingValue は、値のない素子を黙って 0 として扱わないことを見る。
func TestMissingValue(t *testing.T) {
	nl := load(t, "20260906wptSSSP2.net")
	v := DefaultValues(nl)
	delete(v.Elem, "rs")
	if _, err := Solve(nl, v, 2*math.Pi*100e3); err == nil {
		t.Fatal("値がないのに解けてしまった")
	}
	v = DefaultValues(nl)
	delete(v.K, "k12")
	if _, err := Solve(nl, v, 2*math.Pi*100e3); err == nil {
		t.Fatal("結合係数がないのに解けてしまった")
	}
}

// TestDetectsBrokenSolve は検算が検算として働いているかを確かめる。
// わざと値を1つ狂わせて、LTspice との突き合わせが必ず気づくことを見る。
func TestDetectsBrokenSolve(t *testing.T) {
	nl := load(t, "20260913wpt1to3.net")
	rd, err := raw.Read(filepath.Join("..", "..", "testdata", "20260913wpt1to3.raw"))
	if err != nil {
		t.Fatal(err)
	}
	vals := DefaultValues(nl)
	vals.Elem["rs"] *= 1.001 // 0.1% だけずらす
	res, err := Solve(nl, vals, 2*math.Pi*rd.Freqs[350])
	if err != nil {
		t.Fatal(err)
	}
	want, _ := rd.NodeVoltage(350, "y1")
	if cmplx.Abs(res.NodeV["y1"]-want) <= 1e-6*(1+cmplx.Abs(want)) {
		t.Fatal("素子値を狂わせたのに差が出ない。突き合わせが効いていない")
	}
}

// ftoa は失敗したときのメッセージを読みやすくするためだけのもの。
func ftoa(f float64) string {
	if f >= 1e3 {
		return strconv.FormatFloat(f/1e3, 'g', 6, 64) + "kHz"
	}
	return strconv.FormatFloat(f, 'g', 6, 64) + "Hz"
}
