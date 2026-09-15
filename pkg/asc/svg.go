package asc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// shape は素子の絵をつくる図形。座標は部品の原点からの相対。
type shape struct {
	kind string // "poly" / "arc" / "circle" / "dot"
	pts  []Point
	r    float64 // arc / circle / dot の半径
}

// glyphs は扱う部品の絵。LTspice の .asy に書かれている形に合わせてある。
//
// 座標は部品の原点から見たもので、ピンの位置も LTspice と同じ。旧
// wpt-2dplot-main が書き出した SVG と座標まで一致することを、テストで
// 確かめている。
var glyphs = map[string][]shape{
	// 抵抗。ピンは (16,16) と (16,96)。
	"res": {{kind: "poly", pts: []Point{
		{16, 16}, {16, 24}, {8, 30}, {24, 42}, {8, 54}, {24, 66}, {8, 78}, {16, 84}, {16, 96},
	}}},

	// コンデンサ。ピンは (16,0) と (16,64)。
	"cap": {
		{kind: "poly", pts: []Point{{16, 0}, {16, 28}}},
		{kind: "poly", pts: []Point{{2, 28}, {30, 28}}},
		{kind: "poly", pts: []Point{{2, 36}, {30, 36}}},
		{kind: "poly", pts: []Point{{16, 36}, {16, 64}}},
	},

	// 結合できるコイル。ピンは (16,16) と (16,96)。半円4つと極性の点。
	"ind2": {
		{kind: "arc", pts: []Point{{16, 16}, {16, 36}}, r: 10},
		{kind: "arc", pts: []Point{{16, 36}, {16, 56}}, r: 10},
		{kind: "arc", pts: []Point{{16, 56}, {16, 76}}, r: 10},
		{kind: "arc", pts: []Point{{16, 76}, {16, 96}}, r: 10},
		{kind: "dot", pts: []Point{{28, 22}}, r: 3.5},
	},

	// 独立電圧源。ピンは (0,16) と (0,96)。丸と ＋ − の印。
	"voltage": {
		{kind: "poly", pts: []Point{{0, 16}, {0, 32}}},
		{kind: "circle", pts: []Point{{0, 64}}, r: 32},
		{kind: "poly", pts: []Point{{0, 39}, {0, 53}}},  // ＋ の縦
		{kind: "poly", pts: []Point{{-7, 46}, {7, 46}}}, // ＋ の横
		{kind: "poly", pts: []Point{{-7, 82}, {7, 82}}}, // − の横
	},

	// 独立電流源。ピンは (0,16) と (0,96)。丸と矢印。
	"current": {
		{kind: "poly", pts: []Point{{0, 16}, {0, 32}}},
		{kind: "circle", pts: []Point{{0, 64}}, r: 32},
		{kind: "poly", pts: []Point{{0, 44}, {0, 84}}},
		{kind: "poly", pts: []Point{{-5, 74}, {0, 84}, {5, 74}}},
		{kind: "poly", pts: []Point{{0, 96}, {0, 96}}},
	},
}

// labelAt は名前と値を書く位置（部品の原点からの相対）。
var labelAt = map[string][2]Point{
	"res":  {{40, 28}, {40, 56}},
	"ind2": {{40, 28}, {40, 56}},
	"cap":  {{36, 4}, {36, 28}},
	// 電源は丸（中心 (0,64) 半径 32）に重ならないよう、右へ逃がす。
	// 旧版は丸の上に書いていて読みにくかった。
	"voltage": {{40, 52}, {40, 72}},
	"current": {{40, 52}, {40, 72}},
}

var defaultLabelAt = [2]Point{{40, 28}, {40, 56}}

// rotate は LTspice の向きの指定を座標の変換にする。
//
// R が回転、M が鏡像。数字は度。y が下向きなので、R90 は画面上では
// 時計回りに見える。
func rotate(p Point, rot string) Point {
	mirror := strings.HasPrefix(rot, "M")
	deg := strings.TrimLeft(rot, "RM")
	x, y := p.X, p.Y
	if mirror {
		x = -x
	}
	switch deg {
	case "90":
		x, y = -y, x
	case "180":
		x, y = -x, -y
	case "270":
		x, y = y, -x
	}
	return Point{x, y}
}

// SVGOptions は書き出しの設定。
type SVGOptions struct {
	Pad      int // 図の周りの余白（既定 16）
	FontSize int // 素子名の大きさ（既定 13）
	// HideTexts が真なら TEXT 行（ディレクティブや注記）を描かない。
	// 零値は「描く」。図だけ欲しいときに真にする。
	HideTexts bool
}

// SVG は回路図を SVG にする。
//
// 色は currentColor にしてあるので、置いた先の CSS の色をそのまま拾う
// （暗い背景でも明るい背景でも見える）。
func (s *Schematic) SVG(opt SVGOptions) string {
	if opt.Pad == 0 {
		opt.Pad = 16
	}
	if opt.FontSize == 0 {
		opt.FontSize = 13
	}

	var body strings.Builder
	bb := newBounds()

	// 線
	for _, w := range s.Wires {
		fmt.Fprintf(&body, "<path d=\"M%d %dL%d %d\"/>\n", w.A.X, w.A.Y, w.B.X, w.B.Y)
		bb.add(w.A)
		bb.add(w.B)
	}

	// 結線の合流点。3本以上が集まるところに丸を打つ。
	for _, p := range junctions(s.Wires) {
		fmt.Fprintf(&body, "<circle cx=\"%d\" cy=\"%d\" r=\"3.5\" fill=\"currentColor\"/>\n", p.X, p.Y)
	}

	// 部品
	for _, sym := range s.Symbols {
		g, ok := glyphs[sym.Type]
		if !ok {
			// 知らない種類。黙って省かずに四角と種類名で置いておく。
			fmt.Fprintf(&body, "<rect x=\"%d\" y=\"%d\" width=\"32\" height=\"64\"/>\n",
				sym.At.X, sym.At.Y)
			fmt.Fprintf(&body, "<text x=\"%d\" y=\"%d\" font-size=\"10\" fill=\"currentColor\" stroke=\"none\">%s</text>\n",
				sym.At.X+2, sym.At.Y+36, esc(sym.Type))
			bb.add(sym.At)
			bb.add(Point{sym.At.X + 32, sym.At.Y + 64})
			// 名前と値は下の共通の書き方に任せる（絵が無くても要る）。
		}
		for _, sh := range g { // g は知らない種類なら空
			abs := make([]Point, len(sh.pts))
			for i, p := range sh.pts {
				q := rotate(p, sym.Rot)
				abs[i] = Point{sym.At.X + q.X, sym.At.Y + q.Y}
				bb.add(abs[i])
			}
			switch sh.kind {
			case "poly":
				var d strings.Builder
				for i, p := range abs {
					if i == 0 {
						fmt.Fprintf(&d, "M%d %d", p.X, p.Y)
					} else {
						fmt.Fprintf(&d, "L%d %d", p.X, p.Y)
					}
				}
				fmt.Fprintf(&body, "<path d=\"%s\"/>\n", d.String())
			case "arc":
				fmt.Fprintf(&body, "<path d=\"M%d %dA%s %s 0 0 1 %d %d\"/>\n",
					abs[0].X, abs[0].Y, num(sh.r), num(sh.r), abs[1].X, abs[1].Y)
			case "circle":
				fmt.Fprintf(&body, "<circle cx=\"%d\" cy=\"%d\" r=\"%s\"/>\n",
					abs[0].X, abs[0].Y, num(sh.r))
				ri := int(sh.r + 0.5)
				bb.add(Point{abs[0].X - ri, abs[0].Y - ri})
				bb.add(Point{abs[0].X + ri, abs[0].Y + ri})
			case "dot":
				fmt.Fprintf(&body, "<circle cx=\"%d\" cy=\"%d\" r=\"%s\" fill=\"currentColor\"/>\n",
					abs[0].X, abs[0].Y, num(sh.r))
			}
		}

		// 名前と値
		la, ok := labelAt[sym.Type]
		if !ok {
			la = defaultLabelAt
		}
		if n := sym.Name(); n != "" {
			p := Point{sym.At.X + la[0].X, sym.At.Y + la[0].Y}
			fmt.Fprintf(&body, "<text x=\"%d\" y=\"%d\" font-size=\"%d\" fill=\"currentColor\" stroke=\"none\">%s</text>\n",
				p.X, p.Y, opt.FontSize, esc(n))
			bb.add(p)
			bb.add(Point{p.X + 8*len([]rune(n)), p.Y})
		}
		if v := sym.Value(); v != "" {
			p := Point{sym.At.X + la[1].X, sym.At.Y + la[1].Y}
			fmt.Fprintf(&body, "<text x=\"%d\" y=\"%d\" font-size=\"%d\" fill=\"currentColor\" stroke=\"none\">%s</text>\n",
				p.X, p.Y, opt.FontSize-1, esc(v))
			bb.add(p)
			bb.add(Point{p.X + 8*len([]rune(v)), p.Y})
		}
	}

	// 接地とノードのラベル
	for _, fl := range s.Flags {
		x, y := fl.At.X, fl.At.Y
		if fl.IsGround() {
			fmt.Fprintf(&body, "<path d=\"M%d %dL%d %d\"/>\n", x-12, y, x+12, y)
			fmt.Fprintf(&body, "<path d=\"M%d %dL%d %d\"/>\n", x-7, y+6, x+7, y+6)
			fmt.Fprintf(&body, "<path d=\"M%d %dL%d %d\"/>\n", x-2, y+12, x+2, y+12)
			bb.add(Point{x - 12, y})
			bb.add(Point{x + 12, y + 12})
		} else {
			// 名前つきのノード。印の小さな点と名前を書く。
			fmt.Fprintf(&body, "<circle cx=\"%d\" cy=\"%d\" r=\"2.5\" fill=\"currentColor\"/>\n", x, y)
			fmt.Fprintf(&body, "<text x=\"%d\" y=\"%d\" font-size=\"%d\" fill=\"currentColor\" stroke=\"none\">%s</text>\n",
				x+4, y-4, opt.FontSize-1, esc(fl.Name))
			bb.add(Point{x, y - 4})
			bb.add(Point{x + 8*len([]rune(fl.Name)), y})
		}
	}

	// 図面に書かれた文字
	if !opt.HideTexts {
		for _, t := range s.Texts {
			fmt.Fprintf(&body, "<text x=\"%d\" y=\"%d\" font-size=\"%d\" fill=\"currentColor\" stroke=\"none\">%s</text>\n",
				t.At.X, t.At.Y, opt.FontSize-1, esc(t.Body))
			bb.add(t.At)
			bb.add(Point{t.At.X + 7*len([]rune(t.Body)), t.At.Y + 6})
		}
	}

	if bb.empty {
		return `<svg xmlns="http://www.w3.org/2000/svg"></svg>`
	}
	x0, y0 := bb.minX-opt.Pad, bb.minY-opt.Pad
	w, h := bb.maxX-bb.minX+2*opt.Pad, bb.maxY-bb.minY+2*opt.Pad

	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="%d %d %d %d" `+
		`fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" `+
		`stroke-linejoin="round" font-family="Helvetica,Arial,sans-serif">`+"\n",
		x0, y0, w, h)
	out.WriteString(body.String())
	out.WriteString("</svg>")
	return out.String()
}

// junctions は結線が3本以上集まる点を返す。昇順で安定した並びにする。
func junctions(ws []Wire) []Point {
	n := map[Point]int{}
	for _, w := range ws {
		n[w.A]++
		n[w.B]++
	}
	var out []Point
	for p, c := range n {
		if c >= 3 {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		return out[i].Y < out[j].Y
	})
	return out
}

type bounds struct {
	minX, minY, maxX, maxY int
	empty                  bool
}

func newBounds() *bounds { return &bounds{empty: true} }

func (b *bounds) add(p Point) {
	if b.empty {
		b.minX, b.minY, b.maxX, b.maxY = p.X, p.Y, p.X, p.Y
		b.empty = false
		return
	}
	if p.X < b.minX {
		b.minX = p.X
	}
	if p.Y < b.minY {
		b.minY = p.Y
	}
	if p.X > b.maxX {
		b.maxX = p.X
	}
	if p.Y > b.maxY {
		b.maxY = p.Y
	}
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func decodeUTF16(us []uint16) string { return string(utf16.Decode(us)) }

// num は半径などの実数を、余計な小数点を付けずに書く。
func num(v float64) string {
	if v == float64(int(v)) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
