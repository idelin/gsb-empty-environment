// demo 演示：针对同一查询（users 表 + 等值/范围过滤谓词），
// 在不同选择率下分别计算 SeqScan+Filter 与 IndexScan 的估算成本，
// 并由成本模型选择更优物理计划。
package main

import (
	"fmt"

	"costmodel"
)

func main() {
	w := costmodel.DefaultWeights()
	users := costmodel.TableStats{Name: "users", Rows: 100000, Pages: 10000, IndexPages: 300}

	fmt.Printf("表统计: rows=%.0f pages=%.0f indexPages=%.0f\n", users.Rows, users.Pages, users.IndexPages)
	fmt.Printf("权重: seqPage=%.2f randomPage=%.2f cpuTuple=%.4f cpuIndexTuple=%.4f cpuOperator=%.4f\n\n",
		w.SeqPageCost, w.RandomPageCost, w.CpuTupleCost, w.CpuIndexTupleCost, w.CpuOperatorCost)

	fmt.Printf("%-12s %16s %16s  %s\n", "selectivity", "SeqScan+Filter", "IndexScan", "chosen")
	for _, sel := range []float64{0.001, 0.01, 0.05, 0.1, 0.2, 0.25, 0.3, 0.5, 0.9} {
		seq := &costmodel.Filter{Selectivity: sel, Child: &costmodel.SeqScan{Table: users}}
		idx := &costmodel.IndexScan{Table: users, Selectivity: sel}
		_, name := costmodel.ChooseScanPlan(users, sel, w)
		fmt.Printf("%-12.3f %16.2f %16.2f  %s\n", sel, seq.Cost(w), idx.Cost(w), name)
	}

	// 二分定位计划切换的拐点。
	lo, hi := 0.0, 1.0
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if _, name := costmodel.ChooseScanPlan(users, mid, w); name == "IndexScan" {
			lo = mid
		} else {
			hi = mid
		}
	}
	fmt.Printf("\n计划切换拐点: selectivity ≈ %.6f（低于它选 IndexScan，高于它选 SeqScan+Filter）\n", (lo+hi)/2)
}
