// Package asc は LTspice の回路図ファイル（.asc）を読み、SVG にする。
//
// # 配置を決める必要がない
//
// .asc には**線と素子の座標がそのまま入っている**。LTspice が置いた通りの
// 位置が書いてあるので、読む側は配置を考えなくてよく、引かれている線を
// 写すだけで図になる。
//
//	SHEET 1 1076 680            図面の大きさ
//	WIRE 144 -32 -32 -32        線（両端の座標）
//	SYMBOL res -48 0 R0         素子の種類・位置・向き
//	SYMATTR InstName RS         直前の素子の属性
//	FLAG -32 272 0              接地などのラベル
//	TEXT -64 304 Left 2 !.ac …  ディレクティブや注記
//
// # 素子の絵
//
// LTspice は素子の形を .asy に持っているが、ここでは扱う 4 種類
// （res / cap / ind2 / voltage）の形を組み込みで持つ。ネットリスト側が
// R, C, L, V, I しか受け付けないので、これで足りる。知らない種類に
// 出会ったら、黙って省かずに四角と種類名を描く。
package asc

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Point は図面上の座標。LTspice の単位そのままで、y は下向き。
type Point struct{ X, Y int }

// Wire は結線。
type Wire struct{ A, B Point }

// Symbol は部品の1つ。
type Symbol struct {
	Type string            // "res" / "cap" / "ind2" / "voltage" など
	At   Point             // 部品の原点（絵の座標はここからの相対）
	Rot  string            // "R0" / "R90" / "R180" / "R270" / "M0" …
	Attr map[string]string // InstName, Value, Value2, SpiceLine …
}

// Name は素子名（InstName）。
func (s Symbol) Name() string { return s.Attr["InstName"] }

// Value は表示する値。Value が空（あるいは "" と書かれている）なら Value2 を使う。
// 独立源は SYMATTR Value "" / SYMATTR Value2 AC 5 と書かれることがある。
func (s Symbol) Value() string {
	v := strings.TrimSpace(s.Attr["Value"])
	if v == "" || v == `""` {
		return strings.TrimSpace(s.Attr["Value2"])
	}
	return v
}

// Flag はノードのラベル。Name が "0" なら接地。
type Flag struct {
	At   Point
	Name string
}

// IsGround は接地か。
func (f Flag) IsGround() bool { return f.Name == "0" }

// Text は図面に書かれた文字（ディレクティブや注記）。
type Text struct {
	At   Point
	Body string
	Cmd  bool // 先頭が "!" のディレクティブか
}

// Schematic は読み取った回路図。
type Schematic struct {
	Version string
	Sheet   Point // 図面の大きさ
	Wires   []Wire
	Symbols []Symbol
	Flags   []Flag
	Texts   []Text

	// Unknown は絵を持っていない部品の種類（描くときに四角で代える）。
	Unknown []string
}

// Load はファイルを読む。
func Load(path string) (*Schematic, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse は開いてあるものを読む。
//
// # 文字コードについて、実際に確かめたこと
//
// 手元の .asc を 9 個調べた（LTspice 17.0.37.0 が書いたものと、新しい版が
// 書いたもの、Version 4 と 4.1 の両方）。分かったのは次のとおり。
//
//   - **どれも単バイトだった。** UTF-16 のものは1つも無い
//   - 改行はどれも CRLF
//   - 非 ASCII は素子値の µ だけで、0xB5 の**1バイト**（＝ Windows-1252 /
//     Latin-1）。UTF-8 の µ は 2 バイトなので、UTF-8 として読むと壊れる
//   - **同じ µ でもファイルの種類でバイトが違う。** .asc は 0xB5 の1バイト、
//     .net は 0xC2 0xB5 の2バイト（UTF-8）。同じ LTspice が同じ回路から
//     書いたものでもそうなる
//   - µ が入るかどうかは**版と関係がなかった**。同じ回路・同じ版で、
//     100u と書かれたものと 100µ と書かれたものの両方がある
//
// そこで、UTF-8 として通ればそのまま、通らなければ Windows-1252 とみなす。
//
// UTF-16 の見分けも入れてあるが、これは .raw の読み取り（そちらは本当に
// UTF-16LE のものがある）と同じ仕組みを使い回しているだけで、**UTF-16 の
// .asc は見たことがない**。害は無いので残してある。
func Parse(r io.Reader) (*Schematic, error) {
	lr, err := newLineReader(r)
	if err != nil {
		return nil, err
	}
	s := &Schematic{}
	var cur *Symbol
	unknown := map[string]bool{}

	for {
		line, err := lr.Line()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "Version":
			if len(f) >= 2 {
				s.Version = f[1]
			}
		case "SHEET":
			if len(f) >= 4 {
				s.Sheet = Point{atoi(f[2]), atoi(f[3])}
			}
		case "WIRE":
			if len(f) < 5 {
				return nil, fmt.Errorf("WIRE の座標が足りません: %q", line)
			}
			s.Wires = append(s.Wires, Wire{
				Point{atoi(f[1]), atoi(f[2])}, Point{atoi(f[3]), atoi(f[4])}})
		case "SYMBOL":
			if len(f) < 5 {
				return nil, fmt.Errorf("SYMBOL の項目が足りません: %q", line)
			}
			s.Symbols = append(s.Symbols, Symbol{
				Type: f[1], At: Point{atoi(f[2]), atoi(f[3])}, Rot: f[4],
				Attr: map[string]string{},
			})
			cur = &s.Symbols[len(s.Symbols)-1]
			if _, ok := glyphs[f[1]]; !ok {
				unknown[f[1]] = true
			}
		case "SYMATTR":
			if cur == nil || len(f) < 2 {
				continue
			}
			cur.Attr[f[1]] = strings.TrimSpace(strings.Join(f[2:], " "))
		case "FLAG":
			if len(f) >= 4 {
				s.Flags = append(s.Flags, Flag{Point{atoi(f[1]), atoi(f[2])}, f[3]})
			}
		case "TEXT":
			// TEXT <x> <y> <向き> <大きさ> <中身>
			if len(f) < 6 {
				continue
			}
			body := strings.Join(f[5:], " ")
			cmd := strings.HasPrefix(body, "!")
			s.Texts = append(s.Texts, Text{
				At: Point{atoi(f[1]), atoi(f[2])}, Body: strings.TrimPrefix(body, "!"), Cmd: cmd})
		case "WINDOW", "DATAFLAG", "IOPIN", "LINE", "RECTANGLE", "CIRCLE", "ARC":
			// 表示位置の指定や飾り。図の意味を変えないので読み飛ばす。
		}
	}
	for k := range unknown {
		s.Unknown = append(s.Unknown, k)
	}
	if len(s.Wires) == 0 && len(s.Symbols) == 0 {
		return nil, fmt.Errorf("WIRE も SYMBOL も見つかりません（.asc ではないかもしれません）")
	}
	return s, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// --- 文字コード ---------------------------------------------------------
//
// 手元の .asc はすべて単バイト（CRLF）だった。UTF-16 の見分けは .raw の
// 読み取りと同じ仕組みを使い回しているだけで、UTF-16 の .asc は見ていない。

type enc int

const (
	encUTF8 enc = iota
	encUTF16LE
	encUTF16BE
)

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
		l.e = encUTF16LE
	case head[0] == 0 && head[1] != 0:
		l.e = encUTF16BE
	case head[0] == 0xEF && head[1] == 0xBB:
		if h3, err := br.Peek(3); err == nil && h3[2] == 0xBF {
			br.Discard(3)
		}
	}
	return l, nil
}

func (l *lineReader) Line() (string, error) {
	if l.e == encUTF8 {
		s, err := l.br.ReadString('\n')
		if err != nil && (err != io.EOF || s == "") {
			return "", err
		}
		return strings.TrimRight(decodeText([]byte(s)), "\r\n"), nil
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
	return strings.TrimRight(decodeUTF16(us), "\r"), nil
}

// cp1252 は Windows-1252 の 0x80〜0x9F を Unicode に直す表。
// それ以外は Latin-1（＝符号位置がそのままバイト値）と同じ。
var cp1252 = [32]rune{
	'\u20AC', '\uFFFD', '\u201A', '\u0192', '\u201E', '\u2026', '\u2020', '\u2021',
	'\u02C6', '\u2030', '\u0160', '\u2039', '\u0152', '\uFFFD', '\u017D', '\uFFFD',
	'\uFFFD', '\u2018', '\u2019', '\u201C', '\u201D', '\u2022', '\u2013', '\u2014',
	'\u02DC', '\u2122', '\u0161', '\u203A', '\u0153', '\uFFFD', '\u017E', '\u0178',
}

// decodeText は1行を文字列にする。
//
// 素子値の µ は 0xB5 の**1バイト**で入っている（Windows-1252 / Latin-1）。
// UTF-8 の µ は 2 バイトなので、UTF-8 として読むと壊れる。UTF-8 として
// 通らなければ Windows-1252 とみなして読み直す。
//
// .net 側（pkg/netlist）は µ も u も受けるので読めていたが、.asc の素子値を
// そのまま図に書くとここで化ける。
func decodeText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c < 0xA0:
			sb.WriteRune(cp1252[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}
