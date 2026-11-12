package netsec

// FindingKind 标识一致性发现的类别。
type FindingKind string

const (
	// KindStaleEntry 表示目录项的对象号存在，但代次与当前对象不符，
	// 即该引用指向对象号代次复用之前的旧对象。
	KindStaleEntry FindingKind = "stale_generation_reference"

	// KindUnreachableObject 表示对象存在于区段表中，但从根目录出发
	// 不存在任何指向其当前代次的目录路径。
	KindUnreachableObject FindingKind = "unreachable_object"

	// KindReachableUnallocated 表示数据块可由可达对象的区段到达，
	// 但分配位图中该块并未标记为已分配。
	KindReachableUnallocated FindingKind = "reachable_unallocated_block"

	// KindAllocatedUnreferenced 表示数据块在位图中标记为已分配，
	// 但区段表中没有任何对象引用该块（可能被目录隐藏或泄漏）。
	KindAllocatedUnreferenced FindingKind = "allocated_unreferenced_block"

	// KindIllegalOverlap 表示同一块被多个对象引用，而这些对象并非全部
	// 属于同一个非空受信共享组（含私有对象之间或跨组共享）。
	KindIllegalOverlap FindingKind = "illegal_overlap"
)

// PathNode 是最短引用路径上的一跳，记录目录项名称与被指向的对象。
type PathNode struct {
	Name   string
	Object ObjectRef
}

// Finding 是单条一致性发现。
//
// 各类别对字段的使用约定：
//   - KindStaleEntry：Path 为悬空目录项所在的最短路径，Block 为 0 且
//     HasBlock 为 false；
//   - KindUnreachableObject：Object 为不可达对象，Path 为空；
//   - KindReachableUnallocated / KindAllocatedUnreferenced：Block、
//     HasBlock 有效；可达类发现的 Path 为最短引用路径，Object 为路径
//     末端持有该块的对象；无引用类发现 Path 为空、HasObject 为 false；
//   - KindIllegalOverlap：Block、HasBlock 有效，Owners 为全部持有者，
//     Path 与 Object 取排序后的第一个持有者（同样保留最短路径）。
type Finding struct {
	Kind      FindingKind
	Block     uint64
	HasBlock  bool
	Object    ObjectRef
	HasObject bool
	// EntryName 记录产生问题的目录项名称，供宿主在路径之外定位目录。
	EntryName string
	// ShareGroup 是非法重叠首个持有者的共享组，便于报告解释。
	ShareGroup string
	// Owners 仅在 KindIllegalOverlap 时使用，列出该块的全部持有者。
	Owners []ObjectRef
	// Path 是从根对象（不含根本身）到相关对象的最短引用路径。
	Path []PathNode
	// Detail 是面向取证操作员的中文解释。
	Detail string
}

// ShortestPath 返回该发现保留的最短引用路径；无路径时返回 nil。
func (f Finding) ShortestPath() []PathNode {
	return f.Path
}

// Report 是一次一致性分析的完整结果。
type Report struct {
	// ReachableObjects 是从根目录可达的当前代次对象集合，以对象号为键。
	ReachableObjects map[uint64]ObjectRef
	// Findings 按类别、块号、对象号确定性排序，便于宿主比对与归档。
	Findings []Finding
}

// ByKind 返回报告中指定类别的全部发现。
func (r *Report) ByKind(kind FindingKind) []Finding {
	if r == nil {
		return nil
	}
	out := make([]Finding, 0)
	for _, finding := range r.Findings {
		if finding.Kind == kind {
			out = append(out, finding)
		}
	}
	return out
}

// PathString 将最短引用路径渲染为 “/dir/file(对象号.代次)” 形式，
// 供日志和取证报告使用；空路径返回 “/”。
func PathString(path []PathNode) string {
	if len(path) == 0 {
		return "/"
	}
	out := ""
	for _, node := range path {
		out += "/" + node.Name + "(" + node.Object.String() + ")"
	}
	return out
}

// itoa 避免引入 strconv，保持包内轻量。
func itoa(value uint64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}
