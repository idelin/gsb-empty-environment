package costmodel

// PlanNode 是物理计划树节点的统一接口。
// Cost 递归返回以该节点为根的整棵子树的总估算成本，
// Rows 返回该节点输出的估算行数（供父节点继续估算）。
type PlanNode interface {
	Cost(w Weights) float64
	Rows() float64
	String() string
}

// SeqScan 全表顺序扫描。
type SeqScan struct {
	Table TableStats
}

func (s *SeqScan) Cost(w Weights) float64 {
	io := float64(s.Table.NumPages) * w.SeqPageCost
	cpu := float64(s.Table.NumTuples) * w.CPUTupleCost
	return io + cpu
}

func (s *SeqScan) Rows() float64 { return float64(s.Table.NumTuples) }

func (s *SeqScan) String() string { return "SeqScan(" + s.Table.Name + ")" }

// IndexScan 基于索引的扫描：先走 B+ 树定位，再回表取元组。
// Selectivity 为索引条件的选择率（0~1）。
type IndexScan struct {
	Table       TableStats
	Index       IndexStats
	Selectivity float64
}

func (s *IndexScan) Cost(w Weights) float64 {
	// 索引侧 I/O：树高 + 按选择率访问的叶子页。
	indexPages := float64(s.Index.Height) + float64(s.Index.LeafPages)*s.Selectivity
	// 堆侧 I/O：按选择率随机回表，近似为命中页面数 * 随机页权重。
	heapPages := float64(s.Table.NumPages) * s.Selectivity
	io := (indexPages + heapPages) * w.RandomPageCost

	// CPU：索引条目处理 + 命中元组的取行代价。
	indexTuples := float64(s.Table.NumTuples) * s.Selectivity
	cpu := indexTuples*w.CPUIndexTupleCost + indexTuples*w.CPUTupleCost
	return io + cpu
}

func (s *IndexScan) Rows() float64 {
	return float64(s.Table.NumTuples) * s.Selectivity
}

func (s *IndexScan) String() string {
	return "IndexScan(" + s.Index.Name + " on " + s.Table.Name + ")"
}

// Filter 过滤节点：对子节点输出的每个元组做一次比较运算，
// 按选择率向下游输出行。总成本 = 子树成本 + 自身 CPU 成本。
type Filter struct {
	Child       PlanNode
	Selectivity float64 // 过滤条件选择率（0~1）
}

func (f *Filter) Cost(w Weights) float64 {
	cpu := f.Child.Rows() * w.CPUOperatorCost
	return f.Child.Cost(w) + cpu
}

func (f *Filter) Rows() float64 { return f.Child.Rows() * f.Selectivity }

func (f *Filter) String() string { return "Filter(" + f.Child.String() + ")" }

// HashJoin 哈希连接：构建侧做哈希计算，探测侧做哈希查找，
// 演示 CPU 哈希权重的递归累加。
type HashJoin struct {
	Outer, Inner  PlanNode
	JoinSelectivity float64
}

func (j *HashJoin) Cost(w Weights) float64 {
	build := j.Inner.Rows() * w.CPUOperatorCost  // 建表哈希
	probe := j.Outer.Rows() * w.CPUOperatorCost  // 探测哈希
	return j.Outer.Cost(w) + j.Inner.Cost(w) + build + probe
}

func (j *HashJoin) Rows() float64 {
	return j.Outer.Rows() * j.Inner.Rows() * j.JoinSelectivity
}

func (j *HashJoin) String() string {
	return "HashJoin(" + j.Outer.String() + ", " + j.Inner.String() + ")"
}
