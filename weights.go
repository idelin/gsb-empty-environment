package costmodel

// Weights 定义成本模型的权重系数：
// I/O Cost 由页面读取权重构成，CPU Cost 由元组比较/哈希计算权重构成。
type Weights struct {
	SeqPageCost       float64 // 顺序读取一个数据页的代价
	RandomPageCost    float64 // 随机读取一个数据页/索引页的代价
	CpuTupleCost      float64 // 处理一个元组的 CPU 代价
	CpuIndexTupleCost float64 // 处理一个索引项的 CPU 代价
	CpuOperatorCost   float64 // 对单个元组求值一次过滤条件（比较）的代价
	CpuHashCost       float64 // 对单个元组做一次哈希计算的代价
}

// DefaultWeights 返回一组类 PostgreSQL 的默认权重。
func DefaultWeights() Weights {
	return Weights{
		SeqPageCost:       1.0,
		RandomPageCost:    4.0,
		CpuTupleCost:      0.01,
		CpuIndexTupleCost: 0.005,
		CpuOperatorCost:   0.0025,
		CpuHashCost:       0.01,
	}
}
