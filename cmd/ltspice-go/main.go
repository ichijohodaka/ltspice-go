// コマンド ltspice-go は、このリポジトリのパッケージをそのまま試すための道具。
//
//	ltspice-go show  <netlist>   ネットリストを読んで中身を表示する
//	ltspice-go raw   <raw>       .raw を読んで表示する
//	ltspice-go solve <netlist>   数値の修正節点解析で解く
//	ltspice-go run   <netlist>   LTspice を走らせて .raw を読む
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "show":
		err = cmdShow(os.Args[2:])
	case "raw":
		err = cmdRaw(os.Args[2:])
	case "solve":
		err = cmdSolve(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "知らない下位コマンド %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "エラー:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `使いかた: ltspice-go <下位コマンド> [オプション] <ファイル>

  show   <netlist>  ネットリスト（.net / .asc）を読んで、素子・結合・ノードを表示する
  raw    <raw>      .raw を読んで、変数と値を表示する
  solve  <netlist>  数値の修正節点解析で解き、各素子の V・I・P を表示する
  run    <netlist>  LTspice をバッチ実行して .raw を読み、結果を表示する

各下位コマンドの -h でオプションを見られる。
`)
}
