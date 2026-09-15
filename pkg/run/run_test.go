package run

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
)

func load(t *testing.T, name string) *netlist.Netlist {
	t.Helper()
	nl, err := netlist.Load(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return nl
}

func TestParseFreqList(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []float64
	}{
		{"", nil},
		{"   ", nil},
		{"100k", []float64{100e3}},
		{"100k,200k,1meg", []float64{100e3, 200e3, 1e6}},
		{" 100k , 200k ", []float64{100e3, 200e3}},
		{"100k,あ,200k", []float64{100e3, 200e3}}, // 読めないものは捨てる
	} {
		got := ParseFreqList(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ParseFreqList(%q) = %v, 期待 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseFreqList(%q)[%d] = %g, 期待 %g", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestACList(t *testing.T) {
	got := ACList([]float64{100e3, 223606.79774997896})
	if !strings.HasPrefix(got, ".ac list ") {
		t.Fatalf("= %q", got)
	}
	// 17 桁で書くので、読み直しても値が変わらない。
	back := ParseFreqList(strings.ReplaceAll(
		strings.TrimPrefix(got, ".ac list "), " ", ","))
	want := []float64{100e3, 223606.79774997896}
	if len(back) != len(want) {
		t.Fatalf("読み直すと %v", back)
	}
	for i := range back {
		if back[i] != want[i] {
			t.Errorf("読み直すと [%d] = %.17g, 期待 %.17g", i, back[i], want[i])
		}
	}
}

func TestDefaultFreqsFromDec(t *testing.T) {
	// testdata の .ac は "dec 1000 100k 500k"。対数等間隔に 5 点。
	nl := load(t, "20260913wpt1to3.net")
	got := DefaultFreqs(nl)
	if len(got) != 5 {
		t.Fatalf("%d 点, 期待 5: %v", len(got), got)
	}
	if math.Abs(got[0]-100e3) > 1e-6 || math.Abs(got[4]-500e3) > 1e-6 {
		t.Errorf("両端 %g, %g, 期待 100k, 500k", got[0], got[4])
	}
	// 対数等間隔なら、隣り合う比がどこでも同じ。
	r := got[1] / got[0]
	for i := 2; i < len(got); i++ {
		if math.Abs(got[i]/got[i-1]-r) > 1e-9*r {
			t.Errorf("[%d]/[%d] = %g, 期待 %g", i, i-1, got[i]/got[i-1], r)
		}
	}
}

func TestDefaultFreqsFromList(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.net")
	src := "* t\nV1 n1 0 AC 1\nR1 n1 0 1k\n.ac list 100k 250k 400k\n.end\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	nl, err := netlist.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	got := DefaultFreqs(nl)
	want := []float64{100e3, 250e3, 400e3}
	if len(got) != len(want) {
		t.Fatalf("= %v, 期待 %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %g, 期待 %g", i, got[i], want[i])
		}
	}
}

func TestDefaultFreqsNoAC(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.net")
	if err := os.WriteFile(p, []byte("* t\nV1 n1 0 AC 1\nR1 n1 0 1k\n.end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nl, err := netlist.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	got := DefaultFreqs(nl)
	if len(got) != 3 || got[0] != 100e3 {
		t.Errorf("= %v, 期待 [100k 200k 500k]", got)
	}
}

// TestFindMissing は、無いものを指したときに黙って別のものを返さないことを見る。
func TestFindMissing(t *testing.T) {
	for _, ev := range EnvVars {
		t.Setenv(ev, "")
	}
	hint := filepath.Join(t.TempDir(), "notthere.exe")
	got, err := Find(hint)
	if err == nil && got == hint {
		t.Fatalf("無い実行ファイル %q をそのまま返した", got)
	}
	// PATH に本物があれば見つかるのが正しい。無ければエラー。
	if err != nil {
		t.Logf("見つからず: %v", err)
	} else {
		t.Logf("PATH から見つかった: %s", got)
	}
}

// TestFindUsesEnv は環境変数で指した実行ファイルを優先することを見る。
func TestFindUsesEnv(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "LTspice.exe")
	if err := os.WriteFile(fake, []byte("dummy"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ev := range EnvVars {
		t.Setenv(ev, "")
	}
	t.Setenv(EnvVars[0], fake)
	got, err := Find("")
	if err != nil {
		t.Fatal(err)
	}
	if got != fake {
		t.Errorf("Find = %q, 期待 %q", got, fake)
	}
}

// TestBatchRealLTspice は LTspice が入っていれば実際に走らせる。
// 入っていなければ飛ばす（CI では飛ぶ）。
func TestBatchRealLTspice(t *testing.T) {
	exe, err := Find("")
	if err != nil {
		t.Skip("LTspice がないので飛ばす:", err)
	}
	src := "* RC\nV1 n1 0 AC 1\nR1 n1 n2 1k\nC1 n2 0 1n\n" +
		ACList([]float64{100e3}) + "\n.end\n"
	d, err := Batch(exe, filepath.Join(t.TempDir(), "t.net"), src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Vals) != 1 {
		t.Fatalf("%d 点, 期待 1", len(d.Vals))
	}
	v, ok := d.NodeVoltage(0, "n2")
	if !ok {
		t.Fatal("V(n2) がない")
	}
	w := 2 * math.Pi * 100e3
	want := 1 / (1 + complex(0, w*1e3*1e-9))
	if d := real(v) - real(want); math.Abs(d) > 1e-9 {
		t.Errorf("V(n2) = %v, 期待 %v", v, want)
	}
}
