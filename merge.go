// Package merge 实现按来源优先级、有效区间与入库序号进行逐字段裁决的记录合并。
//
// 每条记录的每个字段携带若干候选值（Candidate），每个候选标注：
//   - Source   来源标识
//   - Priority 来源优先级（数值越大优先级越高）
//   - Seq      入库序号（数值越大表示越晚入库）
//   - ValidFrom/ValidTo 有效区间 [ValidFrom, ValidTo)，零值表示无界
//   - Null     显式 null（与“缺失”区分：缺失的候选根本不存在，不参与裁决）
//
// 裁决规则：按查询时间过滤有效候选后，逐字段比较 (Priority, Seq)，
// 取最大者；若最优 (Priority, Seq) 下存在多个不同值的候选，则报冲突。
// 缺失不覆盖显式 null：某来源未提供该字段时不产生候选，因此不会挤掉
// 其他来源的显式 null。
package merge

import (
	"fmt"
	"reflect"
	"sort"
	"time"
)

// Candidate 是某字段来自某一来源的一次取值。
type Candidate struct {
	Source    string
	Priority  int
	Seq       int64
	ValidFrom time.Time // 零值表示 -inf
	ValidTo   time.Time // 零值表示 +inf
	Null      bool      // 显式 null
	Value     interface{}
}

// Effective 报告候选在 t 时刻是否处于有效区间 [ValidFrom, ValidTo)。
func (c Candidate) Effective(t time.Time) bool {
	if !c.ValidFrom.IsZero() && t.Before(c.ValidFrom) {
		return false
	}
	if !c.ValidTo.IsZero() && !t.Before(c.ValidTo) {
		return false
	}
	return true
}

// key 返回用于排序比较的 (Priority, Seq)。
func (c Candidate) key() (int, int64) { return c.Priority, c.Seq }

// better 报告 a 的 (Priority, Seq) 是否严格优于 b。
func better(a, b Candidate) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	return a.Seq > b.Seq
}

// sameValue 比较两个候选的取值是否一致（显式 null 视为相等值）。
func sameValue(a, b Candidate) bool {
	if a.Null != b.Null {
		return false
	}
	if a.Null {
		return true
	}
	return reflect.DeepEqual(a.Value, b.Value)
}

// display 返回候选值的可读表示，用于解释输出。
func (c Candidate) display() string {
	if c.Null {
		return "null"
	}
	return fmt.Sprintf("%v", c.Value)
}

// Record 以字段名映射到该字段的全部候选。
type Record map[string][]Candidate

// Status 是字段裁决结果状态。
type Status int

const (
	// Empty 表示查询时间下没有任何有效候选。
	Empty Status = iota
	// Resolved 表示唯一最优候选胜出。
	Resolved
	// Conflict 表示最优 (Priority, Seq) 下存在多个不同取值。
	Conflict
)

func (s Status) String() string {
	switch s {
	case Empty:
		return "empty"
	case Resolved:
		return "resolved"
	case Conflict:
		return "conflict"
	}
	return "unknown"
}

// CandidateExplanation 记录单个候选在裁决中的命运。
type CandidateExplanation struct {
	Candidate Candidate
	Effective bool   // 是否通过查询时间过滤
	Reason    string // 未通过过滤或落败的原因；胜者为 "winner"
}

// Decision 是单字段的裁决结果与完整候选解释。
type Decision struct {
	Field      string
	Status     Status
	Winner     *Candidate // Status 为 Empty 时为 nil；Conflict 时为确定性兜底值
	Value      interface{}
	Null       bool
	Candidates []CandidateExplanation
}

// Result 是整条记录在查询时间 t 的合并结果。
type Result struct {
	Time      time.Time
	Decisions []Decision          // 按字段名排序
	Fields    map[string]Decision // 同一结果的索引
	Conflicts []string            // 发生冲突的字段名
}

// Merge 在查询时间 t 对记录逐字段裁决，返回含完整候选解释的结果。
func Merge(rec Record, t time.Time) Result {
	res := Result{Time: t, Fields: make(map[string]Decision, len(rec))}
	fields := make([]string, 0, len(rec))
	for f := range rec {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		d := decideField(f, rec[f], t)
		res.Decisions = append(res.Decisions, d)
		res.Fields[f] = d
		if d.Status == Conflict {
			res.Conflicts = append(res.Conflicts, f)
		}
	}
	return res
}

// decideField 对单字段执行过滤与裁决。
func decideField(field string, cands []Candidate, t time.Time) Decision {
	d := Decision{Field: field, Status: Empty}
	var alive []Candidate
	for _, c := range cands {
		ex := CandidateExplanation{Candidate: c}
		if !c.Effective(t) {
			ex.Reason = fmt.Sprintf("outside validity window at %s", t.Format(time.RFC3339Nano))
			d.Candidates = append(d.Candidates, ex)
			continue
		}
		ex.Effective = true
		alive = append(alive, c)
		d.Candidates = append(d.Candidates, ex)
	}
	if len(alive) == 0 {
		return d
	}

	// 找最优 (Priority, Seq)。
	best := alive[0]
	for _, c := range alive[1:] {
		if better(c, best) {
			best = c
		}
	}
	bp, bs := best.key()

	// 收集同优先同序的全部候选，检测异值冲突。
	var top []Candidate
	for _, c := range alive {
		if p, s := c.key(); p == bp && s == bs {
			top = append(top, c)
		}
	}
	conflict := false
	for _, c := range top[1:] {
		if !sameValue(c, top[0]) {
			conflict = true
			break
		}
	}

	// 同 (Priority, Seq) 并列时按 Source 字典序取最小者，保证输出稳定。
	winner := top[0]
	for _, c := range top[1:] {
		if c.Source < winner.Source {
			winner = c
		}
	}
	if conflict {
		d.Status = Conflict
	} else {
		d.Status = Resolved
	}
	d.Winner = &winner
	d.Value = winner.Value
	d.Null = winner.Null

	// 回填每个有效候选的落败/胜出原因。
	for i := range d.Candidates {
		ex := &d.Candidates[i]
		if !ex.Effective {
			continue
		}
		p, s := ex.Candidate.key()
		switch {
		case p == bp && s == bs:
			if conflict {
				if ex.Candidate.Source == winner.Source {
					ex.Reason = "winner (conflict: tie on priority/seq with different values, deterministic fallback by source)"
				} else {
					ex.Reason = "conflict: tie on priority/seq with different values"
				}
			} else {
				ex.Reason = "winner"
			}
		case p != bp:
			ex.Reason = fmt.Sprintf("lower priority (%d < %d)", p, bp)
		default:
			ex.Reason = fmt.Sprintf("older seq (%d < %d)", s, bs)
		}
	}
	return d
}
