package netsec

import (
	"fmt"
	"sort"
)

// Range 是 MIME 叶节点原始字节流上的半开区间 [Start, End)。
// 偏移量相对于传入 MapPart 的原始字节切片，从 0 开始计数。
type Range struct {
	Start int64
	End   int64
}

// coverage 是归一化（排序、合并）后的已认证原始区间集合，
// 支持对单个原始位置做 O(log n) 的覆盖查询。
type coverage struct {
	ranges []Range
}

func newCoverage(authenticated []Range, rawLen int64) (coverage, error) {
	cp := append([]Range(nil), authenticated...)
	for _, r := range cp {
		if r.Start < 0 || r.End < r.Start || r.End > rawLen {
			return coverage{}, fmt.Errorf("netsec: authenticated raw range [%d,%d) out of bounds [0,%d)", r.Start, r.End, rawLen)
		}
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })

	merged := make([]Range, 0, len(cp))
	for _, r := range cp {
		if last := len(merged) - 1; last >= 0 && r.Start <= merged[last].End {
			if r.End > merged[last].End {
				merged[last].End = r.End
			}
			continue
		}
		merged = append(merged, r)
	}
	return coverage{ranges: merged}, nil
}

// covered 报告原始位置 pos 是否落在任一已认证区间内。
func (c coverage) covered(pos int64) bool {
	i := sort.Search(len(c.ranges), func(i int) bool { return c.ranges[i].End > pos })
	return i < len(c.ranges) && c.ranges[i].Start <= pos
}
