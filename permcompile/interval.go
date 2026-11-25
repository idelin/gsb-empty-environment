package permcompile

import "strconv"

// Interval 是左闭右开的 64 位区间 [Lo, Hi)。
// 字节配额直接用字节坐标；读取次数配额用计数坐标 [0, MaxReads)。
type Interval struct {
	Lo uint64
	Hi uint64
}

// IntervalSet 是互不相交、按 Lo 升序排列的区间集合。
// 所有配额都以“尚未消费的 64 位区间”表示，天然支持部分读取与精确扣减。
type IntervalSet []Interval

// newIntervalSet 构造单区间集合。
func newIntervalSet(lo, hi uint64) IntervalSet {
	if hi <= lo {
		return IntervalSet{}
	}
	return IntervalSet{{Lo: lo, Hi: hi}}
}

// containsRange 报告集合是否完整覆盖 [lo, hi)。
func (s IntervalSet) containsRange(lo, hi uint64) bool {
	if hi <= lo {
		return true
	}
	cursor := lo
	for _, in := range s {
		if in.Hi <= cursor {
			continue
		}
		if in.Lo > cursor {
			return false
		}
		cursor = in.Hi
		if cursor >= hi {
			return true
		}
	}
	return cursor >= hi
}

// empty 报告集合是否已耗尽。
func (s IntervalSet) empty() bool { return len(s) == 0 }

// carve 返回从集合中精确移除 [lo, hi) 后的新集合；原集合不被修改。
// 若集合未完整覆盖待移除区间，ok 为 false，调用方必须拒绝本次消费。
func (s IntervalSet) carve(lo, hi uint64) (out IntervalSet, ok bool) {
	if hi <= lo {
		return append(IntervalSet{}, s...), true
	}
	if !s.containsRange(lo, hi) {
		return nil, false
	}
	// 显式分配新底层数组：调用方可能从共享副本中演化多个可能世界，
	// 绝不能就地改写原 IntervalSet 的后备数组。
	out = make(IntervalSet, 0, len(s)+1)
	for _, in := range s {
		if in.Hi <= lo || in.Lo >= hi {
			out = append(out, in)
			continue
		}
		// 相交：保留被切区间落在移除范围之外的左右两段。
		if in.Lo < lo {
			out = append(out, Interval{Lo: in.Lo, Hi: lo})
		}
		if in.Hi > hi {
			out = append(out, Interval{Lo: hi, Hi: in.Hi})
		}
	}
	return out, true
}

// key 生成确定性字符串键，用于世界去重。
func (s IntervalSet) key() string {
	if len(s) == 0 {
		return "E"
	}
	var b []byte
	for _, in := range s {
		b = append(b, '[')
		b = strconv.AppendUint(b, in.Lo, 10)
		b = append(b, ':')
		b = strconv.AppendUint(b, in.Hi, 10)
		b = append(b, ']')
	}
	return string(b)
}
