// demo 演示多币种联合授信双时态敞口监控器的验收场景。
package main

import (
	"fmt"

	"exposure"
)

const wan = 10000.0

func main() {
	fmt.Println("=== 场景一：验收边界 —— 追溯调整击穿预警线 ===")
	m := exposure.NewMonitor("CNY", 0.65) // 预警线：额度的 65%
	m.SetFacility(1000*wan, 0, 100, 0)
	fmt.Println("时间  0：集团授信协议生效，额度 1000 万 CNY，有效期 [0,100)，预警线 65%")

	m.RecordBorrowing("A", 600*wan, "CNY", 10, 10)
	total, _ := m.ExposureAt(10, 10)
	fmt.Printf("时间 10：子公司 A 借款 600 万 CNY，集团总敞口 %.0f 万（占比 %.0f%%），未触线\n",
		total/wan, total/1000/wan*100)

	fmt.Println("时间 20：以当前知悉状态重放 [0,20]，无告警：", len(m.Replay(20, 0, 20)) == 0)

	m.RecordBorrowing("A", 700*wan, "CNY", 10, 30)
	fmt.Println("时间 30：追溯上调 A 的历史借款为 700 万（业务生效时间仍为 10）")

	old, _ := m.ExposureAt(10, 20)
	cur, _ := m.ExposureAt(10, 30)
	fmt.Printf("  双时态对照：valid=10 在 tx=20 知悉为 %.0f 万，在 tx=30 知悉为 %.0f 万\n",
		old/wan, cur/wan)

	hits := m.Replay(30, 0, 30)
	fmt.Printf("  监控器重现时间 30：重放 [0,30] 命中 %d 个告警时点\n", len(hits))
	for _, a := range m.Alerts() {
		fmt.Printf("  [告警] valid=%d tx=%d 敞口 %.0f 万 / 额度 %.0f 万 = %.1f%%\n    原因：%s\n",
			a.ValidTime, a.TxTime, a.Total/wan, a.Limit/wan, a.Ratio*100, a.Reason)
	}

	fmt.Println()
	fmt.Println("=== 场景二：汇率波动影响敞口折算 ===")
	fx := exposure.NewMonitor("CNY", 0.65)
	fx.SetFacility(1000*wan, 0, 100, 0)
	fx.SetFXRate("USD", 6.5, 0)
	fx.RecordBorrowing("B", 100*wan, "USD", 5, 5)
	t1, _ := fx.ExposureAt(10, 10)
	fmt.Printf("时间 10：B 借款 100 万 USD，汇率 6.5，折算敞口 %.0f 万 CNY\n", t1/wan)
	fx.SetFXRate("USD", 7.0, 15)
	t2, _ := fx.ExposureAt(20, 20)
	back, _ := fx.ExposureAt(10, 20)
	fmt.Printf("时间 15：USD 汇率升至 7.0，同一笔借款在 valid=20 折算 %.0f 万 CNY\n", t2/wan)
	fmt.Printf("  历史不变：valid=10 仍按 6.5 折算 %.0f 万 CNY\n", back/wan)

	fmt.Println()
	fmt.Println("=== 场景三：担保物价值变动引发质押率重算 ===")
	m.RecordCollateral("C1", "A", 1000*wan, "CNY", 5, 5)
	m.RecalcPledgeRatio("A", 10, 10, "初始登记")
	m.RecordCollateral("C1", "A", 800*wan, "CNY", 20, 20)
	m.RecalcPledgeRatio("A", 20, 20, "担保物价值变动：1000 万 -> 800 万")
	m.RecalcPledgeRatio("A", 30, 30, "借款追溯调整：600 万 -> 700 万")
	for _, tr := range m.PledgeTraces() {
		fmt.Printf("  [质押率重算] valid=%d tx=%d %s：敞口 %.0f 万 / 担保物 %.0f 万 = %.1f%%（%s）\n",
			tr.ValidTime, tr.TxTime, tr.Owner, tr.Exposure/wan, tr.CollateralValue/wan,
			tr.PledgeRatio*100, tr.Note)
	}
}
