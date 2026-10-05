package fxsettle

import (
	"fmt"
	"math/big"
)

// Step 换汇流水线的一步：乘以精确汇率；仅当 Round 为 true 时
// 才在本步之后舍入到 Scale 位小数（舍入节点）。
type Step struct {
	Label string
	Rate  *big.Rat
	Round bool
	Scale int
	Mode  RoundMode
}

// FX 构造不舍入的换算步：全程保持精确分数。
func FX(label string, rate *big.Rat) Step {
	return Step{Label: label, Rate: new(big.Rat).Set(rate)}
}

// FXRound 构造舍入节点：先精确乘汇率，再按 mode 舍入到 scale 位小数。
func FXRound(label string, rate *big.Rat, scale int, mode RoundMode) Step {
	return Step{Label: label, Rate: new(big.Rat).Set(rate), Round: true, Scale: scale, Mode: mode}
}

// Node 流水线中一步的执行记录，可复核。
type Node struct {
	Index   int
	Label   string
	In      *big.Rat // 进入本步的精确值
	Rate    *big.Rat // 本步汇率
	Exact   *big.Rat // In × Rate，舍入前的精确中间值
	Rounded *big.Rat // 舍入后的值；非舍入节点为 nil
	RoundPL *big.Rat // 舍入损益 = Rounded − Exact；非舍入节点为 nil
	Out     *big.Rat // 离开本步的值（舍入节点为 Rounded，否则为 Exact）
}

// IsRoundingNode 报告本节点是否发生舍入。
func (n Node) IsRoundingNode() bool { return n.Rounded != nil }

// Trace 一条账目经过整条流水线的完整换算轨迹。
type Trace struct {
	EntryID string
	FromCur string
	ToCur   string
	Start   *big.Rat
	Nodes   []Node
	Final   *big.Rat
}

// RoundPLTotal 整条轨迹的累计舍入损益（精确值）。
func (t *Trace) RoundPLTotal() *big.Rat {
	total := new(big.Rat)
	for _, n := range t.Nodes {
		if n.RoundPL != nil {
			total.Add(total, n.RoundPL)
		}
	}
	return total
}

// Convert 让条目依次经过各步换算。非舍入节点一律保留精确分数，
// 只在 Round 为 true 的节点舍入。返回换算后的新条目（含追加的
// 来源记录）和完整轨迹。
func Convert(e *Entry, toCur string, steps []Step) (*Entry, *Trace) {
	if len(steps) == 0 {
		panic("fxsettle: 至少需要一步换算")
	}
	tr := &Trace{
		EntryID: e.ID,
		FromCur: e.Cur,
		ToCur:   toCur,
		Start:   new(big.Rat).Set(e.Val),
	}
	out := &Entry{ID: e.ID, Cur: toCur, Prov: append([]ProvRec{}, e.Prov...)}
	val := new(big.Rat).Set(e.Val)
	for i, st := range steps {
		exact := new(big.Rat).Mul(val, st.Rate)
		node := Node{
			Index: i,
			Label: st.Label,
			In:    val,
			Rate:  new(big.Rat).Set(st.Rate),
			Exact: exact,
		}
		out.record(OpConvert,
			fmt.Sprintf("步骤%d %s: ×%s（精确）", i, st.Label, Frac(st.Rate)),
			[]string{e.ID}, exact)
		node.Out = exact
		if st.Round {
			rounded := RoundTo(exact, st.Scale, st.Mode)
			node.Rounded = rounded
			node.RoundPL = new(big.Rat).Sub(rounded, exact)
			node.Out = rounded
			out.record(OpRound,
				fmt.Sprintf("步骤%d %s: 舍入到%d位小数(%s)，损益%s",
					i, st.Label, st.Scale, st.Mode, Frac(node.RoundPL)),
				[]string{e.ID}, rounded)
		}
		tr.Nodes = append(tr.Nodes, node)
		val = node.Out
	}
	out.Val = val
	tr.Final = val
	return out, tr
}
