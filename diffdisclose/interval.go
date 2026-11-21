package diffdisclose

import "sort"

// Interval 是半开区间 [Lo, Hi)，Lo < Hi，边界均为 48 位键空间内的值。
type Interval struct {
	Lo uint64
	Hi uint64
}

// IntervalSet 是归一化后的互不相交、按起点升序排列的半开区间集合。
type IntervalSet struct {
	items []Interval
}

// NewIntervalSet 归一化输入区间：排序、校验并合并重叠或相邻区间。
func NewIntervalSet(ivs []Interval) (IntervalSet, error) {
	copied := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if iv.Lo >= iv.Hi {
			return IntervalSet{}, errBadInterval
		}
		copied = append(copied, iv)
	}
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].Lo != copied[j].Lo {
			return copied[i].Lo < copied[j].Lo
		}
		return copied[i].Hi < copied[j].Hi
	})
	merged := make([]Interval, 0, len(copied))
	for _, iv := range copied {
		if len(merged) == 0 || iv.Lo > merged[len(merged)-1].Hi {
			merged = append(merged, iv)
			continue
		}
		if iv.Hi > merged[len(merged)-1].Hi {
			merged[len(merged)-1].Hi = iv.Hi
		}
	}
	return IntervalSet{items: merged}, nil
}

// newRevocationSet 构造撤权集合：排序并校验，但不合并相邻区间，
// 因为每个撤权区间都必须保留为独立的对齐子树粒度。
func newRevocationSet(ivs []Interval) (IntervalSet, error) {
	sorted := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if iv.Lo >= iv.Hi {
			return IntervalSet{}, errBadInterval
		}
		sorted = append(sorted, iv)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Lo != sorted[j].Lo {
			return sorted[i].Lo < sorted[j].Lo
		}
		return sorted[i].Hi < sorted[j].Hi
	})
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Lo < sorted[i-1].Hi {
			return IntervalSet{}, errRevocationAligned
		}
	}
	return IntervalSet{items: sorted}, nil
}

// empty 判断集合是否为空。
func (s IntervalSet) empty() bool { return len(s.items) == 0 }

// contains 判断键是否落在集合内。
func (s IntervalSet) contains(key uint64) bool {
	idx := sort.Search(len(s.items), func(i int) bool { return s.items[i].Hi > key })
	return idx < len(s.items) && key >= s.items[idx].Lo
}

// cover 分类 [lo, hi) 与集合的关系：
//   - coverNone：完全在集合外；
//   - coverFull：完全被集合包含；
//   - coverPartial：跨越集合边界，需要继续下降。
func (s IntervalSet) cover(lo, hi uint64) coverKind {
	full := false
	for _, iv := range s.items {
		if iv.Hi <= lo {
			continue
		}
		if iv.Lo >= hi {
			break
		}
		// 存在交集。
		if iv.Lo <= lo && iv.Hi >= hi {
			full = true
			continue
		}
		return coverPartial
	}
	if full {
		return coverFull
	}
	return coverNone
}

type coverKind int

const (
	coverNone coverKind = iota
	coverFull
	coverPartial
)

// validateAligned 校验撤权区间集合中的每个区间都恰好对齐为一棵子树，
// 且完全落在授权集合内部。撤权只允许以整子树为最小粒度。
func validateRevocation(authorized, revoked IntervalSet, height int) error {
	spanMax := uint64(1) << height
	for _, iv := range revoked.items {
		width := iv.Hi - iv.Lo
		if width == 0 || width > spanMax || width&(width-1) != 0 {
			return errRevocationAligned
		}
		if iv.Lo%width != 0 || iv.Hi > spanMax {
			return errRevocationAligned
		}
		if authorized.cover(iv.Lo, iv.Hi) != coverFull {
			return errRevocationScope
		}
	}
	return nil
}
