package offlinecli

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// OpType 标识补丁中的一条操作。
type OpType uint8

const (
	OpCopyOriginal  OpType = 1 // 从原始镜像复制
	OpCopyGenerated OpType = 2 // 从已生成输出区复制（允许向前重叠）
	OpAdd           OpType = 3 // 追加新增数据
)

// Op 是补丁中的一条操作。
//
//   - CopyOriginal: Src/Len 指向原镜像，Dst 为目标输出偏移。
//   - CopyGenerated: Src/Len 指向当前已生成输出区，Dst 为目标输出偏移。
//   - Add: Data 为新增字节，追加到输出尾部，Dst/Len 由应用器推导。
type Op struct {
	Type OpType
	Src  uint64
	Dst  uint64
	Len  uint64
	Data []byte
}

// Range 是一个半开区间 [Start, End)。
type Range struct {
	Start uint64
	End   uint64
}

// 来源种类：段要么直接指向原镜像 / 新增数据，要么指向此前已写出的输出字节。
const (
	srcOriginal  = 0
	srcAdd       = 1
	srcGenerated = 2
)

// aliveSegment 是 segment.deadOp 的哨兵值，表示该层当前仍然有效。
const aliveSegment = math.MaxInt

// segment 是来源历史中的一个不可变层：在出生操作 bornOp 之后，区间
// [start,end) 内的字节由它描述；被后续操作覆盖的部分会以新区间裁剪失效，
// 而不是原地改写。这样自重叠 memmove 仍能读到“复制发生时刻”的旧来源，
// 回溯链不会成环。
type segment struct {
	start, end uint64
	kind       int
	src        uint64
	opIndex    int
	bornOp     int
	deadOp     int // math.MaxInt 表示仍然有效
	op         *Op
}

// Hop 是回溯链上的一跳，描述某个输出偏移处的字节由哪条操作产生。
type Hop struct {
	OpIndex int
	Op      *Op
	Offset  uint64 // 当前输出偏移（末跳为叶子来源内的偏移）
	SrcKind int    // 末跳来源：srcOriginal / srcAdd；中间跳为 srcGenerated
	Source  uint64 // 末跳在原镜像或新增数据中的偏移
}

// Checker 增量应用补丁并维护输出区的来源段。
type Checker struct {
	origSize    uint64
	trusted     []Range // 已合并、已裁剪到原镜像范围的可信原区
	segments    []segment
	outSize     uint64
	nextOpIndex int
}

// 哨兵错误，便于调用方用 errors.Is 精确判别。
var (
	ErrLengthOverflow            = errors.New("length arithmetic overflow")
	ErrIllegalForwardReference   = errors.New("illegal forward reference into unwritten output")
	ErrOutOfBounds               = errors.New("span exceeds its bound")
	ErrMalformedPatch            = errors.New("malformed patch")
	ErrProtectedRangeOutOfBounds = errors.New("protected range outside generated output")
)

// NewChecker 创建检查器。origSize 为原镜像字节数，trustedRanges 为原镜像中
// 被视为可信的区间（可为空，表示整个原镜像都不可信）。
func NewChecker(origSize uint64, trustedRanges []Range) (*Checker, error) {
	trusted, err := MergeRanges(trustedRanges)
	if err != nil {
		return nil, err
	}
	clamped := make([]Range, 0, len(trusted))
	for _, r := range trusted {
		if r.Start >= origSize {
			continue
		}
		end := r.End
		if end > origSize {
			end = origSize
		}
		clamped = append(clamped, Range{Start: r.Start, End: end})
	}
	return &Checker{origSize: origSize, trusted: clamped}, nil
}

// MergeRanges 校验、排序并合并相交或相邻的区间。
func MergeRanges(ranges []Range) ([]Range, error) {
	for _, r := range ranges {
		if r.Start > r.End {
			return nil, fmt.Errorf("invalid range [%d, %d): start > end", r.Start, r.End)
		}
	}
	if len(ranges) < 2 {
		out := make([]Range, len(ranges))
		copy(out, ranges)
		return out, nil
	}
	sorted := make([]Range, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	merged := []Range{sorted[0]}
	for _, r := range sorted[1:] {
		last := &merged[len(merged)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged, nil
}

// OutputSize 返回当前已生成输出的字节数。
func (c *Checker) OutputSize() uint64 { return c.outSize }
