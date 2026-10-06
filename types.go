package main

// Entry 是段里的一条记录：一次写入，或一个带序号的删除标记（墓碑）。
type Entry struct {
	Key       string
	Seq       uint64
	Value     string
	Tombstone bool
}

// Segment 是不可变段。Level 越大越新；[Start, End] 为闭区间键范围。
// Entries 按 (Key 升序, Seq 降序) 排序。
type Segment struct {
	ID      string
	Level   int
	Start   string
	End     string
	Entries []Entry
}

// Contains 报告键是否落在段的范围内。
func (s *Segment) Contains(key string) bool {
	return s.Start <= key && key <= s.End
}

// maxSeq 返回所有段里最大的序号，没有记录时返回 0。
func maxSeq(segs []*Segment) uint64 {
	var m uint64
	for _, s := range segs {
		for _, e := range s.Entries {
			if e.Seq > m {
				m = e.Seq
			}
		}
	}
	return m
}

// allKeys 收集所有段里出现过的键（去重、升序）。
func allKeys(segs []*Segment) []string {
	seen := map[string]bool{}
	var keys []string
	for _, s := range segs {
		for _, e := range s.Entries {
			if !seen[e.Key] {
				seen[e.Key] = true
				keys = append(keys, e.Key)
			}
		}
	}
	sortStrings(keys)
	return keys
}
