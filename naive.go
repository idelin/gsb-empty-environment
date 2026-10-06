package merge

import (
	"sort"
	"time"
)

// NaiveAdjudicate 是直接“筛选 + 排序”的参照模型，用于核对 Adjudicate：
// 先按有效区间与缺失规则过滤，再按（优先级降序、序号降序、来源升序、
// 取值规范串升序）全量排序，取队首的同（优先级, 序号）前缀作为并列组。
// 它与 Adjudicate 共享 decide 的冲突判定，差异仅在并列组的求法。
func NaiveAdjudicate(cands []Candidate, t time.Time) Result {
	fields, idx := groupByField(cands)
	res := Result{Time: t, Fields: make([]FieldResult, 0, len(fields))}
	for _, f := range fields {
		sub := make([]Candidate, len(idx[f]))
		for i, j := range idx[f] {
			sub[i] = cands[j]
		}
		disps := make([]Disposition, len(sub))
		alive := make([]int, 0, len(sub))
		for i, c := range sub {
			switch {
			case !c.validAt(t):
				disps[i] = OutOfWindow
			case c.Value.Kind == Missing:
				disps[i] = IgnoredMissing
			default:
				alive = append(alive, i)
			}
		}
		sort.Slice(alive, func(i, j int) bool {
			a, b := sub[alive[i]], sub[alive[j]]
			if cmp := compare(a, b); cmp != 0 {
				return cmp > 0
			}
			if a.Source != b.Source {
				return a.Source < b.Source
			}
			return a.Value.canonical() < b.Value.canonical()
		})
		var top []int
		for k, i := range alive {
			if k > 0 && compare(sub[i], sub[alive[0]]) != 0 {
				disps[i] = Superseded
				continue
			}
			top = append(top, i)
		}
		val, conflict := decide(top, sub, disps)
		res.Fields = append(res.Fields, buildFieldResult(f, sub, disps, top, val, conflict))
	}
	return res
}
