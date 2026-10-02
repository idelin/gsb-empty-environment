// 运行样例：Q1 考核周期 KPI 权重的时态版本管理与追溯调整。
package main

import (
	"fmt"

	kpi "kpitemporal"
)

func main() {
	ledger := kpi.NewLedger(90) // Q1 共 90 天

	v1 := ledger.AddVersion("张三", "年初战略规划", 1, []kpi.Entry{
		{KPI: "A", Weight: 0.6, StartDay: 1, EndDay: 60},
		{KPI: "B", Weight: 0.4, StartDay: 1, EndDay: 60},
	})
	v2 := ledger.AddVersion("李四", "战略重心由A转向B，追溯全季度", 45, []kpi.Entry{
		{KPI: "A", Weight: 0.4, StartDay: 1, EndDay: 91},
		{KPI: "B", Weight: 0.6, StartDay: 1, EndDay: 91},
	})

	actuals := map[string]float64{"A": 80, "B": 90}

	fmt.Println("=== 版本台账 ===")
	for _, v := range ledger.Versions() {
		fmt.Printf("v%d 第%d天登记 责任人:%s 原因:%s\n  签名:%s\n",
			v.ID, v.RecordedDay, v.Author, v.Reason, v.Signature[:16]+"...")
		for _, e := range v.Entries {
			fmt.Printf("  KPI %s 权重 %.1f 生效区间 [%d, %d)\n", e.KPI, e.Weight, e.StartDay, e.EndDay)
		}
	}

	fmt.Println("\n=== 历史快照推演（实际得分 A=80, B=90）===")
	for _, id := range []int{v1.ID, v2.ID} {
		w, _ := ledger.EffectiveWeights(id)
		s, _ := ledger.Score(id, actuals)
		fmt.Printf("按 v%d 快照: 有效权重 A=%.4f B=%.4f -> 季度末加权得分 %.4f\n", id, w["A"], w["B"], s)
	}

	imp, _ := ledger.Impact(v1.ID, v2.ID, actuals)
	fmt.Println("\n=== 版本影响（v1 -> v2）===")
	for _, c := range imp.WeightChanges {
		fmt.Printf("KPI %s 有效权重 %.4f -> %.4f\n", c.KPI, c.FromWeight, c.ToWeight)
	}
	fmt.Printf("调整前得分 %.4f，调整后得分 %.4f，实际得分差 %+.4f\n", imp.ScoreBefore, imp.ScoreAfter, imp.ScoreDelta)
}
