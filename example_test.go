package fxsettle_test

import (
	"fmt"

	"fxsettle"
)

// 完整流程：逐笔 vs 汇总对比 → 生成更正条目 → 反向还原复核。
func Example() {
	rate := fxsettle.Must(fxsettle.ParseRat("7.1234")) // 1 USD = 7.1234 CNY
	steps := []fxsettle.Step{
		fxsettle.FX("USD->CNY 中间换算", rate),
		fxsettle.FXRound("CNY 入账舍入", fxsettle.Rat(1, 1), 2, fxsettle.RoundHalfUp),
	}

	bills := []*fxsettle.Entry{
		fxsettle.NewEntry("bill-1", "USD", fxsettle.Must(fxsettle.ParseRat("0.007")), "零钞1"),
		fxsettle.NewEntry("bill-2", "USD", fxsettle.Must(fxsettle.ParseRat("0.007")), "零钞2"),
		fxsettle.NewEntry("bill-3", "USD", fxsettle.Must(fxsettle.ParseRat("0.007")), "零钞3"),
	}

	// 1. 对比逐笔与汇总两种口径。
	c := fxsettle.Compare(bills, "CNY", steps)
	fmt.Print(c.Report())

	// 2. 生成对齐两种口径的更正条目。
	corr := c.CorrectionEntry("corr-1")
	fmt.Printf("更正条目 %s: %s %s\n\n", corr.ID, corr.Cur, fxsettle.Frac(corr.Val))

	// 3. 对第一笔做反向更正，精确还原原始金额。
	_, tr := fxsettle.Convert(bills[0], "CNY", steps)
	fmt.Print(tr.Report())
	rt := fxsettle.Reverse(tr)
	fmt.Print(rt.Report())

	// Output:
	// 逐笔 vs 汇总（目标币种 CNY）
	//   舍入节点[1] CNY 入账舍入
	//       逐笔舍入合计: 3/20 (≈0.150000)
	//       汇总精确值:   747957/5000000 (≈0.149591)
	//       汇总舍入后:   3/20 (≈0.150000)
	//       本节点差额:   0 (≈0.000000)
	//   逐笔最终合计: 3/20 (≈0.150000)
	//   汇总最终合计: 3/20 (≈0.150000)
	//   总差额(逐笔−汇总): 0 (≈0.000000)
	// 更正条目 corr-1: CNY 0
	//
	// 条目 bill-1: USD 7/1000 -> CNY 1/20
	//   [0] USD->CNY 中间换算  ×35617/5000
	//       入: 7/1000  精确中间值: 249319/5000000 (≈0.049864)
	//   [1] CNY 入账舍入  ×1
	//       入: 249319/5000000  精确中间值: 249319/5000000 (≈0.049864)
	//       舍入后: 1/20  舍入损益: 681/5000000 (≈0.000136)
	//   累计舍入损益: 681/5000000 (≈0.000136)
	// 反向更正 条目 bill-1: CNY 1/20 -> USD 7/1000
	//   [correct] 反向更正 步骤1 CNY 入账舍入: 加回舍入损益 -681/5000000
	//       入: 1/20  系数: 1  更正额: -681/5000000  出: 249319/5000000 (≈0.049864)
	//   [invert] 反向换算 步骤1 CNY 入账舍入: ×1（=1/(1)）
	//       入: 249319/5000000  系数: 1  更正额: 0  出: 249319/5000000 (≈0.049864)
	//   [invert] 反向换算 步骤0 USD->CNY 中间换算: ×5000/35617（=1/(35617/5000)）
	//       入: 249319/5000000  系数: 5000/35617  更正额: 0  出: 7/1000 (≈0.007000)
}
