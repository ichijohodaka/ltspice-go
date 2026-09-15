package netlist

import (
	"path/filepath"
	"testing"
)

func exampleNet(t *testing.T) *Netlist {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "20260906wptSSSP2.net"))
	if err != nil {
		t.Fatal(err)
	}
	nl, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return nl
}

func TestParseExample(t *testing.T) {
	nl := exampleNet(t)
	if len(nl.Elements) != 14 {
		t.Fatalf("素子数 = %d, want 14", len(nl.Elements))
	}
	if len(nl.Couplings) != 3 {
		t.Fatalf("結合数 = %d, want 3", len(nl.Couplings))
	}
	vs := nl.FindElement("VS")
	if vs == nil || vs.Kind != KindV || vs.ACMag != 5 {
		t.Fatalf("VS = %+v", vs)
	}
	if vs.Nodes != [2]string{"n002", "0"} {
		t.Fatalf("VS nodes = %v", vs.Nodes)
	}
	l1 := nl.FindElement("L1")
	if l1 == nil || relDiff(l1.Value, 100e-6) > 1e-12 {
		t.Fatalf("L1 = %+v", l1)
	}
	c1 := nl.FindElement("C1")
	if c1 == nil || relDiff(c1.Value, 3.9e-9) > 1e-12 {
		t.Fatalf("C1 = %+v", c1)
	}
	if nl.AscPath == "" {
		t.Fatalf(".asc が見つかっていない")
	}
	for _, w := range nl.Warnings {
		t.Logf("warning: %s", w)
	}
	if len(nl.Warnings) != 0 {
		t.Fatalf(".asc との突き合わせで警告が出た: %v", nl.Warnings)
	}
}

func TestLoadFromAsc(t *testing.T) {
	p, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "20260906wptSSSP2.asc"))
	nl, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(nl.Elements) != 14 {
		t.Fatalf("素子数 = %d", len(nl.Elements))
	}
}

func TestParseValue(t *testing.T) {
	cases := map[string]float64{
		"2.8": 2.8, "100µ": 100e-6, "100u": 100e-6, "3.9n": 3.9e-9,
		"1Meg": 1e6, "1meg": 1e6, "1k": 1e3, "1m": 1e-3, "1e-6": 1e-6,
		"50": 50, "1p": 1e-12, "10G": 1e10, "1T": 1e12, "4.7uF": 4.7e-6,
		"-3": -3, "1e3k": 1e6,
	}
	for in, want := range cases {
		got, err := ParseValue(in)
		if err != nil {
			t.Errorf("ParseValue(%q) error: %v", in, err)
			continue
		}
		if relDiff(got, want) > 1e-12 {
			t.Errorf("ParseValue(%q) = %v want %v", in, got, want)
		}
	}
	if _, err := ParseValue("{Rx}"); err == nil {
		t.Errorf("式は未対応エラーになるべき")
	}
}
