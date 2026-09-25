package volmap

import "sort"

// Interval 把逻辑区间 [Start, End) 映射到不可变物理块 Block 内偏移 BOff 处。
// 同一视图内的区间互不重叠且按 Start 升序排列。
type Interval struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"` // 开区间
	Block uint64 `json:"blk"`
	BOff  uint64 `json:"boff"`
}

func (iv Interval) Len() uint64 { return iv.End - iv.Start }

// carve 返回把 [start, end) 从区间集合中挖掉后的结果，
// 边界处的区间被分裂，两侧保留（仍指向原物理块的对应偏移）。
func carve(ivs []Interval, start, end uint64) []Interval {
	out := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if iv.End <= start || iv.Start >= end {
			out = append(out, iv)
			continue
		}
		if iv.Start < start {
			out = append(out, Interval{Start: iv.Start, End: start, Block: iv.Block, BOff: iv.BOff})
		}
		if iv.End > end {
			out = append(out, Interval{Start: end, End: iv.End, Block: iv.Block, BOff: iv.BOff + (end - iv.Start)})
		}
	}
	return out
}

// insert 在已 carve 过的集合中插入新区间，并排序、合并相邻可合并区间。
func insert(ivs []Interval, nv Interval) []Interval {
	ivs = append(ivs, nv)
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].Start < ivs[j].Start })
	return merge(ivs)
}

// merge 合并逻辑上相邻、且指向同一物理块连续偏移的区间，保持不重叠不变式。
func merge(ivs []Interval) []Interval {
	if len(ivs) == 0 {
		return ivs
	}
	out := ivs[:1]
	for _, iv := range ivs[1:] {
		last := &out[len(out)-1]
		if last.End == iv.Start && last.Block == iv.Block && last.BOff+last.Len() == iv.BOff {
			last.End = iv.End
			continue
		}
		out = append(out, iv)
	}
	return out
}
