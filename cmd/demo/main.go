// demo 演示：对同一查询比较 SeqScan+Filter 与 IndexScan 的成本，
// 并展示选择率跨越拐点时的计划切换。
package main

import (
	"fmt"

	"costmodel/costmodel"
)

func main() {
	w := costmodel.DefaultWeights()
	table := costmodel.TableStats{Name: "users", NumPages: 10000, NumTuples: 1000000}
	index := costmodel.IndexStats{Name: "idx_users_age", Table: "users", Height: 3, LeafPages: 2000}

	fmt.Printf("权重: SeqPage=%.2f RandomPage=%.2f CPUTuple=%.4f CPUOp=%.4f CPUIndexTuple=%.4f\n",
		w.SeqPageCost, w.RandomPageCost, w.CPUTupleCost, w.CPUOperatorCost, w.CPUIndexTupleCost)
	fmt.Printf("统计: %s pages=%d tuples=%d; 索引 %s height=%d leafPages=%d\n\n",
		table.Name, table.NumPages, table.NumTuples, index.Name, index.Height, index.LeafPages)

	sStar := costmodel.BreakEvenSelectivity(w, table, index)
	fmt.Printf("成本拐点选择率 s* = %.4f%%\n\n", sStar*100)

	fmt.Printf("%-12s %-18s %-18s %s\n", "选择率", "SeqScan+Filter", "IndexScan", "选择计划")
	for _, s := range []float64{0.0001, 0.001, 0.005, 0.01, 0.02, 0.05, 0.1, 0.3, 0.5, 1.0} {
		seq := &costmodel.Filter{Child: &costmodel.SeqScan{Table: table}, Selectivity: s}
		idx := &costmodel.IndexScan{Table: table, Index: index, Selectivity: s}
		chosen := costmodel.ChooseScanPlan(w, table, index, s)
		fmt.Printf("%-12.4f %-18.2f %-18.2f %s\n", s, seq.Cost(w), idx.Cost(w), chosen.String())
	}
}
