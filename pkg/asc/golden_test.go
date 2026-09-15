package asc

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGeometryMatchesGolden は、書き出した回路図の幾何が、以前 .asc から
// 自動生成されていた図と**座標まで一致する**ことを確かめる。
//
// # なぜ golden を置いてあるか
//
// 元の生成コードはどのリポジトリにも残っていない（wpt-symbolic-solution の
// 全履歴を見ても無い）。残っていたのは出来上がった index.html だけで、
// そこから幾何を抜き出したものが testdata/golden にある。
//
// つまりこれは「作り直したものが、失われた道具と同じ図を描くか」の
// 答え合わせである。出どころの wpt-2dplot-main はこの後アーカイブされる
// ので、比べるものをこちらに持ってきてある。
//
// 文字の位置は入れていない。LTspice の .asy が持つ既定の表示位置に
// 依存していて、そこまで真似る値打ちがないため。
func TestGeometryMatchesGolden(t *testing.T) {
	for _, name := range []string{"20260906wptSSSP2", "20260913wpt1to3"} {
		t.Run(name, func(t *testing.T) {
			s := load(t, name+".asc")
			got := geometryOf(s.SVG(SVGOptions{}))

			b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", name+".geom"))
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, l := range strings.Split(string(b), "\n") {
				l = strings.TrimSpace(l)
				if l != "" && !strings.HasPrefix(l, "#") {
					want = append(want, l)
				}
			}
			sort.Strings(want)

			if len(got) != len(want) {
				t.Errorf("図形の数が %d、golden は %d", len(got), len(want))
			}
			gs, ws := map[string]bool{}, map[string]bool{}
			for _, g := range got {
				gs[g] = true
			}
			for _, w := range want {
				ws[w] = true
			}
			miss, extra := 0, 0
			for _, w := range want {
				if !gs[w] {
					if miss < 5 {
						t.Errorf("golden にあって出ていない: %s", w)
					}
					miss++
				}
			}
			for _, g := range got {
				if !ws[g] {
					if extra < 5 {
						t.Errorf("golden に無いものが出ている: %s", g)
					}
					extra++
				}
			}
			if miss == 0 && extra == 0 {
				t.Logf("図形 %d 個が座標まで一致", len(got))
			}
		})
	}
}

var reGeom = regexp.MustCompile(`M-?\d+ -?\d+L-?\d+ -?\d+|<circle[^/]*/>`)

// geometryOf は SVG から線と丸だけを取り出して並べる。
func geometryOf(svg string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range reGeom.FindAllString(svg, -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// TestGoldenWouldCatchAChange は、この突き合わせが検算として働いているかを
// 確かめる。抵抗の絵を1目盛りずらして、必ず気づくことを見る。
func TestGoldenWouldCatchAChange(t *testing.T) {
	s := load(t, "20260906wptSSSP2.asc")
	before := geometryOf(s.SVG(SVGOptions{}))

	orig := glyphs["res"]
	defer func() { glyphs["res"] = orig }()
	moved := []shape{{kind: "poly", pts: append([]Point(nil), orig[0].pts...)}}
	moved[0].pts[0] = Point{moved[0].pts[0].X + 1, moved[0].pts[0].Y}
	glyphs["res"] = moved

	after := geometryOf(s.SVG(SVGOptions{}))
	if len(before) == len(after) && equalStrings(before, after) {
		t.Fatal("絵を動かしたのに幾何が変わらない。突き合わせが効いていない")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
