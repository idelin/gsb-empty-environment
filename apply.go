package offlinecli

import (
	"errors"
	"fmt"
	"math"
)

// Apply 按顺序应用一条补丁操作。
//
// 零长操作直接跳过；任何长度算术溢出返回 ErrLengthOverflow；
// COPY 来源越界返回 ErrOutOfBounds；COPY 已生成区引用尚未写出的位置
// 返回 ErrIllegalForwardReference。
func (c *Checker) Apply(op Op) error {
	idx := c.nextOpIndex
	c.nextOpIndex++

	if op.Len == 0 {
		// 零长操作不产生任何字节，跳过其余校验（含前向引用校验）。
		if op.Type == OpAdd && len(op.Data) > 0 {
			return fmt.Errorf("add op carries %d data bytes but length is zero: %w", len(op.Data), ErrMalformedPatch)
		}
		return nil
	}

	switch op.Type {
	case OpCopyOriginal:
		if err := checkSpan(op.Src, op.Len, c.origSize); err != nil {
			return err
		}
		if err := checkDst(c, op.Dst, op.Len); err != nil {
			return err
		}
		c.overwriteAt(op.Dst, op.Len, idx, segment{
			kind: srcOriginal, src: op.Src, opIndex: idx, op: &op,
		})
	case OpCopyGenerated:
		// 源必须完全落在“当前已经写出”的输出区内。
		// 允许与目标向前重叠（memmove 语义），但禁止引用未生成的位置。
		if err := checkSpan(op.Src, op.Len, c.outSize); err != nil {
			if errors.Is(err, ErrLengthOverflow) {
				return err
			}
			return fmt.Errorf("generated copy references unwritten output at src=%d len=%d (output size=%d): %w",
				op.Src, op.Len, c.outSize, ErrIllegalForwardReference)
		}
		if err := checkDst(c, op.Dst, op.Len); err != nil {
			return err
		}
		c.overwriteAt(op.Dst, op.Len, idx, segment{
			kind: srcGenerated, src: op.Src, opIndex: idx, op: &op,
		})
	case OpAdd:
		if op.Len != uint64(len(op.Data)) {
			return fmt.Errorf("add op length=%d but data=%d bytes: %w", op.Len, len(op.Data), ErrMalformedPatch)
		}
		// ADD 只能追加在输出尾部。
		tail := c.outSize
		if op.Dst != tail {
			return fmt.Errorf("add op must append at tail=%d, got dst=%d: %w", c.outSize, op.Dst, ErrMalformedPatch)
		}
		if err := checkDst(c, tail, op.Len); err != nil {
			return err
		}
		c.overwriteAt(tail, op.Len, idx, segment{
			kind: srcAdd, src: 0, opIndex: idx, op: &op,
		})
	default:
		return fmt.Errorf("unknown op type %d: %w", op.Type, ErrMalformedPatch)
	}
	return nil
}

// ApplyPatch 顺序应用整个补丁，首条出错即返回。
func (c *Checker) ApplyPatch(ops []Op) error {
	for i, op := range ops {
		if err := c.Apply(op); err != nil {
			return fmt.Errorf("apply op %d: %w", i, err)
		}
	}
	return nil
}

func checkSpan(start, length, bound uint64) error {
	end, ok := addU64(start, length)
	if !ok {
		return fmt.Errorf("span [%d, +%d) overflows uint64: %w", start, length, ErrLengthOverflow)
	}
	if end > bound {
		return fmt.Errorf("span [%d, %d) exceeds bound %d: %w", start, end, bound, ErrOutOfBounds)
	}
	return nil
}

// checkDst 校验目标区间 [dst, dst+length)，并在其延伸输出尾部时推进 outSize。
func checkDst(c *Checker, dst, length uint64) error {
	end, ok := addU64(dst, length)
	if !ok {
		return fmt.Errorf("destination [%d, +%d) overflows uint64: %w", dst, length, ErrLengthOverflow)
	}
	newEnd, ok := addU64(c.outSize, length)
	if !ok {
		return fmt.Errorf("output size %d + %d overflows uint64: %w", c.outSize, length, ErrLengthOverflow)
	}
	// 允许 dst <= outSize 且 end <= outSize+length，即仅能覆盖已生成区或紧贴尾部扩展，
	// 不允许跳过空洞写出。
	if dst > c.outSize || end > newEnd {
		return fmt.Errorf("destination [%d, %d) leaves a gap beyond output size %d: %w", dst, end, c.outSize, ErrOutOfBounds)
	}
	if end > c.outSize {
		c.outSize = end
	}
	return nil
}

func addU64(a, b uint64) (uint64, bool) {
	if a > math.MaxUint64-b {
		return 0, false
	}
	return a + b, true
}

// segmentAt 返回当前输出偏移 off 处仍然有效的段。
func (c *Checker) segmentAt(off uint64) (int, error) {
	return c.segmentAtAsOf(off, c.nextOpIndex)
}

// segmentAtAsOf 返回在操作序号 asOf（不含）时刻负责输出偏移 off 的段。
// 段按出生序号追加，覆盖时只裁剪旧区间；同一偏移可能存在多层历史，
// 出生序号最大且早于 asOf 的层即为该时刻的可见版本。
func (c *Checker) segmentAtAsOf(off uint64, asOf int) (int, error) {
	best := -1
	for i := range c.segments {
		s := &c.segments[i]
		if s.start <= off && off < s.end && s.bornOp < asOf && asOf <= s.deadOp &&
			(best == -1 || s.bornOp > c.segments[best].bornOp) {
			best = i
		}
	}
	if best == -1 {
		return 0, fmt.Errorf("offset %d is outside generated output size %d: %w", off, c.outSize, ErrOutOfBounds)
	}
	return best, nil
}

// overwriteAt 在操作 idx 时刻用新来源覆盖输出区间 [dst, dst+length)。
// 被覆盖的旧层仅裁剪区间或标记失效，保留其历史快照供重叠复制回溯。
func (c *Checker) overwriteAt(dst, length uint64, idx int, seg segment) {
	end := dst + length // 调用方已保证不溢出

	// 与覆盖区间相交的有效层：重叠部分保留为带 deadOp 的死亡历史层，
	// 非重叠的左右两侧保留为存活碎片（均沿用原 bornOp，历史快照仍指向
	// 正确的来源操作）。完整覆盖时只剩死亡历史层。
	kept := make([]segment, 0, len(c.segments)*2)
	for i := range c.segments {
		s := c.segments[i]
		if s.end <= dst || s.start >= end {
			kept = append(kept, s)
			continue
		}
		overlapLo := maxU64(s.start, dst)
		overHi := minU64(s.end, end)
		// 被覆盖的重叠部分作为死亡历史层保留：它在 bornOp..idx 时刻可见，
		// 使自重叠 memmove 能读到复制发生前的旧来源。
		if overlapLo < overHi {
			dead := s
			dead.start, dead.end = overlapLo, overHi
			dead.deadOp = idx
			kept = append(kept, dead)
		}

		if s.start < overlapLo {
			left := s
			left.end = overlapLo
			kept = append(kept, left)
		}
		if overHi < s.end {
			right := s
			right.start = overHi
			kept = append(kept, right)
		}
	}

	seg.start, seg.end = dst, end
	seg.bornOp = idx
	seg.deadOp = aliveSegment
	// 段需按 start 有序，便于后续按偏移定位；在正确位置插入新段。
	pos := len(kept)
	for i := range kept {
		if kept[i].start >= dst {
			pos = i
			break
		}
	}
	kept = append(kept, segment{})
	copy(kept[pos+1:], kept[pos:])
	kept[pos] = seg
	c.segments = kept
}
