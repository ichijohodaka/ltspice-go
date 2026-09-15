package main

import (
	"flag"
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ichijohodaka/ltspice-go/pkg/asc"
	"github.com/ichijohodaka/ltspice-go/pkg/mna"
	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
	"github.com/ichijohodaka/ltspice-go/pkg/raw"
	"github.com/ichijohodaka/ltspice-go/pkg/run"
)

func arg1(fs *flag.FlagSet, args []string, what string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 1 {
		return "", fmt.Errorf("%s を1つ指定してください", what)
	}
	return fs.Arg(0), nil
}

// --- show ---------------------------------------------------------------

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	path, err := arg1(fs, args, "ネットリスト")
	if err != nil {
		return err
	}
	nl, err := netlist.Load(path)
	if err != nil {
		return err
	}
	fmt.Printf("題名: %s\n", nl.Title)
	fmt.Printf("ファイル: %s\n", nl.Path)
	if nl.AscPath != "" {
		fmt.Printf("回路図: %s\n", nl.AscPath)
	}
	fmt.Printf("\n素子 %d 個\n", len(nl.Elements))
	for _, e := range nl.Elements {
		switch e.Kind {
		case netlist.KindV, netlist.KindI:
			fmt.Printf("  %-6s %-4s %-6s %-6s AC %g∠%g°\n",
				e.Name, e.Kind, e.Nodes[0], e.Nodes[1], e.ACMag, e.ACPhase)
		default:
			fmt.Printf("  %-6s %-4s %-6s %-6s %g\n",
				e.Name, e.Kind, e.Nodes[0], e.Nodes[1], e.Value)
		}
	}
	if len(nl.Couplings) > 0 {
		fmt.Printf("\n結合 %d 個\n", len(nl.Couplings))
		for _, c := range nl.Couplings {
			fmt.Printf("  %-6s %-20s k = %g\n", c.Name, strings.Join(c.Inductors, ", "), c.K)
		}
	}
	fmt.Printf("\nノード %d 個: %s\n", len(nl.Nodes), strings.Join(nl.Nodes, ", "))
	if len(nl.Directives) > 0 {
		fmt.Printf("\nディレクティブ\n")
		for _, d := range nl.Directives {
			fmt.Printf("  %s\n", d)
		}
	}
	if start, stop, ok := netlist.ACRange(nl); ok {
		fmt.Printf("\nAC の掃引範囲: %g 〜 %g Hz\n", start, stop)
	}
	for _, w := range nl.Warnings {
		fmt.Fprintf(os.Stderr, "注意: %s\n", w)
	}
	return nil
}

// --- raw ----------------------------------------------------------------

func cmdRaw(args []string) error {
	fs := flag.NewFlagSet("raw", flag.ExitOnError)
	point := fs.Int("point", 0, "表示する点の番号")
	list := fs.Bool("vars", false, "変数名だけを並べる")
	path, err := arg1(fs, args, ".raw")
	if err != nil {
		return err
	}
	d, err := raw.Read(path)
	if err != nil {
		return err
	}
	fmt.Printf("題名: %s\n", d.Title)
	fmt.Printf("Flags: %s\n", d.Flags)
	fmt.Printf("変数 %d 個, 点 %d 個 (%g 〜 %g Hz)\n",
		len(d.Vars), len(d.Vals), d.Freqs[0], d.Freqs[len(d.Freqs)-1])
	if *list {
		for i, v := range d.Vars {
			fmt.Printf("  %2d %s\n", i, v)
		}
		return nil
	}
	if *point < 0 || *point >= len(d.Vals) {
		return fmt.Errorf("点の番号は 0 〜 %d", len(d.Vals)-1)
	}
	fmt.Printf("\n点 %d (%g Hz)\n", *point, d.Freqs[*point])
	for i, name := range d.Vars {
		v := d.Vals[*point][i]
		fmt.Printf("  %-16s %s\n", name, cstr(v))
	}
	return nil
}

// --- solve --------------------------------------------------------------

func cmdSolve(args []string) error {
	fs := flag.NewFlagSet("solve", flag.ExitOnError)
	freq := fs.String("freq", "", "周波数（既定はネットリストの .ac から選ぶ）")
	path, err := arg1(fs, args, "ネットリスト")
	if err != nil {
		return err
	}
	nl, err := netlist.Load(path)
	if err != nil {
		return err
	}
	freqs := run.ParseFreqList(*freq)
	if len(freqs) == 0 {
		freqs = run.DefaultFreqs(nl)
	}
	vals := mna.DefaultValues(nl)
	for _, f := range freqs {
		res, err := mna.Solve(nl, vals, 2*math.Pi*f)
		if err != nil {
			return err
		}
		fmt.Printf("=== %g Hz ===\n", f)
		nodes := append([]string(nil), nl.Nodes...)
		sort.Strings(nodes)
		fmt.Printf("ノード電圧\n")
		for _, n := range nodes {
			if netlist.IsGround(n) {
				continue
			}
			fmt.Printf("  V(%-6s) = %s\n", n, cstr(res.NodeV[n]))
		}
		fmt.Printf("素子（V は第1ノード−第2ノード、I は第1→第2、P は吸収）\n")
		total := 0.0
		for _, e := range nl.Elements {
			p := res.Power(e)
			total += p
			fmt.Printf("  %-6s V = %-28s I = %-28s P = %12.6g W\n",
				e.Name, cstr(res.V(e)), cstr(res.BranchI[strings.ToLower(e.Name)]), p)
		}
		fmt.Printf("  %-6s %62s P = %12.6g W（テレゲンの定理により 0）\n", "合計", "", total)
		fmt.Println()
	}
	return nil
}

// --- run ----------------------------------------------------------------

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	exePath := fs.String("ltspice", "", "LTspice の実行ファイル")
	freq := fs.String("freq", "", "周波数（既定はネットリストの .ac から選ぶ）")
	keep := fs.String("dir", "", "作業ディレクトリ（既定は一時ディレクトリ）")
	path, err := arg1(fs, args, "ネットリスト")
	if err != nil {
		return err
	}
	nl, err := netlist.Load(path)
	if err != nil {
		return err
	}
	exe, err := run.Find(*exePath)
	if err != nil {
		return err
	}
	fmt.Printf("LTspice: %s\n", exe)

	freqs := run.ParseFreqList(*freq)
	if len(freqs) == 0 {
		freqs = run.DefaultFreqs(nl)
	}

	// 元のネットリストから .ac 行だけを差し替える。
	var b strings.Builder
	src, err := os.ReadFile(nl.Path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), ".ac ") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	full := strings.Replace(b.String(), ".end", run.ACList(freqs)+"\n.end", 1)

	dir := *keep
	if dir == "" {
		dir, err = os.MkdirTemp("", "ltspice-go")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	d, err := run.Batch(exe, filepath.Join(dir, "run.net"), full, 0)
	if err != nil {
		return err
	}
	fmt.Printf("変数 %d 個, 点 %d 個\n\n", len(d.Vars), len(d.Vals))
	for pt := range d.Vals {
		fmt.Printf("=== %g Hz ===\n", d.Freqs[pt])
		for i, name := range d.Vars {
			if i == 0 {
				continue
			}
			fmt.Printf("  %-16s %s\n", name, cstr(d.Vals[pt][i]))
		}
		fmt.Println()
	}
	return nil
}

func cstr(c complex128) string {
	return fmt.Sprintf("%.6g%+.6gj (|%.4g| ∠%.1f°)",
		real(c), imag(c), cmplx.Abs(c), cmplx.Phase(c)*180/math.Pi)
}

// --- svg ----------------------------------------------------------------

func cmdSVG(args []string) error {
	fs := flag.NewFlagSet("svg", flag.ExitOnError)
	out := fs.String("o", "", "書き出し先（省略すると標準出力）")
	noText := fs.Bool("no-text", false, "図面の注記・ディレクティブを描かない")
	pad := fs.Int("pad", 16, "図の周りの余白")
	path, err := arg1(fs, args, "回路図（.asc）")
	if err != nil {
		return err
	}
	s, err := asc.Load(path)
	if err != nil {
		return err
	}
	svg := s.SVG(asc.SVGOptions{Pad: *pad, HideTexts: *noText})
	if *out == "" {
		fmt.Println(svg)
	} else if err := os.WriteFile(*out, []byte(svg), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "結線 %d 本、部品 %d 個、ラベル %d 個\n",
		len(s.Wires), len(s.Symbols), len(s.Flags))
	for _, u := range s.Unknown {
		fmt.Fprintf(os.Stderr, "注意: %s の絵を持っていないので四角で代えました\n", u)
	}
	return nil
}

// --- net ----------------------------------------------------------------

func cmdNet(args []string) error {
	fs := flag.NewFlagSet("net", flag.ExitOnError)
	out := fs.String("o", "", "書き出し先（省略すると標準出力）")
	path, err := arg1(fs, args, "回路図（.asc）")
	if err != nil {
		return err
	}
	s, err := asc.Load(path)
	if err != nil {
		return err
	}
	text, err := s.NetlistText()
	if err != nil {
		return err
	}
	if *out == "" {
		fmt.Print(text)
	} else if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "素子 %d 個、結線 %d 本\n", len(s.Symbols), len(s.Wires))
	return nil
}
