package fxsettle

import (
	"math/big"
	"strings"
	"testing"
)

func mustRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, err := ParseRat(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func eq(t *testing.T, got *big.Rat, want string) {
	t.Helper()
	w, ok := new(big.Rat).SetString(want)
	if !ok {
		t.Fatalf("bad want %q", want)
	}
	if got.Cmp(w) != 0 {
		t.Fatalf("got %s, want %s", Frac(got), Frac(w))
	}
}

// 不指定舍入节点时，中间值必须保持精确分数，绝不提前舍入。
func TestNoEarlyRounding(t *testing.T) {
	e := NewEntry("b1", "USD", mustRat(t, "100"), "原始账单")
	steps := []Step{
		FX("USD->HKD", Rat(1, 3)),  // 除不尽的汇率
		FX("HKD->CNY", Rat(7, 11)), // 又一个除不尽的汇率
	}
	out, tr := Convert(e, "CNY", steps)
	// 100 × 1/3 × 7/11 = 700/33，必须精确保留
	eq(t, tr.Nodes[0].Exact, "100/3")
	eq(t, tr.Nodes[1].Exact, "700/33")
	eq(t, out.Val, "700/33")
	if tr.RoundPLTotal().Sign() != 0 {
		t.Fatalf("无舍入节点时累计损益应为 0，得到 %s", Frac(tr.RoundPLTotal()))
	}
}

// 舍入只在指定节点发生，且损益 = 舍入后 − 精确值。
func TestRoundingNodePL(t *testing.T) {
	e := NewEntry("b2", "USD", mustRat(t, "1"), "原始账单")
	steps := []Step{
		FX("USD->JPY", Rat(300, 7)),                 // 精确中间值 300/7
		FXRound("JPY入账", Rat(1, 1), 2, RoundHalfUp), // 舍入到分
	}
	_, tr := Convert(e, "JPY", steps)
	n := tr.Nodes[1]
	if !n.IsRoundingNode() {
		t.Fatal("节点1应为舍入节点")
	}
	eq(t, n.Exact, "300/7")
	eq(t, n.Rounded, "4286/100") // 42.857142... -> 42.86
	eq(t, n.RoundPL, "1/350")    // 42.86 − 300/7 = 1/350
	// 舍入前精确值 + 损益 = 舍入后值
	sum := new(big.Rat).Add(n.Exact, n.RoundPL)
	if sum.Cmp(n.Rounded) != 0 {
		t.Fatalf("勾稽失败: %s + %s != %s", Frac(n.Exact), Frac(n.RoundPL), Frac(n.Rounded))
	}
	eq(t, tr.RoundPLTotal(), "1/350")
}

// 逐笔换汇 vs 汇总换汇：3 笔 0.005，half-up 到分。
// 逐笔: 0.01×3 = 0.03；汇总: 0.015 -> 0.02；差额 0.01。
func TestCompareItemVsAggregate(t *testing.T) {
	var entries []*Entry
	for _, id := range []string{"i1", "i2", "i3"} {
		entries = append(entries, NewEntry(id, "USD", mustRat(t, "0.005"), "零钞"))
	}
	steps := []Step{FXRound("USD入账", Rat(1, 1), 2, RoundHalfUp)}
	c := Compare(entries, "USD", steps)
	eq(t, c.PerItemTotal, "3/100")
	eq(t, c.AggTotal, "1/50")
	eq(t, c.Diff, "1/100")
	if len(c.Nodes) != 1 {
		t.Fatalf("应有1个舍入节点对比，得到 %d", len(c.Nodes))
	}
	eq(t, c.Nodes[0].Diff, "1/100")

	// 更正条目并入逐笔口径后，与汇总口径对齐。
	corr := c.CorrectionEntry("corr-1")
	eq(t, corr.Val, "-1/100")
	adjusted := new(big.Rat).Add(c.PerItemTotal, corr.Val)
	if adjusted.Cmp(c.AggTotal) != 0 {
		t.Fatalf("更正后仍不对齐: %s != %s", Frac(adjusted), Frac(c.AggTotal))
	}
}

// 多级流水线：中间舍入节点会同时影响逐笔与汇总两条路径。
func TestCompareMultiStep(t *testing.T) {
	entries := []*Entry{
		NewEntry("m1", "USD", mustRat(t, "10.004"), ""),
		NewEntry("m2", "USD", mustRat(t, "20.006"), ""),
	}
	steps := []Step{
		FXRound("USD->EUR入账", mustRat(t, "0.9"), 2, RoundHalfUp),
		FX("EUR->GBP", mustRat(t, "0.8")),
		FXRound("GBP入账", Rat(1, 1), 2, RoundHalfUp),
	}
	c := Compare(entries, "GBP", steps)
	if len(c.Nodes) != 2 {
		t.Fatalf("应有2个舍入节点对比，得到 %d", len(c.Nodes))
	}
	// 两个口径的总差额 = 最终合计之差，且报告可生成。
	if new(big.Rat).Sub(c.PerItemTotal, c.AggTotal).Cmp(c.Diff) != 0 {
		t.Fatal("Diff 勾稽失败")
	}
	rep := c.Report()
	for _, kw := range []string{"舍入节点[0]", "舍入节点[2]", "总差额"} {
		if !strings.Contains(rep, kw) {
			t.Fatalf("报告缺少 %q:\n%s", kw, rep)
		}
	}
}

// 反向更正：从最终舍入值精确还原原始金额。
func TestReverseRecoversOriginal(t *testing.T) {
	e := NewEntry("r1", "USD", mustRat(t, "123.456"), "原始账单")
	steps := []Step{
		FX("USD->EUR", mustRat(t, "0.91")),
		FXRound("EUR入账", Rat(1, 1), 2, RoundHalfUp),
		FX("EUR->GBP", mustRat(t, "0.86")),
		FXRound("GBP入账", Rat(1, 1), 2, RoundHalfEven),
	}
	_, tr := Convert(e, "GBP", steps)
	rt := Reverse(tr)
	if rt.Recovered.Cmp(tr.Start) != 0 {
		t.Fatalf("反向还原失败: got %s, want %s", Frac(rt.Recovered), Frac(tr.Start))
	}
	// 反向轨迹应包含 2 个更正步 + 4 个逆换算步（每个正向步一个）。
	var corr, inv int
	for _, n := range rt.Nodes {
		switch n.Kind {
		case RevCorrect:
			corr++
		case RevInvert:
			inv++
		}
	}
	if corr != 2 || inv != 4 {
		t.Fatalf("反向步数不对: correct=%d invert=%d", corr, inv)
	}
	// 更正条目金额合计 = 原轨迹累计舍入损益的相反数。
	ces := rt.CorrectionEntries("rev")
	if len(ces) != 2 {
		t.Fatalf("应有2条更正分录，得到 %d", len(ces))
	}
	total := new(big.Rat)
	for _, ce := range ces {
		total.Add(total, ce.Val)
	}
	want := new(big.Rat).Neg(tr.RoundPLTotal())
	if total.Cmp(want) != 0 {
		t.Fatalf("更正分录合计 %s != %s", Frac(total), Frac(want))
	}
}

// 来源链：每个值都能追溯到产生它的操作与输入。
func TestProvenance(t *testing.T) {
	e := NewEntry("p1", "USD", mustRat(t, "10"), "发票#1")
	steps := []Step{
		FX("USD->EUR", mustRat(t, "0.9")),
		FXRound("EUR入账", Rat(1, 1), 2, RoundHalfUp),
	}
	out, _ := Convert(e, "EUR", steps)
	var ops []Op
	for _, p := range out.Prov {
		ops = append(ops, p.Op)
	}
	want := []Op{OpInput, OpConvert, OpConvert, OpRound}
	if len(ops) != len(want) {
		t.Fatalf("来源链长度 %d，期望 %d", len(ops), len(want))
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("来源链[%d] = %s，期望 %s", i, ops[i], want[i])
		}
	}
	rep := ProvReport(out)
	if !strings.Contains(rep, "发票#1") || !strings.Contains(rep, "round") {
		t.Fatalf("来源报告不完整:\n%s", rep)
	}
}

// 汇总来源链记录全部输入条目。
func TestSumProvenance(t *testing.T) {
	a := NewEntry("s1", "USD", mustRat(t, "1.005"), "")
	b := NewEntry("s2", "USD", mustRat(t, "2.005"), "")
	s := Sum("sum-1", a, b)
	eq(t, s.Val, "301/100")
	if len(s.Prov) != 1 || len(s.Prov[0].Inputs) != 2 {
		t.Fatalf("汇总来源记录异常: %+v", s.Prov)
	}
}

// 舍入模式语义。
func TestRoundModes(t *testing.T) {
	cases := []struct {
		in    string
		scale int
		mode  RoundMode
		want  string
	}{
		{"2.5", 0, RoundHalfUp, "3"},
		{"2.5", 0, RoundHalfEven, "2"},
		{"3.5", 0, RoundHalfEven, "4"},
		{"-2.5", 0, RoundHalfUp, "-3"},
		{"-2.5", 0, RoundHalfEven, "-2"},
		{"2.1", 0, RoundUp, "3"},
		{"-2.1", 0, RoundUp, "-3"},
		{"2.9", 0, RoundDown, "2"},
		{"-2.9", 0, RoundDown, "-2"},
		{"0.005", 2, RoundHalfUp, "1/100"},
		{"1/3", 2, RoundHalfUp, "33/100"},
	}
	for _, c := range cases {
		got := RoundTo(mustRat(t, c.in), c.scale, c.mode)
		eq(t, got, c.want)
	}
}
