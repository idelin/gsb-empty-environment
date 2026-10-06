package main

// NaiveQuery 是朴素历史查询：在全量段集合上，找 key 的、序号 <= seq 的最新
// 一条记录；若该记录是墓碑或不存在，返回 found=false。它不做任何索引优化，
// 作为压实前后等价性验证的“事实标准”。
func NaiveQuery(segs []*Segment, key string, seq uint64) (value string, found bool) {
	var best *Entry
	for _, s := range segs {
		for i := range s.Entries {
			e := &s.Entries[i]
			if e.Key != key || e.Seq > seq {
				continue
			}
			if best == nil || e.Seq > best.Seq {
				best = e
			}
		}
	}
	if best == nil || best.Tombstone {
		return "", false
	}
	return best.Value, true
}
