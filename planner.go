package costmodel

// ChooseScanPlan 针对同一查询（同一张表 + 同一选择率的过滤谓词），
// 比较 SeqScan+Filter 与 IndexScan 两个物理计划的总成本，返回更优者。
// 返回值为（计划, 计划名）。
func ChooseScanPlan(t TableStats, selectivity float64, w Weights) (Plan, string) {
	seq := &Filter{Selectivity: selectivity, Child: &SeqScan{Table: t}}
	idx := &IndexScan{Table: t, Selectivity: selectivity}
	if idx.Cost(w) < seq.Cost(w) {
		return idx, "IndexScan"
	}
	return seq, "SeqScan+Filter"
}
