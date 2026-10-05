package fxsettle

import (
	"fmt"
	"math/big"
)

// NodeCompare 某个舍入节点上“逐笔舍入后求和”与“汇总后一次舍入”的对比。
type NodeCompare struct {
	Index      int
	Label      string
	PerItemSum *big.Rat // 各笔在本节点分别舍入后的合计
	AggExact   *big.Rat // 汇总口径在本节点舍入前的精确值
	AggRounded *big.Rat // 汇总口径在本节点一次舍入后的值
	Diff       *big.Rat // PerItemSum − AggRounded
}

// Comparison 逐笔换汇 vs 汇总换汇的整体对比结果。
type Comparison struct {
	ToCur        string
	PerItemTotal *big.Rat // 逐笔换算（各节点逐笔舍入）的最终合计
	AggTotal     *big.Rat // 汇总换算（各节点仅对合计舍入一次）的最终合计
	Diff         *big.Rat // PerItemTotal − AggTotal
	Nodes        []NodeCompare
	ItemTraces   []*Trace
}

// Compare 对同一批账目分别按“逐笔”和“汇总”两种口径跑同一条流水线，
// 量化每个舍入节点以及最终的差额。两种口径使用完全相同的汇率与
// 舍入规则，唯一差别是舍入作用的对象：每笔 vs 合计。
func Compare(entries []*Entry, toCur string, steps []Step) *Comparison {
	if len(entries) == 0 {
		panic("fxsettle: Compare 至少需要一条账目")
	}
	c := &Comparison{ToCur: toCur}

	// 逐笔口径：每笔独立走流水线。
	perItem := new(big.Rat)
	traces := make([]*Trace, 0, len(entries))
	for _, e := range entries {
		_, tr := Convert(e, toCur, steps)
		traces = append(traces, tr)
		perItem.Add(perItem, tr.Final)
	}
	c.ItemTraces = traces
	c.PerItemTotal = perItem

	// 汇总口径：每步先对全体精确求和，仅在舍入节点对合计舍入一次。
	agg := new(big.Rat)
	for _, e := range entries {
		agg.Add(agg, e.Val)
	}
	for i, st := range steps {
		exact := new(big.Rat).Mul(agg, st.Rate)
		if !st.Round {
			agg = exact
			continue
		}
		rounded := RoundTo(exact, st.Scale, st.Mode)
		perItemSum := new(big.Rat)
		for _, tr := range traces {
			perItemSum.Add(perItemSum, tr.Nodes[i].Out)
		}
		c.Nodes = append(c.Nodes, NodeCompare{
			Index:      i,
			Label:      st.Label,
			PerItemSum: perItemSum,
			AggExact:   exact,
			AggRounded: rounded,
			Diff:       new(big.Rat).Sub(perItemSum, rounded),
		})
		agg = rounded
	}
	c.AggTotal = agg
	c.Diff = new(big.Rat).Sub(perItem, agg)
	return c
}

// CorrectionEntry 生成更正条目：金额 = AggTotal − PerItemTotal，
// 把它并入逐笔口径的账后，两种口径完全对齐。来源链记录差额成因。
func (c *Comparison) CorrectionEntry(id string) *Entry {
	amount := new(big.Rat).Sub(c.AggTotal, c.PerItemTotal)
	inputs := make([]string, 0, len(c.ItemTraces))
	for _, tr := range c.ItemTraces {
		inputs = append(inputs, tr.EntryID)
	}
	e := &Entry{ID: id, Cur: c.ToCur, Val: amount}
	e.record(OpCorrect,
		fmt.Sprintf("逐笔/汇总口径差额更正: 逐笔合计%s，汇总合计%s，差额%s",
			Frac(c.PerItemTotal), Frac(c.AggTotal), Frac(c.Diff)),
		inputs, amount)
	return e
}
