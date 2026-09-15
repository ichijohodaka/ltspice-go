package raw

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/cmplx"
	"testing"
	"unicode/utf16"
)

const rawPath = "../../testdata/20260913wpt1to3.raw"

// TestReadXVII は LTspice XVII が書いた .raw を読む。
// 頭は UTF-16LE（BOM なし）、数値は Binary:。どちらを取り違えても読めない。
func TestReadXVII(t *testing.T) {
	d, err := Read(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	if d.Flags != "complex forward log" {
		t.Errorf("Flags = %q", d.Flags)
	}
	if len(d.Vars) != 33 {
		t.Fatalf("変数 %d 個, 期待 33", len(d.Vars))
	}
	if len(d.Vals) != 700 {
		t.Fatalf("点 %d 個, 期待 700", len(d.Vals))
	}
	if len(d.Freqs) != len(d.Vals) {
		t.Fatalf("Freqs %d 個, Vals %d 個", len(d.Freqs), len(d.Vals))
	}
	if d.Vars[0] != "frequency" {
		t.Errorf("Vars[0] = %q, 期待 \"frequency\"", d.Vars[0])
	}
	// .ac dec 1000 100k 500k なので、両端は 100k と 500k。
	if math.Abs(d.Freqs[0]-100e3) > 1 {
		t.Errorf("先頭の周波数 %g, 期待 100k", d.Freqs[0])
	}
	if math.Abs(d.Freqs[len(d.Freqs)-1]-500e3) > 1 {
		t.Errorf("末尾の周波数 %g, 期待 500k", d.Freqs[len(d.Freqs)-1])
	}
	for i := 1; i < len(d.Freqs); i++ {
		if d.Freqs[i] <= d.Freqs[i-1] {
			t.Fatalf("周波数が単調でない: [%d]=%g [%d]=%g", i-1, d.Freqs[i-1], i, d.Freqs[i])
		}
	}
}

// TestPhysics は読めた値が回路として辻褄が合うことを見る。
// 文字コードや形式を取り違えて「それらしい数字」が出ていないかの歯止め。
func TestPhysics(t *testing.T) {
	d, err := Read(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range []int{0, 350, 699} {
		// VS N003 0 AC 5 なので、V(n003) はどの周波数でも 5+0j。
		v, ok := d.Get(pt, "V(N003)") // 大文字で引いても引ける
		if !ok {
			t.Fatalf("点 %d: V(n003) がない", pt)
		}
		if cmplx.Abs(v-5) > 1e-9 {
			t.Errorf("点 %d: V(n003) = %v, 期待 5", pt, v)
		}
		// RS y1 N003 2.8 の両端の差を抵抗で割ると I(Rs) になる。
		vy1, ok1 := d.NodeVoltage(pt, "y1")
		i, ok2 := d.Get(pt, "I(Rs)")
		if !ok1 || !ok2 {
			t.Fatalf("点 %d: V(y1) または I(Rs) がない", pt)
		}
		// LTspice の素子電流は第1ノード → 第2ノードの向き。
		want := (vy1 - v) / 2.8
		if cmplx.Abs(want-i) > 1e-9*(1+cmplx.Abs(i)) {
			t.Errorf("点 %d: I(Rs) = %v, オームの法則からは %v", pt, i, want)
		}
	}
	if _, ok := d.Get(0, "v(nosuchnode)"); ok {
		t.Error("ない変数が見つかったことになっている")
	}
	if _, ok := d.Get(-1, "frequency"); ok {
		t.Error("範囲外の点が取れてしまう")
	}
	if v, ok := d.NodeVoltage(0, "0"); !ok || v != 0 {
		t.Errorf("NodeVoltage(0, \"0\") = %v, %v", v, ok)
	}
}

// 小さな例。Values: と Binary: の両方をこれで組み立てる。
var (
	sampleHeader = "Title: * test\n" +
		"Date: Mon Sep 14 15:27:27 2026\n" +
		"Plotname: AC Analysis\n" +
		"Flags: complex forward log\n" +
		"No. Variables: 2\n" +
		"No. Points: 2\n" +
		"Offset:   0.0000000000000000e+000\n" +
		"Variables:\n" +
		"\t0\tfrequency\tfrequency\n" +
		"\t1\tV(n1)\tvoltage\n"
	sampleVals = [][]complex128{
		{complex(100e3, 0), complex(2, -3)},
		{complex(200e3, 0), complex(4, -5)},
	}
)

func checkSample(t *testing.T, d *Data) {
	t.Helper()
	if len(d.Vars) != 2 || len(d.Vals) != 2 {
		t.Fatalf("Vars %d, Vals %d", len(d.Vars), len(d.Vals))
	}
	if d.Vars[0] != "frequency" || d.Vars[1] != "v(n1)" {
		t.Errorf("Vars = %v", d.Vars)
	}
	if d.Freqs[0] != 100e3 || d.Freqs[1] != 200e3 {
		t.Errorf("Freqs = %v", d.Freqs)
	}
	for p, row := range sampleVals {
		if v, _ := d.Get(p, "v(n1)"); v != row[1] {
			t.Errorf("点%d V(n1) = %v, 期待 %v", p, v, row[1])
		}
	}
}

// TestEncodings は同じ中身を 5 通りの文字コードで書いて、どれも同じに
// 読めることを確かめる。Values: と Binary: の両方で見る。
func TestEncodings(t *testing.T) {
	ascii := []byte(sampleHeader + "Values:\n" +
		"\t0\t1.0e+05,0.0e+00\n\t\t2.0e+00,-3.0e+00\n" +
		"\t1\t2.0e+05,0.0e+00\n\t\t4.0e+00,-5.0e+00\n")

	bin := append([]byte(sampleHeader+"Binary:\n"), encodeBinary(sampleVals)...)

	for _, form := range []struct {
		name string
		body []byte
		// Binary: は数値をバイトのまま置くので、頭だけを変換する。
		headOnly bool
	}{
		{name: "values", body: ascii},
		{name: "binary", body: bin, headOnly: true},
	} {
		for _, c := range []struct {
			name string
			enc  func([]byte, bool) []byte
		}{
			{"utf8", func(b []byte, _ bool) []byte { return b }},
			{"utf8-bom", func(b []byte, _ bool) []byte { return append([]byte{0xEF, 0xBB, 0xBF}, b...) }},
			{"utf16le-bom", func(b []byte, h bool) []byte { return toUTF16(b, h, false, true) }},
			{"utf16le-nobom", func(b []byte, h bool) []byte { return toUTF16(b, h, false, false) }},
			{"utf16be-bom", func(b []byte, h bool) []byte { return toUTF16(b, h, true, true) }},
		} {
			t.Run(form.name+"/"+c.name, func(t *testing.T) {
				d, err := ReadFrom(bytes.NewReader(c.enc(form.body, form.headOnly)))
				if err != nil {
					t.Fatal(err)
				}
				checkSample(t, d)
			})
		}
	}
}

// TestPointCountMismatch は、点数が宣言と食い違うときに黙って通さないことを見る。
func TestPointCountMismatch(t *testing.T) {
	body := []byte(sampleHeader + "Values:\n" +
		"\t0\t1.0e+05,0.0e+00\n\t\t2.0e+00,-3.0e+00\n") // 1 点しかない
	if _, err := ReadFrom(bytes.NewReader(body)); err == nil {
		t.Fatal("点数が足りないのに通った")
	}
}

// TestNoValuesSection は、見出しがないものを弾くことを見る。
func TestNoValuesSection(t *testing.T) {
	if _, err := ReadFrom(bytes.NewReader([]byte(sampleHeader))); err == nil {
		t.Fatal("Values: も Binary: もないのに通った")
	}
}

// TestBinaryNonAC は、複素数でない Binary: を黙って読まないことを見る。
// 過渡解析は 1 変数あたりのバイト数が違うので、読めば必ず化ける。
func TestBinaryNonAC(t *testing.T) {
	head := "Title: * test\nPlotname: Transient Analysis\nFlags: real forward\n" +
		"No. Variables: 2\nNo. Points: 2\nVariables:\n\t0\ttime\ttime\n\t1\tV(n1)\tvoltage\nBinary:\n"
	body := append([]byte(head), encodeBinary(sampleVals)...)
	_, err := ReadFrom(bytes.NewReader(body))
	if err == nil {
		t.Fatal("過渡解析の Binary: を読んでしまった")
	}
	t.Log(err)
}

// TestSurrogate は BMP 外の文字（サロゲート対）を通す。
// .raw の中身には出てこないが、Title: にファイル名が入るので、
// そういう文字を含むパスでも壊れないようにしておく。
func TestSurrogate(t *testing.T) {
	const title = "* 𝄞 と ✓"
	body := []byte("Title: " + title + "\n" + sampleHeader[len("Title: * test\n"):] +
		"Values:\n\t0\t1.0e+05,0.0e+00\n\t\t2.0e+00,-3.0e+00\n" +
		"\t1\t2.0e+05,0.0e+00\n\t\t4.0e+00,-5.0e+00\n")
	d, err := ReadFrom(bytes.NewReader(toUTF16(body, false, false, true)))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != title {
		t.Errorf("Title = %q, 期待 %q", d.Title, title)
	}
	checkSample(t, d)
}

func encodeBinary(rows [][]complex128) []byte {
	var b bytes.Buffer
	for _, row := range rows {
		for _, v := range row {
			binary.Write(&b, binary.LittleEndian, real(v))
			binary.Write(&b, binary.LittleEndian, imag(v))
		}
	}
	return b.Bytes()
}

// toUTF16 は UTF-16 に直す。headOnly なら "Binary:\n" までだけを直し、
// 残りのバイトはそのまま繋ぐ（LTspice の Binary: がその形）。
func toUTF16(b []byte, headOnly, big, bom bool) []byte {
	head, tail := b, []byte(nil)
	if headOnly {
		const marker = "Binary:\n"
		if i := bytes.Index(b, []byte(marker)); i >= 0 {
			head, tail = b[:i+len(marker)], b[i+len(marker):]
		}
	}
	us := utf16.Encode([]rune(string(head)))
	out := make([]byte, 0, 2*len(us)+2+len(tail))
	put := func(v uint16) {
		if big {
			out = append(out, byte(v>>8), byte(v))
		} else {
			out = append(out, byte(v), byte(v>>8))
		}
	}
	if bom {
		put(0xFEFF)
	}
	for _, v := range us {
		put(v)
	}
	return append(out, tail...)
}
