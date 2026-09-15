package mna

import (
	"math"
	"math/cmplx"
	"strings"

	"github.com/ichijohodaka/ltspice-go/pkg/netlist"
)

// DefaultValues はネットリストに書かれている値をそのまま Values にする。
//
// R, C, L は Value を、独立源は AC 振幅と位相からフェーザを作る。結合は
// K 行の係数をそのまま使う。素子値を振るときは、これを作ってから
// 変えたいものだけ書き換えるとよい。
func DefaultValues(nl *netlist.Netlist) Values {
	v := Values{
		Elem: make(map[string]complex128, len(nl.Elements)),
		K:    make(map[string]float64, len(nl.Couplings)),
	}
	for _, e := range nl.Elements {
		name := strings.ToLower(e.Name)
		switch e.Kind {
		case netlist.KindV, netlist.KindI:
			v.Elem[name] = cmplx.Rect(e.ACMag, e.ACPhase*math.Pi/180)
		default:
			v.Elem[name] = complex(e.Value, 0)
		}
	}
	for _, c := range nl.Couplings {
		v.K[strings.ToLower(c.Name)] = c.K
	}
	return v
}

// Power は素子が吸収する平均電力 ½·Re(V·conj(I))。
//
// 符号は吸収を正とする。電源は負、抵抗は正、結合していない理想 L・C は 0 で、
// 全素子の和は 0 になる（テレゲンの定理）。
func (r *Result) Power(e netlist.Element) float64 {
	i := r.BranchI[strings.ToLower(e.Name)]
	return 0.5 * real(r.V(e)*cmplx.Conj(i))
}
