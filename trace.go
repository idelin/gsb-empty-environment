package offlinecli

import (
	"fmt"
	"math"
)

// ResolveChain 返回输出偏移 off 处字节从输出到叶子来源的完整回溯链。
// 链首是覆盖该偏移的那条操作，链尾是原镜像或 ADD 数据中的具体来源。
func (c *Checker) ResolveChain(off uint64) ([]Hop, error) {
	var hops []Hop
	cur := off
	asOf := c.nextOpIndex
	for {
		i, err := c.segmentAtAsOf(cur, asOf)
		if err != nil {
			return nil, err
		}
		seg := c.segments[i]
		switch seg.kind {
		case srcOriginal, srcAdd:
			hops = append(hops, Hop{
				OpIndex: seg.opIndex,
				Op:      seg.op,
				Offset:  cur,
				SrcKind: seg.kind,
				Source:  seg.src + (cur - seg.start),
			})
			return hops, nil
		case srcGenerated:
			hops = append(hops, Hop{
				OpIndex: seg.opIndex,
				Op:      seg.op,
				Offset:  cur,
				SrcKind: srcGenerated,
			})
			cur = seg.src + (cur - seg.start)
			// 父字节必须按本复制操作发生之前的快照解析。
			asOf = seg.bornOp
		default:
			return nil, fmt.Errorf("internal error: unknown segment kind %d", seg.kind)
		}
	}
}

// Finding 是保护区间内首个不可信字节及其完整回溯链。
type Finding struct {
	Offset uint64 // 输出区中的字节偏移
	Reason string
	Hops   []Hop
}

// Check 检查受保护输出区中的任一字节是否来源于不可信原区或新增数据，
// 返回首个问题字节的完整回溯链；全部可信时返回 nil。
// protectedRanges 会被排序合并，且必须完全落在已生成输出区内。
func (c *Checker) Check(protectedRanges []Range) (*Finding, error) {
	protected, err := MergeRanges(protectedRanges)
	if err != nil {
		return nil, err
	}
	for _, r := range protected {
		if r.End > c.outSize {
			return nil, fmt.Errorf("protected range [%d, %d) outside output size %d: %w",
				r.Start, r.End, c.outSize, ErrProtectedRangeOutOfBounds)
		}
		if r.Start == r.End {
			continue
		}
		off, reason, tainted, err := c.firstTaintedInOutput(r.Start, r.End)
		if err != nil {
			return nil, err
		}
		if tainted {
			chain, err := c.ResolveChain(off)
			if err != nil {
				return nil, err
			}
			return &Finding{Offset: off, Reason: reason, Hops: chain}, nil
		}
	}
	return nil, nil
}

// firstTaintedInOutput 在输出区间 [lo, hi) 内返回首个不可信字节。
func (c *Checker) firstTaintedInOutput(lo, hi uint64) (off uint64, reason string, tainted bool, err error) {
	return c.firstTaintedInOutputAsOf(lo, hi, c.nextOpIndex)
}

func (c *Checker) firstTaintedInOutputAsOf(lo, hi uint64, asOf int) (off uint64, reason string, tainted bool, err error) {
	cur := lo
	for cur < hi {
		idx, err := c.segmentAtAsOf(cur, asOf)
		if err != nil {
			return 0, "", false, err
		}
		seg := c.segments[idx]
		segLo := maxU64(seg.start, cur)
		segHi := minU64(seg.end, hi)
		localOff, reason, tainted, err := c.firstTaintedInSegment(seg, segLo, segHi)
		if err != nil {
			return 0, "", false, err
		}
		if tainted {
			return localOff, reason, true, nil
		}
		cur = segHi
	}
	return 0, "", false, nil
}

// firstTaintedInSegment 判断来源段覆盖的输出区间 [lo,hi) 内的首个不可信字节，
// 返回的偏移始终位于“调用方当前所在的坐标空间”（即 lo/hi 同一空间）。
func (c *Checker) firstTaintedInSegment(seg segment, lo, hi uint64) (off uint64, reason string, tainted bool, err error) {
	switch seg.kind {
	case srcOriginal:
		srcLo := seg.src + (lo - seg.start)
		srcHi := seg.src + (hi - seg.start)
		if u := firstUntrustedOriginal(c.trusted, srcLo, srcHi); u != math.MaxUint64 {
			return lo + (u - srcLo),
				fmt.Sprintf("byte traces to untrusted original image byte at offset %d", u),
				true, nil
		}
	case srcAdd:
		u := seg.src + (lo - seg.start)
		return lo,
			fmt.Sprintf("byte traces to added (untrusted) data at offset %d", u),
			true, nil
	case srcGenerated:
		// 映射回父输出区间（本操作之前的快照）继续递归。
		parentLo := seg.src + (lo - seg.start)
		parentHi := seg.src + (hi - seg.start)
		parentOff, reason, tainted, err := c.firstTaintedInOutputAsOf(parentLo, parentHi, seg.bornOp)
		if err != nil || !tainted {
			return 0, reason, tainted, err
		}
		// 父坐标再映射回本层坐标：本层与父区间逐字节平移。
		return lo + (parentOff - parentLo), reason, true, nil
	default:
		return 0, "", false, fmt.Errorf("internal error: unknown segment kind %d", seg.kind)
	}
	return 0, "", false, nil
}

// firstUntrustedOriginal 返回原镜像区间 [lo, hi) 内首个不在任何可信区间中的偏移；
// 全部可信时返回 math.MaxUint64。trusted 已排序合并。
func firstUntrustedOriginal(trusted []Range, lo, hi uint64) uint64 {
	cursor := lo
	for _, r := range trusted {
		if r.End <= cursor {
			continue
		}
		if r.Start > cursor {
			if r.Start >= hi {
				return cursor
			}
			return cursor // cursor 落在可信区间之前的空洞里
		}
		// r.Start <= cursor，当前游标被该可信区间覆盖
		if r.End >= hi {
			return math.MaxUint64
		}
		cursor = r.End
	}
	if cursor < hi {
		return cursor
	}
	return math.MaxUint64
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
