package main

import "fmt"

// VerifyEquivalence 用朴素历史查询验证压实前后等价：
// 对每个键、每个序号 seq（oldestSnapshot <= seq <= maxSeq），
// NaiveQuery 在 before 与 after 两组段上的结果必须完全一致。
// seq < oldestSnapshot 的历史允许不同——那些快照已经不存在了。
func VerifyEquivalence(before, after []*Segment, oldestSnapshot uint64) error {
	hi := maxSeq(before)
	if m := maxSeq(after); m > hi {
		hi = m
	}
	keys := allKeys(before)
	keys = append(keys, allKeys(after)...)
	keys = dedup(keys)

	for seq := oldestSnapshot; seq <= hi; seq++ {
		for _, k := range keys {
			vb, fb := NaiveQuery(before, k, seq)
			va, fa := NaiveQuery(after, k, seq)
			if vb != va || fb != fa {
				return fmt.Errorf("不等价: key=%q seq=%d 压实前=(%q,%v) 压实后=(%q,%v)",
					k, seq, vb, fb, va, fa)
			}
		}
	}
	return nil
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sortStrings(out)
	return out
}
