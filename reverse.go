package fxsettle

import (
	"fmt"
	"math/big"
)

// RevKind 反向步骤的类型。
type RevKind string

const (
	RevInvert  RevKind = "invert"  // 乘以汇率倒数，精确还原
	RevCorrect RevKind = "correct" // 加回被舍去的部分（反向更正）
)

// RevNode 反向轨迹中的一步，全部保持精确值，可复核。
type RevNode struct {
	Kind   RevKind
	Label  string
	In     *big.Rat // 进入本步的值（从最终值倒推）
	Factor *big.Rat // invert 步为 1/汇率；correct 步为 1
	Adjust *big.Rat // correct 步加回的金额 = −(原舍入损益)；invert 步为 0
	Out    *big.Rat // In × Factor + Adjust
}

// ReverseTrace 反向更正的完整轨迹。
type ReverseTrace struct {
	EntryID   string
	FromCur   string // 反向起点币种（原轨迹的目标币种）
	ToCur     string // 反向终点币种（原轨迹的源币种）
	Start     *big.Rat
	Nodes     []RevNode
	Recovered *big.Rat // 还原出的原始金额（精确）
}

// Reverse 沿换算轨迹反向更正：
//   - 换算步：乘以汇率的倒数，精确还原；
//   - 舍入节点：生成显式更正步，把当时舍去的部分（−RoundPL）加回。
//
// 因为原轨迹保留了每个舍入节点的精确损益，反向可以无信息损失地
// 还原出原始金额；Recovered 一定等于原 Trace.Start。
func Reverse(tr *Trace) *ReverseTrace {
	rt := &ReverseTrace{
		EntryID: tr.EntryID,
		FromCur: tr.ToCur,
		ToCur:   tr.FromCur,
		Start:   new(big.Rat).Set(tr.Final),
	}
	val := new(big.Rat).Set(tr.Final)
	for i := len(tr.Nodes) - 1; i >= 0; i-- {
		n := tr.Nodes[i]
		if n.IsRoundingNode() {
			adjust := new(big.Rat).Neg(n.RoundPL)
			out := new(big.Rat).Add(val, adjust)
			rt.Nodes = append(rt.Nodes, RevNode{
				Kind:   RevCorrect,
				Label:  fmt.Sprintf("反向更正 步骤%d %s: 加回舍入损益 %s", i, n.Label, Frac(adjust)),
				In:     val,
				Factor: Rat(1, 1),
				Adjust: adjust,
				Out:    out,
			})
			val = out
		}
		inv := new(big.Rat).Inv(n.Rate)
		out := new(big.Rat).Mul(val, inv)
		rt.Nodes = append(rt.Nodes, RevNode{
			Kind:   RevInvert,
			Label:  fmt.Sprintf("反向换算 步骤%d %s: ×%s（=1/(%s)）", i, n.Label, Frac(inv), Frac(n.Rate)),
			In:     val,
			Factor: inv,
			Adjust: new(big.Rat),
			Out:    out,
		})
		val = out
	}
	rt.Recovered = val
	return rt
}

// CorrectionEntries 把反向轨迹落成可入账的更正条目序列：
// 每个 RevCorrect 步对应一条更正分录（金额为加回值），
// 来源链指回原条目，便于审计勾稽。
func (rt *ReverseTrace) CorrectionEntries(idPrefix string) []*Entry {
	var out []*Entry
	k := 0
	for _, n := range rt.Nodes {
		if n.Kind != RevCorrect || n.Adjust.Sign() == 0 {
			continue
		}
		k++
		e := &Entry{
			ID:  fmt.Sprintf("%s-%d", idPrefix, k),
			Cur: rt.FromCur,
			Val: new(big.Rat).Set(n.Adjust),
		}
		e.record(OpCorrect, n.Label, []string{rt.EntryID}, n.Adjust)
		out = append(out, e)
	}
	return out
}
