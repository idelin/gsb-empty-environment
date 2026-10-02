package costmodel

// Plan 是物理计划树节点。Cost 递归计算以该节点为根的整棵子树的总估算成本，
// OutputRows 返回该节点估算的输出行数（供父节点继续估算）。
type Plan interface {
	Cost(w Weights) float64
	OutputRows() float64
}

// SeqScan 全表顺序扫描。
type SeqScan struct {
	Table TableStats
}

func (s *SeqScan) Cost(w Weights) float64 {
	return s.Table.Pages*w.SeqPageCost + s.Table.Rows*w.CpuTupleCost
}

func (s *SeqScan) OutputRows() float64 { return s.Table.Rows }

// IndexScan 基于索引的扫描，Selectivity 为索引谓词选择率（0~1）。
// 简化模型：随机读取全部索引页 + 按选择率回表随机读取数据页。
type IndexScan struct {
	Table       TableStats
	Selectivity float64
}

func (s *IndexScan) Cost(w Weights) float64 {
	ioCost := (s.Table.IndexPages + s.Table.Pages*s.Selectivity) * w.RandomPageCost
	cpuCost := s.Table.Rows*w.CpuIndexTupleCost + s.OutputRows()*w.CpuTupleCost
	return ioCost + cpuCost
}

func (s *IndexScan) OutputRows() float64 { return s.Table.Rows * s.Selectivity }

// Filter 过滤节点，对子计划输出的每个元组求值一次谓词。
type Filter struct {
	Selectivity float64
	Child       Plan
}

func (f *Filter) Cost(w Weights) float64 {
	return f.Child.Cost(w) + f.Child.OutputRows()*w.CpuOperatorCost
}

func (f *Filter) OutputRows() float64 { return f.Child.OutputRows() * f.Selectivity }

// HashJoin 哈希连接：对 build 侧每行做哈希建表，对 probe 侧每行做哈希探测。
type HashJoin struct {
	JoinSelectivity float64
	Build           Plan
	Probe           Plan
}

func (h *HashJoin) Cost(w Weights) float64 {
	return h.Build.Cost(w) + h.Probe.Cost(w) +
		h.Build.OutputRows()*w.CpuHashCost +
		h.Probe.OutputRows()*w.CpuHashCost
}

func (h *HashJoin) OutputRows() float64 {
	return h.Build.OutputRows() * h.Probe.OutputRows() * h.JoinSelectivity
}
