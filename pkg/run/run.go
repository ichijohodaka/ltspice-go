// Package run は LTspice をバッチモード（-b -ascii）で走らせ、書き出された
// .raw を読む。
//
// LTspice は GUI のアプリケーションだが、-b を付けるとウィンドウを出さずに
// シミュレーションだけを行う。-ascii を併せて指定すると .raw がテキストで
// 書かれるので、[raw] で読める。
//
// 実行ファイルの場所は環境によって違う。Find が標準的な場所を順に探す。
//
// [raw]: github.com/ichijohodaka/ltspice-go/pkg/raw
package run

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
	"github.com/ichijohodaka/ltspice-go/pkg/raw"
)

// DefaultTimeout は Batch の時間切れの目安。
const DefaultTimeout = 2 * time.Minute

// Batch はネットリストを netPath に書き出してバッチ実行し、.raw を読む。
//
// netPath と同じ名前の .raw が作られる。作業用のディレクトリを呼び出し側で
// 用意して、その中のパスを渡すとよい。
func Batch(exe, netPath, src string, timeout time.Duration) (*raw.Data, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if err := os.WriteFile(netPath, []byte(src), 0o644); err != nil {
		return nil, err
	}
	rawPath := strings.TrimSuffix(netPath, filepath.Ext(netPath)) + ".raw"
	os.Remove(rawPath)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-b", "-ascii", netPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("LTspice の実行に失敗: %w\n%s", err, string(out))
	}
	if _, statErr := os.Stat(rawPath); statErr != nil {
		logPath := strings.TrimSuffix(netPath, filepath.Ext(netPath)) + ".log"
		logTxt, _ := os.ReadFile(logPath)
		return nil, fmt.Errorf("LTspice が .raw を出力しませんでした (%s)\n--- log ---\n%s",
			filepath.Base(netPath), string(logTxt))
	}
	return raw.Read(rawPath)
}

// EnvVars は実行ファイルの場所を指定できる環境変数。先に挙げたものが優先。
var EnvVars = []string{"LTSPICE_EXE", "WPTIMP_LTSPICE"}

// Find は LTspice の実行ファイルを探す。
//
// 探す順は hint → 環境変数（EnvVars）→ 標準的なインストール先 → PATH。
func Find(hint string) (string, error) {
	cands := []string{}
	if hint != "" {
		cands = append(cands, hint)
	}
	for _, ev := range EnvVars {
		if v := os.Getenv(ev); v != "" {
			cands = append(cands, v)
		}
	}
	home, _ := os.UserHomeDir()
	cands = append(cands,
		filepath.Join(home, "AppData", "Local", "Programs", "ADI", "LTspice", "LTspice.exe"),
		`C:\Program Files\ADI\LTspice\LTspice.exe`,
		`C:\Program Files\LTC\LTspiceXVII\XVIIx64.exe`,
		`C:\Program Files (x86)\LTC\LTspiceIV\scad3.exe`,
	)
	for _, c := range cands {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	for _, n := range []string{"LTspice.exe", "XVIIx64.exe", "ltspice"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("LTspice の実行ファイルが見つかりません。"+
		"引数で指定するか、環境変数 %s を設定してください", strings.Join(EnvVars, " か "))
}

// ParseFreqList は "100k,200k" 形式を解釈する。読めない要素は黙って捨てる。
func ParseFreqList(s string) []float64 {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []float64
	for _, part := range strings.Split(s, ",") {
		if v, err := netlist.ParseValue(strings.TrimSpace(part)); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// ACList は指定した周波数だけを走らせる .ac list 行を組み立てる。
//
// 有効数字を 17 桁で書くのは、読み書きで値が変わらないようにするため。
func ACList(freqs []float64) string {
	var b strings.Builder
	b.WriteString(".ac list")
	for _, f := range freqs {
		fmt.Fprintf(&b, " %s", strconv.FormatFloat(f, 'g', 17, 64))
	}
	return b.String()
}

// DefaultFreqs はネットリストの .ac 行から、確かめに使う周波数を選ぶ。
//
//   - .ac list なら、そこに並んでいる周波数をそのまま使う
//   - .ac dec/oct/lin なら、始点と終点の間を対数等間隔に 5 点
//   - .ac 行がなければ 100k, 200k, 500k
func DefaultFreqs(nl *netlist.Netlist) []float64 {
	fstart, fstop := 0.0, 0.0
	for _, d := range nl.Directives {
		f := strings.Fields(d)
		if len(f) == 0 || !strings.EqualFold(f[0], ".ac") {
			continue
		}
		if len(f) >= 3 && strings.EqualFold(f[1], "list") {
			var out []float64
			for _, t := range f[2:] {
				if v, err := netlist.ParseValue(t); err == nil {
					out = append(out, v)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		if len(f) >= 5 {
			a, err1 := netlist.ParseValue(f[3])
			b, err2 := netlist.ParseValue(f[4])
			if err1 == nil && err2 == nil && a > 0 && b > a {
				fstart, fstop = a, b
			}
		}
	}
	if fstart == 0 {
		return []float64{100e3, 200e3, 500e3}
	}
	n := 5
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n-1)
		out[i] = fstart * math.Pow(fstop/fstart, t)
	}
	return out
}
