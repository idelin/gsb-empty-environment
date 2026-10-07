package merge

import (
	"sort"
	"time"
)

// referenceMerge 是直接排序筛选参照模型：对每个字段，先按查询时间过滤，
// 再按 (Priority 降序, Seq 降序, Source 升序) 整体排序，取首元素为胜者；
// 若与胜者同 (Priority, Seq) 的候选中存在异值，则判冲突。
// 它刻意独立于 Merge 的实现路径，用于交叉核对。
func referenceMerge(rec Record, t time.Time) Result {
	res := Result{Time: t, Fields: make(map[string]Decision, len(rec))}
	fields := make([]string, 0, len(rec))
	for f := range rec {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		d := Decision{Field: f, Status: Empty}
		var alive []Candidate
		for _, c := range rec[f] {
			if c.Effective(t) {
				alive = append(alive, c)
			}
		}
		if len(alive) > 0 {
			sort.SliceStable(alive, func(i, j int) bool {
				a, b := alive[i], alive[j]
				if a.Priority != b.Priority {
					return a.Priority > b.Priority
				}
				if a.Seq != b.Seq {
					return a.Seq > b.Seq
				}
				return a.Source < b.Source
			})
			winner := alive[0]
			conflict := false
			for _, c := range alive {
				if c.Priority == winner.Priority && c.Seq == winner.Seq {
					if !sameValue(c, winner) {
						conflict = true
					}
				} else {
					break
				}
			}
			if conflict {
				d.Status = Conflict
			} else {
				d.Status = Resolved
			}
			w := winner
			d.Winner = &w
			d.Value = winner.Value
			d.Null = winner.Null
		}
		res.Decisions = append(res.Decisions, d)
		res.Fields[f] = d
		if d.Status == Conflict {
			res.Conflicts = append(res.Conflicts, f)
		}
	}
	return res
}
