package netsec

import "errors"

// 输入证据中存在结构性错误时由 Analyze 返回。
var (
	// ErrNilInput 表示引用图、区段表或位图三者中存在空输入。
	ErrNilInput = errors.New("netsec: reference graph, segment table and allocation bitmap are required")
	// ErrDuplicateObject 表示区段表中出现重复的对象引用（对象号相同即视为重复，
	// 因为一个对象号在任意时刻只能对应一个代次）。
	ErrDuplicateObject = errors.New("netsec: duplicate object in segment table")
	// ErrInvalidExtent 表示区段长度为零或为负。
	ErrInvalidExtent = errors.New("netsec: extent length must be positive")
)

// ObjectRef 是对象的精确身份：对象号加上代次。
//
// 对象号可以在旧对象删除后被重新分配给新对象，因此仅靠对象号无法区分
// “指向当前代次对象的合法目录项”与“指向已删除旧代次的悬空目录项”。
// 目录项携带的代次必须与区段表中当前对象的代次相等才视为有效引用。
type ObjectRef struct {
	ID         uint64
	Generation uint64
}

// String 返回便于日志和报告阅读的“对象号.代次”表示。
func (r ObjectRef) String() string {
	return itoa(r.ID) + "." + itoa(r.Generation)
}

// Extent 表示从 StartBlock 开始、连续 Length 个块的区段。
type Extent struct {
	StartBlock uint64
	Length     uint64
}

// Blocks 返回该区段覆盖的块号；Length 为 0 时返回空切片。
func (e Extent) Blocks() []uint64 {
	if e.Length == 0 {
		return nil
	}
	blocks := make([]uint64, 0, e.Length)
	for block := e.StartBlock; block < e.StartBlock+e.Length; block++ {
		blocks = append(blocks, block)
	}
	return blocks
}

// Object 是区段表中的一个当前对象及其区段清单。
type Object struct {
	Ref     ObjectRef
	Extents []Extent
	// ShareGroup 为受信共享组标识。空字符串表示私有对象：
	// 私有对象的任何块都不得与其它对象共享。
	ShareGroup string
}

// SegmentTable 是全设备当前对象的区段表。
type SegmentTable struct {
	Objects []Object
}

// DirEntry 是目录中的一个引用项。
type DirEntry struct {
	Name   string
	Target ObjectRef
	// IsDir 标记目标是否为目录，仅用于报告语义，不影响可达性判定。
	IsDir bool
}

// ReferenceGraph 描述目录引用关系。Root 是根目录对象的精确引用；
// Edges 以父对象号为键，值为该目录下的全部目录项。
//
// 目录项只携带对象号与代次，不携带块信息，因此硬链接产生的多个目录项
// 指向同一个对象，其区段只会在区段表中被计算一次。
type ReferenceGraph struct {
	Root  ObjectRef
	Edges map[uint64][]DirEntry
}

// AllocationBitmap 是分配位图的最小接口，宿主可由真实位图适配实现。
type AllocationBitmap interface {
	// TotalBlocks 返回设备块总数，合法块号区间为 [0, TotalBlocks())。
	TotalBlocks() uint64
	// IsAllocated 报告给定块号当前是否标记为已分配；
	// 对越界块号必须返回 false。
	IsAllocated(block uint64) bool
}

// Bitmap 是 AllocationBitmap 的内存实现，供宿主和测试直接使用。
// 索引即块号，true 表示已分配。
type Bitmap []bool

// TotalBlocks 返回位图覆盖的块数。
func (b Bitmap) TotalBlocks() uint64 {
	return uint64(len(b))
}

// IsAllocated 报告块是否已分配，越界块号返回 false。
func (b Bitmap) IsAllocated(block uint64) bool {
	if block >= uint64(len(b)) {
		return false
	}
	return b[block]
}
