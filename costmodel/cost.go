// Package costmodel 实现基于成本模型（Cost Model）的 I/O 与 CPU 复合打分器。
//
// 通过为页面读取（I/O Cost）与元组比较/哈希计算（CPU Cost）分别赋权，
// 对物理计划树递归求和，得到整棵计划树的总估算成本值。
package costmodel

// Weights 定义成本模型的权重系数（可配置，默认值参考 PostgreSQL）。
type Weights struct {
	// SeqPageCost 顺序扫描读取一个页面的 I/O 权重。
	SeqPageCost float64
	// RandomPageCost 随机读取一个页面（索引扫描回表）的 I/O 权重。
	RandomPageCost float64
	// CPUTupleCost 处理一个元组的 CPU 权重（取行、评估代价）。
	CPUTupleCost float64
	// CPUOperatorCost 每元组执行一次比较/哈希等运算符的 CPU 权重。
	CPUOperatorCost float64
	// CPUIndexTupleCost 处理一个索引条目的 CPU 权重。
	CPUIndexTupleCost float64
}

// DefaultWeights 返回与 PostgreSQL 一致的经典默认权重。
func DefaultWeights() Weights {
	return Weights{
		SeqPageCost:       1.0,
		RandomPageCost:    4.0,
		CPUTupleCost:      0.01,
		CPUOperatorCost:   0.0025,
		CPUIndexTupleCost: 0.005,
	}
}

// TableStats 描述一张表的统计信息，是成本估算的输入。
type TableStats struct {
	Name      string
	NumPages  int // 表占用的磁盘页数
	NumTuples int // 表中的元组总数
}

// IndexStats 描述一个索引的统计信息。
type IndexStats struct {
	Name      string
	Table     string // 所属表
	Height    int    // B+ 树高度（根到叶需要访问的页数）
	LeafPages int    // 叶子页总数
}

// TuplesPerPage 返回每页平均元组数。
func (t TableStats) TuplesPerPage() float64 {
	if t.NumPages == 0 {
		return 0
	}
	return float64(t.NumTuples) / float64(t.NumPages)
}
