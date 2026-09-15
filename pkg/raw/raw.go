// Package raw は LTspice の .raw（AC 解析）を読む。
//
// # 2 つの形式
//
// LTspice は .raw を 2 通りの形で書く。どちらも頭は同じ体裁のテキストで、
// 最後の行が形式を決める。
//
//	Values:   数値も текст。-ascii を付けて走らせたときの形
//	Binary:   数値は little-endian の float64 が並んだもの。既定の形
//
// バッチ実行では -ascii を付けるのが普通だが、GUI で走らせたものを後から
// 読むときは Binary: になる。どちらも読める。
//
// # 文字コード
//
// 頭のテキストの文字コードも版によって違う。LTspice XVII は UTF-16LE
// （BOM なし）で書き、ADI の LTspice 26 は素の ASCII で書く。先頭の
// 2 バイトから見分ける。
//
// 見分けそこねると「データがありません」になるだけで黙って壊れはしないが、
// 版を変えた途端に読めなくなるので、ここで吸収しておく。
package raw

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Data は読み取った AC 解析の結果。
type Data struct {
	Title string
	Flags string
	Vars  []string       // 変数名（小文字）
	Vals  [][]complex128 // [点][変数]
	Freqs []float64      // Vals[i][0] の実部。Vals と同じ長さ
}

// Read はファイルを読む。
func Read(path string) (*Data, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d, err := ReadFrom(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// ReadFrom は開いてあるものから読む。文字コードと形式は中身から見分ける。
func ReadFrom(r io.Reader) (*Data, error) {
	lr, err := newLineReader(r)
	if err != nil {
		return nil, err
	}

	d := &Data{}
	nVars, nPoints := 0, 0
	inVars := false

	for {
		line, err := lr.Line()
		if err == io.EOF {
			return nil, fmt.Errorf("Values: も Binary: も見つかりません")
		}
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		switch {
		case trimmed == "Values:":
			if err := d.readASCII(lr, nVars, nPoints); err != nil {
				return nil, err
			}
			return d, d.check(nPoints)
		case trimmed == "Binary:":
			if err := d.readBinary(lr.br, nVars, nPoints); err != nil {
				return nil, err
			}
			return d, d.check(nPoints)
		case strings.HasPrefix(trimmed, "Title:"):
			d.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "Title:"))
			continue
		case strings.HasPrefix(trimmed, "Flags:"):
			d.Flags = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "Flags:")))
			continue
		case strings.HasPrefix(trimmed, "No. Variables:"):
			nVars, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "No. Variables:")))
			continue
		case strings.HasPrefix(trimmed, "No. Points:"):
			nPoints, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "No. Points:")))
			continue
		case trimmed == "Variables:":
			inVars = true
			continue
		case !strings.HasPrefix(line, "\t") && strings.Contains(line, ":"):
			// Date:, Offset:, Command: など、知らない見出しは読み飛ばす。
			inVars = false
			continue
		}

		if inVars {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 {
				d.Vars = append(d.Vars, strings.ToLower(fields[1]))
			}
		}
	}
}

func (d *Data) check(nPoints int) error {
	if nPoints > 0 && len(d.Vals) != nPoints {
		return fmt.Errorf("点数が合いません (%d / %d)", len(d.Vals), nPoints)
	}
	if len(d.Vals) == 0 {
		return fmt.Errorf("データがありません")
	}
	return nil
}

// readASCII は Values: 以降を読む。1 点が
//
//	<点番号>\t<変数0 の値>
//	\t<変数1 の値>
//	…
//
// という形で並んでいる。値は "実部,虚部"。
func (d *Data) readASCII(lr *lineReader, nVars, nPoints int) error {
	if nVars <= 0 {
		return fmt.Errorf("No. Variables: がありません")
	}
	var cur []complex128
	for {
		line, err := lr.Line()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		v, err := parseComplex(fields[len(fields)-1])
		if err != nil {
			return fmt.Errorf("値を解釈できません: %q", trimmed)
		}
		cur = append(cur, v)
		if len(cur) == nVars {
			d.Vals = append(d.Vals, cur)
			d.Freqs = append(d.Freqs, real(cur[0]))
			cur = nil
			if nPoints > 0 && len(d.Vals) == nPoints {
				break
			}
		}
	}
	return nil
}

// readBinary は Binary: 以降を読む。AC 解析では 1 変数が
// little-endian の float64 2 つ（実部・虚部）で、それが変数の数だけ
// 並んだものが 1 点になる。
func (d *Data) readBinary(br *bufio.Reader, nVars, nPoints int) error {
	if nVars <= 0 {
		return fmt.Errorf("No. Variables: がありません")
	}
	if !strings.Contains(d.Flags, "complex") {
		return fmt.Errorf("Binary: の .raw は AC 解析（Flags に complex）のものだけ読める（Flags: %s）。"+
			"過渡解析なら -ascii を付けて走らせ直してください", d.Flags)
	}
	buf := make([]byte, 16*nVars)
	for nPoints <= 0 || len(d.Vals) < nPoints {
		if _, err := io.ReadFull(br, buf); err != nil {
			if err == io.EOF {
				break
			}
			if err == io.ErrUnexpectedEOF {
				return fmt.Errorf("%d 点目の途中でファイルが終わりました", len(d.Vals)+1)
			}
			return err
		}
		row := make([]complex128, nVars)
		for v := range row {
			re := math.Float64frombits(binary.LittleEndian.Uint64(buf[16*v:]))
			im := math.Float64frombits(binary.LittleEndian.Uint64(buf[16*v+8:]))
			row[v] = complex(re, im)
		}
		d.Vals = append(d.Vals, row)
		d.Freqs = append(d.Freqs, real(row[0]))
	}
	return nil
}

func parseComplex(s string) (complex128, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		// 実数のみ
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		return complex(v, 0), nil
	}
	re, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, err
	}
	im, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, err
	}
	return complex(re, im), nil
}

// Get は指定した点・変数名の値を返す。変数名の大文字小文字は問わない。
func (d *Data) Get(point int, name string) (complex128, bool) {
	if point < 0 || point >= len(d.Vals) {
		return 0, false
	}
	name = strings.ToLower(name)
	for i, v := range d.Vars {
		if v == name {
			return d.Vals[point][i], true
		}
	}
	return 0, false
}

// NodeVoltage はノード電圧（接地なら 0）。
func (d *Data) NodeVoltage(point int, node string) (complex128, bool) {
	if node == "0" {
		return 0, true
	}
	return d.Get(point, "v("+node+")")
}

// --- 文字コード ---------------------------------------------------------

type enc int

const (
	encUTF8 enc = iota
	encUTF16LE
	encUTF16BE
)

// lineReader は頭のテキストを1行ずつ返す。
//
// 行の区切りまでの**バイト数**を文字コードに合わせて正確に食べるので、
// Binary: の行を読んだ直後の br には、数値の先頭バイトがそのまま残る。
// 全体をまとめて文字列に直してしまうと、この境目で数値が壊れる。
type lineReader struct {
	br *bufio.Reader
	e  enc
}

func newLineReader(r io.Reader) (*lineReader, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	head, err := br.Peek(2)
	if err != nil && err != io.EOF {
		return nil, err
	}
	l := &lineReader{br: br, e: encUTF8}
	if len(head) < 2 {
		return l, nil
	}
	switch {
	case head[0] == 0xFF && head[1] == 0xFE:
		br.Discard(2)
		l.e = encUTF16LE
	case head[0] == 0xFE && head[1] == 0xFF:
		br.Discard(2)
		l.e = encUTF16BE
	case head[0] != 0 && head[1] == 0:
		l.e = encUTF16LE // BOM なし（LTspice XVII）
	case head[0] == 0 && head[1] != 0:
		l.e = encUTF16BE
	case head[0] == 0xEF && head[1] == 0xBB:
		if h3, err := br.Peek(3); err == nil && h3[2] == 0xBF {
			br.Discard(3)
		}
	}
	return l, nil
}

// Line は改行を含まない1行を返す。末尾の \r は落とす。
func (l *lineReader) Line() (string, error) {
	if l.e == encUTF8 {
		s, err := l.br.ReadString('\n')
		if err != nil && (err != io.EOF || s == "") {
			return "", err
		}
		return strings.TrimRight(s, "\r\n"), nil
	}
	var us []uint16
	var unit [2]byte
	for {
		if _, err := io.ReadFull(l.br, unit[:]); err != nil {
			if (err == io.EOF || err == io.ErrUnexpectedEOF) && len(us) > 0 {
				break
			}
			if err == io.ErrUnexpectedEOF {
				err = io.EOF
			}
			return "", err
		}
		var v uint16
		if l.e == encUTF16BE {
			v = uint16(unit[0])<<8 | uint16(unit[1])
		} else {
			v = uint16(unit[1])<<8 | uint16(unit[0])
		}
		if v == '\n' {
			break
		}
		us = append(us, v)
	}
	return strings.TrimRight(string(utf16.Decode(us)), "\r"), nil
}
