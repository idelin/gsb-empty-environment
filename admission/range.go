package admission

import (
	"sort"
)

// 页号为 48 位无符号整数，所有页区间均为闭区间。
const (
	PageBits  = 48
	PageCount = uint64(1) << PageBits
	MaxPage   = PageCount - 1
)

// Range 是闭区间页范围 [Start, End]。
type Range struct {
	Start uint64
	End   uint64 // 包含端点
}

// Valid 判断区间是否落在 48 位页空间内且端点有序。
func (r Range) Valid() bool {
	return r.Start <= r.End && r.End <= MaxPage
}

// Len 返回区间内的页数，调用前需保证 Valid。
func (r Range) Len() uint64 {
	return r.End - r.Start + 1
}

// Contains 判断页 p 是否属于该区间。
func (r Range) Contains(p uint64) bool {
	return r.Start <= p && p <= r.End
}

// Overlaps 判断两个区间是否有公共页。
func (r Range) Overlaps(o Range) bool {
	return r.Start <= o.End && o.Start <= r.End
}

// Intersect 返回两个区间的公共部分，无交集时 ok 为 false。
func (r Range) Intersect(o Range) (out Range, ok bool) {
	s := max(r.Start, o.Start)
	e := min(r.End, o.End)
	if s > e {
		return Range{}, false
	}
	return Range{Start: s, End: e}, true
}

// coalesceRanges 将若干区间排序并合并重叠/相邻者，返回精确的最小区间集合。
func coalesceRanges(rs []Range) []Range {
	if len(rs) == 0 {
		return nil
	}
	out := make([]Range, len(rs))
	copy(out, rs)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	merged := out[:1]
	for i := 1; i < len(out); i++ {
		last := &merged[len(merged)-1]
		if out[i].Start <= last.End {
			if out[i].End > last.End {
				last.End = out[i].End
			}
			continue
		}
		if out[i].Start == last.End+1 && last.End < MaxPage {
			last.End = out[i].End
			continue
		}
		merged = append(merged, out[i])
	}
	return merged
}

// validateShards 校验宿主提供的分片边界：分片必须有序、互不重叠、首尾相接，
// 且完整覆盖整个 48 位页空间 [0, MaxPage]。
func validateShards(shards []Range) bool {
	if len(shards) == 0 || !shards[0].Valid() || shards[0].Start != 0 {
		return false
	}
	for i, sh := range shards {
		if !sh.Valid() {
			return false
		}
		if i > 0 && sh.Start != shards[i-1].End+1 {
			return false
		}
	}
	return shards[len(shards)-1].End == MaxPage
}

// shardAt 返回包含页 p 的分片下标，调用前需保证分片已通过 validateShards。
func shardAt(shards []Range, p uint64) int {
	idx := sort.Search(len(shards), func(i int) bool {
		return shards[i].End >= p
	})
	if idx >= len(shards) {
		return len(shards) - 1
	}
	return idx
}

// alignedToShards 判断 r 是否由完整分片构成。若 r 完全落在单个分片内
// （宿主通常把整个小地址域放进一个分片），则天然对齐；跨分片时两端
// 必须分别是某分片的起点与终点。这是内容收据的强制要求。
func alignedToShards(r Range, shards []Range) bool {
	if !r.Valid() {
		return false
	}
	startShard := shardAt(shards, r.Start)
	endShard := shardAt(shards, r.End)
	if startShard == endShard {
		return true
	}
	return shards[startShard].Start == r.Start &&
		shards[endShard].End == r.End
}
