package costmodel

// TableStats 描述优化器可用的表统计信息。
type TableStats struct {
	Name       string
	Rows       float64 // 表中的元组数
	Pages      float64 // 表占用的数据页数
	IndexPages float64 // 相关索引占用的页数
}
