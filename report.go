package fxsettle

import (
	"fmt"
	"strings"
)

// 报告统一用 6 位小数展示十进制近似，精确值始终以分数为准。
const reportDigits = 6

// Report 输出单条换算轨迹的复核报告：每步的精确分数中间值、
// 舍入节点的舍入前后值与舍入损益。
func (t *Trace) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "条目 %s: %s %s -> %s %s\n",
		t.EntryID, t.FromCur, Frac(t.Start), t.ToCur, Frac(t.Final))
	for _, n := range t.Nodes {
		fmt.Fprintf(&b, "  [%d] %s  ×%s\n", n.Index, n.Label, Frac(n.Rate))
		fmt.Fprintf(&b, "      入: %s  精确中间值: %s (≈%s)\n",
			Frac(n.In), Frac(n.Exact), Dec(n.Exact, reportDigits))
		if n.IsRoundingNode() {
			fmt.Fprintf(&b, "      舍入后: %s  舍入损益: %s (≈%s)\n",
				Frac(n.Rounded), Frac(n.RoundPL), Dec(n.RoundPL, reportDigits))
		}
	}
	fmt.Fprintf(&b, "  累计舍入损益: %s (≈%s)\n",
		Frac(t.RoundPLTotal()), Dec(t.RoundPLTotal(), reportDigits))
	return b.String()
}

// Report 输出逐笔 vs 汇总口径的对比报告。
func (c *Comparison) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "逐笔 vs 汇总（目标币种 %s）\n", c.ToCur)
	for _, nc := range c.Nodes {
		fmt.Fprintf(&b, "  舍入节点[%d] %s\n", nc.Index, nc.Label)
		fmt.Fprintf(&b, "      逐笔舍入合计: %s (≈%s)\n", Frac(nc.PerItemSum), Dec(nc.PerItemSum, reportDigits))
		fmt.Fprintf(&b, "      汇总精确值:   %s (≈%s)\n", Frac(nc.AggExact), Dec(nc.AggExact, reportDigits))
		fmt.Fprintf(&b, "      汇总舍入后:   %s (≈%s)\n", Frac(nc.AggRounded), Dec(nc.AggRounded, reportDigits))
		fmt.Fprintf(&b, "      本节点差额:   %s (≈%s)\n", Frac(nc.Diff), Dec(nc.Diff, reportDigits))
	}
	fmt.Fprintf(&b, "  逐笔最终合计: %s (≈%s)\n", Frac(c.PerItemTotal), Dec(c.PerItemTotal, reportDigits))
	fmt.Fprintf(&b, "  汇总最终合计: %s (≈%s)\n", Frac(c.AggTotal), Dec(c.AggTotal, reportDigits))
	fmt.Fprintf(&b, "  总差额(逐笔−汇总): %s (≈%s)\n", Frac(c.Diff), Dec(c.Diff, reportDigits))
	return b.String()
}

// Report 输出反向更正轨迹的复核报告。
func (rt *ReverseTrace) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "反向更正 条目 %s: %s %s -> %s %s\n",
		rt.EntryID, rt.FromCur, Frac(rt.Start), rt.ToCur, Frac(rt.Recovered))
	for _, n := range rt.Nodes {
		fmt.Fprintf(&b, "  [%s] %s\n", n.Kind, n.Label)
		fmt.Fprintf(&b, "      入: %s  系数: %s  更正额: %s  出: %s (≈%s)\n",
			Frac(n.In), Frac(n.Factor), Frac(n.Adjust), Frac(n.Out), Dec(n.Out, reportDigits))
	}
	return b.String()
}

// ProvReport 输出来源链，便于审计追溯每个值的来历。
func ProvReport(e *Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "来源链 条目 %s (%s %s):\n", e.ID, e.Cur, Frac(e.Val))
	for i, p := range e.Prov {
		inputs := "-"
		if len(p.Inputs) > 0 {
			inputs = strings.Join(p.Inputs, ",")
		}
		fmt.Fprintf(&b, "  %d. [%s] %s  输入: %s  值: %s\n",
			i, p.Op, p.Note, inputs, Frac(p.Value))
	}
	return b.String()
}
