package costmodel

// ChooseScanPlan 针对同一查询比较 SeqScan+Filter 与 IndexScan 两个候选
// 物理计划，返回总估算成本更低者。选择率变化时会在成本拐点处切换计划。
func ChooseScanPlan(w Weights, t TableStats, idx IndexStats, selectivity float64) PlanNode {
	seq := &Filter{Child: &SeqScan{Table: t}, Selectivity: selectivity}
	index := &IndexScan{Table: t, Index: idx, Selectivity: selectivity}
	if index.Cost(w) < seq.Cost(w) {
		return index
	}
	return seq
}

// BreakEvenSelectivity 解析求解 SeqScan+Filter 与 IndexScan 成本相等的
// 选择率拐点 s*：
//
//	seqCost(s)   = P*Cs + T*Ct + T*Co
//	indexCost(s) = (H + L*s + P*s)*Cr + T*s*(Ci + Ct)
//
// 令两者相等得 s* = (P*Cs + T*Ct + T*Co - H*Cr) / ((L+P)*Cr + T*(Ci+Ct))。
// 返回值可能超出 [0,1]，表示在有效选择率范围内某计划恒优。
func BreakEvenSelectivity(w Weights, t TableStats, idx IndexStats) float64 {
	p := float64(t.NumPages)
	tuples := float64(t.NumTuples)
	seqConst := p*w.SeqPageCost + tuples*w.CPUTupleCost + tuples*w.CPUOperatorCost
	indexConst := float64(idx.Height) * w.RandomPageCost
	indexSlope := (float64(idx.LeafPages)+p)*w.RandomPageCost + tuples*(w.CPUIndexTupleCost+w.CPUTupleCost)
	return (seqConst - indexConst) / indexSlope
}
