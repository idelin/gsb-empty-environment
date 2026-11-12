package netsec

// Violation 记录一次无法证明安全的读取或一次失败的不变量检查。
type Violation struct {
	Site   string
	Block  BlockID
	Op     int
	Reason string
}

// Certificate 是单条数据读取的范围证书。
//
// 证书声明：在到达该读取点的任意具体执行中，访问区间 [AccessLo, AccessHi]
// 完整落在每个可能来源区域内，且来源集合 Origins 已随合流精确保留。
type Certificate struct {
	Site       string
	Block      BlockID
	Op         int
	Size       int64
	IndexRange Interval
	// Origins 为该读取可能命中的区域来源集合（合流并集）。
	Origins []RegionID
	// OriginRanges 给出每个来源下的绝对访问闭区间 [Lo, Hi]。
	OriginRanges map[RegionID]Interval
	// AccessLo / AccessHi 是跨全部来源的统一绝对闭区间界。
	AccessLo int64
	AccessHi int64
	// ExternalBase 标记基址是否经由外部输入（仅在已收窄且可证时为真）。
	ExternalBase bool
	// CheckSites 是使索引/基址收窄的显式边界检查站点。
	CheckSites []string
}

// LoopCheck 是对单个循环头给定不变量的保持性验证结论。
type LoopCheck struct {
	Header     BlockID
	Holds      bool
	CheckedIn  []BlockID // 已核对的入边来源块（含循环前置与回边）
	Violations []string
}

// Report 是一次分析的完整结论。
type Report struct {
	OK           bool
	Certificates []Certificate
	Violations   []Violation
	LoopChecks   []LoopCheck
}
