# ltspice-go

LTspice のファイルを Go から読み書きし、バッチ実行し、回路を数値で解くための
パッケージ群。**標準ライブラリ以外に依存しない。**

```
go get github.com/ichijohodaka/ltspice-go
```

| パッケージ | 中身 |
|---|---|
| [`pkg/netlist`](pkg/netlist) | ネットリスト（`.net` / `.asc`）を読む |
| [`pkg/raw`](pkg/raw) | `.raw`（AC 解析）を読む。ASCII・Binary・UTF-16 のどれでも |
| [`pkg/run`](pkg/run) | LTspice をバッチ実行して `.raw` を読む |
| [`pkg/mna`](pkg/mna) | 修正節点解析で回路を数値的に解く |

扱える素子は `R`, `C`, `L`, 独立電圧源 `V`, 独立電流源 `I`, 結合 `K` で、
解析は AC（交流定常）のみ。**それ以外に出会ったら、黙って無視せずエラーにする。**

## 試す

```bash
go run ./cmd/ltspice-go show  testdata/20260906wptSSSP2.net
go run ./cmd/ltspice-go solve testdata/20260906wptSSSP2.net -freq 223.6k
go run ./cmd/ltspice-go raw   testdata/20260913wpt1to3.raw -vars
go run ./cmd/ltspice-go run   testdata/20260906wptSSSP2.net -freq 223.6k
```

`solve` の出力（一部）。`P` は吸収した平均電力で、電源は負・抵抗は正、
全素子の和は 0 になる。

```
=== 223600 Hz ===
素子（V は第1ノード−第2ノード、I は第1→第2、P は吸収）
  VS     V = 5+0j             I = -0.00917588-0.111596j   P =   -0.0229397 W
  RS     V = -0.0256925-...j  I = -0.00917588-0.111596j   P =     0.017553 W
  ...
  合計                                                    P =  2.56778e-18 W
```

## 読む

```go
nl, err := netlist.Load("circuit.net")   // .asc を渡すと対応する .net を探す
res, err := mna.Solve(nl, mna.DefaultValues(nl), 2*math.Pi*223.6e3)

res.NodeV["y1"]              // ノード電圧
res.BranchI["rl1"]           // 素子電流（第1ノード → 第2ノード）
res.V(nl.Elements[0])        // 素子の端子間電圧
res.Power(nl.Elements[0])    // 吸収した平均電力 ½·Re(V·conj(I))
```

素子値を振るときは `DefaultValues` を作ってから、変えたいものだけ
書き換える。

```go
v := mna.DefaultValues(nl)
v.Elem["l1"] = complex(105e-6, 0)   // 素子名は小文字
v.K["k12"] = 0.12
```

LTspice を実際に走らせるなら:

```go
exe, err := run.Find("")             // 標準的な場所と PATH を探す
src := "* RC\nV1 n1 0 AC 1\nR1 n1 n2 1k\nC1 n2 0 1n\n" +
    run.ACList([]float64{100e3}) + "\n.end\n"
d, err := run.Batch(exe, filepath.Join(dir, "t.net"), src, 0)
v, ok := d.NodeVoltage(0, "n2")
```

## 符号の約束

| | 向き |
|---|---|
| `V(X)` | 第1ノード − 第2ノード |
| `I(X)` | 第1ノード → 第2ノード（独立源も同じ） |
| `P(X)` | `½·Re(V·conj(I))`。**吸収**を正とする |

電源は負、抵抗は正、結合していない理想 L・C は 0 で、全素子の和は 0
（テレゲンの定理）。**LTspice の `I(...)` もこれと同じ向き**であることは、
`.raw` と突き合わせて確かめてある（`pkg/mna` の `TestVsLTspice`）。

## 気をつけること

### `.raw` は 4 通りの顔を持つ

「ascii」と銘打っても中身は版によって違うので、`pkg/raw` が全部吸収する。

| | LTspice XVII | LTspice 26 (ADI) |
|---|---|---|
| 頭の文字コード | UTF-16LE（BOM なし） | ASCII |
| `-ascii` あり | `Values:`（テキスト） | `Values:` |
| `-ascii` なし | `Binary:`（float64 の並び） | `Binary:` |

見分けそこねると「データがありません」になる。**`Binary:` は AC 解析のもの
だけ読める**（過渡解析は1変数あたりのバイト数が違うので、読めば必ず化ける。
黙って読まずエラーにする）。

### µ（U+00B5）を UTF-8 で書くと LTspice が読まない

2バイトになるため。`100µ` が 100 H と解釈されてコイルが開放になる。
自分でネットリストを組み立てるときは `u` を使うこと。読むほうは
`pkg/netlist` が µ も `u` も受ける。

### 恒等的に 0 の量を相対誤差で比べない

理想コンデンサの吸収電力は 0 なので、1e-19 と 1e-21 を比べて「相対差 1」に
なる。**同じ種類の量のうち最大のもの**を尺度にすること。

## 検算

- **LTspice と突き合わせる** — `testdata` の `.raw`（送電1・受電3、700 点）の
  全ノード電圧・全素子電流と、`pkg/mna` の解を 7 点で照合（相対 1e-6）
- **テレゲンの定理** — 全素子の吸収平均電力の総和が 0
- **手で解ける回路** — RC 分圧、直列 RLC の共振、結合コイルの開放電圧
- **壊した入力を弾けるか** — 素子値を 0.1% ずらして、突き合わせが気づくことを確認

```bash
go test ./...
```

LTspice が入っていれば `pkg/run` が実際に走らせる。入っていなければ
その1つだけ飛ばす。

## 出どころ

`wpt-symbolic-ac-analysis` の `internal/` から、回路の種類に依らない部分を
切り出したもの。切り出しの経緯は
[20260915-plan.md](https://github.com/ichijohodaka/wpt-symbolic-ac-analysis-20260914)
（非公開）にある。

## ライセンス

MIT
