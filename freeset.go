package pagealloc

import "sort"

// interval 表示一段连续空闲页 [start, start+count)。
type interval struct {
	start uint64
	count uint64
}

// freeSet 保存按起始页排序、两两不相邻（已合并）的空闲区间。
type freeSet struct {
	ivs []interval
}

func newFreeSet(total uint64) *freeSet {
	return &freeSet{ivs: []interval{{start: 0, count: total}}}
}

func (s *freeSet) clone() *freeSet {
	out := &freeSet{ivs: make([]interval, len(s.ivs))}
	copy(out.ivs, s.ivs)
	return out
}

// total 返回空闲页总数。
func (s *freeSet) total() uint64 {
	var n uint64
	for _, iv := range s.ivs {
		n += iv.count
	}
	return n
}

// containsRange 报告 [start, start+count) 是否完全空闲。
// 区间已合并，因此被覆盖的范围必然落在单个区间内。
func (s *freeSet) containsRange(start, count uint64) bool {
	i := sort.Search(len(s.ivs), func(i int) bool { return s.ivs[i].start+s.ivs[i].count > start })
	return i < len(s.ivs) && s.ivs[i].start <= start && start+count <= s.ivs[i].start+s.ivs[i].count
}

// overlaps 报告 [start, start+count) 是否与任何空闲区间相交。
func (s *freeSet) overlaps(start, count uint64) bool {
	i := sort.Search(len(s.ivs), func(i int) bool { return s.ivs[i].start+s.ivs[i].count > start })
	return i < len(s.ivs) && s.ivs[i].start < start+count
}

// alloc 按首次适配分配 n 个连续空闲页，返回起始页号。
func (s *freeSet) alloc(n uint64) (uint64, bool) {
	for _, iv := range s.ivs {
		if iv.count >= n {
			start := iv.start
			s.removeRange(start, n)
			return start, true
		}
	}
	return 0, false
}

// removeRange 从空闲集合中移除 [start, start+count)，调用前须保证其完全空闲。
func (s *freeSet) removeRange(start, count uint64) {
	i := sort.Search(len(s.ivs), func(i int) bool { return s.ivs[i].start+s.ivs[i].count > start })
	iv := s.ivs[i]
	end := start + count
	ivEnd := iv.start + iv.count
	switch {
	case iv.start == start && ivEnd == end:
		s.ivs = append(s.ivs[:i], s.ivs[i+1:]...)
	case iv.start == start:
		s.ivs[i].start = end
		s.ivs[i].count = ivEnd - end
	case ivEnd == end:
		s.ivs[i].count = start - iv.start
	default:
		right := interval{start: end, count: ivEnd - end}
		s.ivs[i].count = start - iv.start
		s.ivs = append(s.ivs, interval{})
		copy(s.ivs[i+2:], s.ivs[i+1:])
		s.ivs[i+1] = right
	}
}

// insert 把 [start, start+count) 放回空闲集合并与相邻区间合并，
// 调用前须保证该范围当前不空闲（不与现有区间相交）。
func (s *freeSet) insert(start, count uint64) {
	end := start + count
	i := sort.Search(len(s.ivs), func(i int) bool { return s.ivs[i].start+s.ivs[i].count >= start })
	// 与前驱合并
	if i < len(s.ivs) && s.ivs[i].start+s.ivs[i].count == start {
		s.ivs[i].count += count
		// 若后继也相邻，一并合并
		if i+1 < len(s.ivs) && s.ivs[i+1].start == end {
			s.ivs[i].count += s.ivs[i+1].count
			s.ivs = append(s.ivs[:i+1], s.ivs[i+2:]...)
		}
		return
	}
	// 仅与后继相邻
	if i < len(s.ivs) && s.ivs[i].start == end {
		s.ivs[i].start = start
		s.ivs[i].count += count
		return
	}
	s.ivs = append(s.ivs, interval{})
	copy(s.ivs[i+1:], s.ivs[i:])
	s.ivs[i] = interval{start: start, count: count}
}
