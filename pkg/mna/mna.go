// Package mna は回路全体を数値的に解く、独立実装の修正節点解析である。
//
// シンボリックな解（テブナン等価への分割 → N×N の結合系 → 後退代入）とは
// 別の道筋で、結合インダクタンスまで含めた1つの大きな行列を直接解く。
// 分割が正しいかどうかを乱数パラメータで突き合わせるための参照実装であり、
// 速さは求めていない。
package mna

import (
	"fmt"
	"math"
	"math/cmplx"
	"strings"

	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
)

// Values は素子値。Elem は素子名（小文字）→ 値で、R[Ω], C[F], L[H] は実数、
// 独立源は AC フェーザ。K は結合行の名前（小文字）→ 結合係数。
type Values struct {
	Elem map[string]complex128
	K    map[string]float64
}

// Result は解。
type Result struct {
	NodeV   map[string]complex128 // ノード電圧（接地は 0）
	BranchI map[string]complex128 // 素子電流（第1ノード→第2ノードの向き）
}

// V は素子の端子間電圧 V(+) − V(−)。
func (r *Result) V(e netlist.Element) complex128 {
	return r.node(e.Nodes[0]) - r.node(e.Nodes[1])
}

func (r *Result) node(n string) complex128 {
	if netlist.IsGround(n) {
		return 0
	}
	return r.NodeV[n]
}

// Solve は角周波数 omega における回路全体の解を返す。
func Solve(nl *netlist.Netlist, v Values, omega float64) (*Result, error) {
	s := complex(0, omega)

	// 未知数: 非接地ノードの電圧、そのあと枝電流（電流源以外の全素子）。
	var nodes []string
	nodeIdx := map[string]int{}
	for _, e := range nl.Elements {
		for _, n := range e.Nodes {
			if netlist.IsGround(n) {
				continue
			}
			if _, ok := nodeIdx[n]; !ok {
				nodeIdx[n] = len(nodes)
				nodes = append(nodes, n)
			}
		}
	}
	nn := len(nodes)

	var branches []netlist.Element
	branchIdx := map[string]int{}
	for _, e := range nl.Elements {
		if e.Kind == netlist.KindI {
			continue
		}
		branchIdx[strings.ToLower(e.Name)] = len(branches)
		branches = append(branches, e)
	}
	nb := len(branches)
	n := nn + nb

	a := make([]complex128, n*n)
	b := make([]complex128, n)
	at := func(i, j int) *complex128 { return &a[i*n+j] }

	val := func(e netlist.Element) (complex128, error) {
		x, ok := v.Elem[strings.ToLower(e.Name)]
		if !ok {
			return 0, fmt.Errorf("素子 %s の値がありません", e.Name)
		}
		return x, nil
	}

	// KCL: 各ノードから出ていく枝電流の和 ＝ 注入電流
	for bi, e := range branches {
		col := nn + bi
		if i, ok := nodeIdx[e.Nodes[0]]; ok {
			*at(i, col) += 1
		}
		if i, ok := nodeIdx[e.Nodes[1]]; ok {
			*at(i, col) -= 1
		}
	}
	for _, e := range nl.Elements {
		if e.Kind != netlist.KindI {
			continue
		}
		x, err := val(e)
		if err != nil {
			return nil, err
		}
		// SPICE: 電流は第1ノードから素子内部を通って第2ノードへ流れる
		if i, ok := nodeIdx[e.Nodes[0]]; ok {
			b[i] -= x
		}
		if i, ok := nodeIdx[e.Nodes[1]]; ok {
			b[i] += x
		}
	}

	// 結合インダクタンス M_kj = k_kj·√(L_k·L_j)
	mutual := map[string]map[string]complex128{}
	for _, c := range nl.Couplings {
		k, ok := v.K[strings.ToLower(c.Name)]
		if !ok {
			return nil, fmt.Errorf("結合 %s の値がありません", c.Name)
		}
		for x := 0; x < len(c.Inductors); x++ {
			for y := x + 1; y < len(c.Inductors); y++ {
				ea, eb := nl.FindElement(c.Inductors[x]), nl.FindElement(c.Inductors[y])
				if ea == nil || eb == nil {
					return nil, fmt.Errorf("結合 %s のインダクタが見つかりません", c.Name)
				}
				la, err := val(*ea)
				if err != nil {
					return nil, err
				}
				lb, err := val(*eb)
				if err != nil {
					return nil, err
				}
				m := complex(k*math.Sqrt(real(la)*real(lb)), 0)
				na, nb2 := strings.ToLower(ea.Name), strings.ToLower(eb.Name)
				if mutual[na] == nil {
					mutual[na] = map[string]complex128{}
				}
				if mutual[nb2] == nil {
					mutual[nb2] = map[string]complex128{}
				}
				mutual[na][nb2] += m
				mutual[nb2][na] += m
			}
		}
	}

	// 素子方程式
	for bi, e := range branches {
		row := nn + bi
		col := nn + bi
		x, err := val(e)
		if err != nil {
			return nil, err
		}
		setNode := func(node string, c complex128) {
			if i, ok := nodeIdx[node]; ok {
				*at(row, i) += c
			}
		}
		switch e.Kind {
		case netlist.KindR:
			setNode(e.Nodes[0], 1)
			setNode(e.Nodes[1], -1)
			*at(row, col) = -x
		case netlist.KindC:
			setNode(e.Nodes[0], s*x)
			setNode(e.Nodes[1], -s*x)
			*at(row, col) = -1
		case netlist.KindL:
			setNode(e.Nodes[0], 1)
			setNode(e.Nodes[1], -1)
			*at(row, col) = -s * x
			for other, m := range mutual[strings.ToLower(e.Name)] {
				oi, ok := branchIdx[other]
				if !ok {
					return nil, fmt.Errorf("結合相手 %s が枝にありません", other)
				}
				*at(row, nn+oi) -= s * m
			}
		case netlist.KindV:
			setNode(e.Nodes[0], 1)
			setNode(e.Nodes[1], -1)
			b[row] = x
		default:
			return nil, fmt.Errorf("未対応の素子 %s", e.Name)
		}
	}

	x, err := solveDense(a, b, n)
	if err != nil {
		return nil, err
	}

	res := &Result{NodeV: map[string]complex128{}, BranchI: map[string]complex128{}}
	for i, name := range nodes {
		res.NodeV[name] = x[i]
	}
	for bi, e := range branches {
		res.BranchI[strings.ToLower(e.Name)] = x[nn+bi]
	}
	for _, e := range nl.Elements {
		if e.Kind == netlist.KindI {
			res.BranchI[strings.ToLower(e.Name)] = v.Elem[strings.ToLower(e.Name)]
		}
	}
	return res, nil
}

// solveDense は部分ピボット付きガウス消去。
func solveDense(a []complex128, b []complex128, n int) ([]complex128, error) {
	m := make([]complex128, len(a))
	copy(m, a)
	x := make([]complex128, n)
	copy(x, b)
	for k := 0; k < n; k++ {
		p, best := k, cmplx.Abs(m[k*n+k])
		for i := k + 1; i < n; i++ {
			if v := cmplx.Abs(m[i*n+k]); v > best {
				p, best = i, v
			}
		}
		if best == 0 {
			return nil, fmt.Errorf("numeric: 係数行列が特異（段 %d）", k)
		}
		if p != k {
			for j := 0; j < n; j++ {
				m[k*n+j], m[p*n+j] = m[p*n+j], m[k*n+j]
			}
			x[k], x[p] = x[p], x[k]
		}
		inv := 1 / m[k*n+k]
		for i := k + 1; i < n; i++ {
			f := m[i*n+k] * inv
			if f == 0 {
				continue
			}
			for j := k; j < n; j++ {
				m[i*n+j] -= f * m[k*n+j]
			}
			x[i] -= f * x[k]
		}
	}
	for i := n - 1; i >= 0; i-- {
		sum := x[i]
		for j := i + 1; j < n; j++ {
			sum -= m[i*n+j] * x[j]
		}
		x[i] = sum / m[i*n+i]
	}
	return x, nil
}
