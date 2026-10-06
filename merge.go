// Package merge 实现多源记录的字段级裁决合并。
//
// 每个字段候选携带来源优先级（Priority）、有效区间（[From, To)，零值表示无界）
// 与入库序号（Seq）。给定查询时间，先按有效区间过滤，再逐字段裁决：
// 优先级高者胜出；优先级相同则入库序号大者胜出；同优先级同序号而取值不同
// 则判定冲突（Conflict），并按确定性规则选出代表值。缺失（Missing）候选
// 不参与裁决，因而不会覆盖显式 null。每个候选的处置结果都会在解释中保留。
package merge

import (
	"fmt"
	"reflect"
	"sort"
	"time"
)

// Kind 区分字段取值的三种状态。
type Kind int

const (
	// Missing 表示来源记录中不存在该字段。
	Missing Kind = iota
	// Null 表示来源显式给出了 null。
	Null
	// Val 表示来源给出了具体值。
	Val
)

// Value 是带三态语义的字段取值。
type Value struct {
	Kind Kind
	V    interface{}
}

// ValueOf 构造具体值。
func ValueOf(v interface{}) Value { return Value{Kind: Val, V: v} }

// NullValue 构造显式 null。
func NullValue() Value { return Value{Kind: Null} }

// MissingValue 构造缺失值。
func MissingValue() Value { return Value{Kind: Missing} }

// Equal 比较两个取值是否语义相等。
func (a Value) Equal(b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind != Val {
		return true
	}
	return reflect.DeepEqual(a.V, b.V)
}

// canonical 返回用于确定性排序的规范字符串。
func (a Value) canonical() string {
	switch a.Kind {
	case Missing:
		return "\x00missing"
	case Null:
		return "\x00null"
	}
	return fmt.Sprintf("%#v", a.V)
}

// Candidate 是某个来源对某字段的一次取值申报。
type Candidate struct {
	Field    string
	Source   string
	Priority int       // 来源优先级，越大越权威
	Seq      uint64    // 入库序号，同优先级时大者胜出
	From     time.Time // 有效区间下界（含），零值表示无下界
	To       time.Time // 有效区间上界（不含），零值表示无上界
	Value    Value
}

// validAt 判断候选在查询时间 t 是否处于有效区间 [From, To) 内。
func (c Candidate) validAt(t time.Time) bool {
	if !c.From.IsZero() && t.Before(c.From) {
		return false
	}
	if !c.To.IsZero() && !t.Before(c.To) {
		return false
	}
	return true
}

// Disposition 是候选在裁决后的处置结果。
type Disposition int

const (
	// Winner 胜出且同组无分歧。
	Winner Disposition = iota
	// CoWinner 与胜出值完全一致的并列候选。
	CoWinner
	// ConflictWinner 冲突组中被确定性规则选中的代表。
	ConflictWinner
	// ConflictLoser 冲突组中未被选中的候选。
	ConflictLoser
	// Superseded 因优先级或入库序号落后而出局。
	Superseded
	// OutOfWindow 查询时间不在有效区间内，被过滤。
	OutOfWindow
	// IgnoredMissing 缺失候选，不参与裁决（不会覆盖显式 null）。
	IgnoredMissing
)

// Explained 是候选及其处置解释。
type Explained struct {
	Candidate   Candidate
	Disposition Disposition
	Note        string
}

// FieldResult 是单个字段的裁决结果。
type FieldResult struct {
	Field      string
	Value      Value       // 胜出取值；无任何有效候选时为 Missing
	Conflict   bool        // 同优先级同序号组内是否存在异值
	Candidates []Explained // 完整候选解释，顺序与输入一致
}

// Result 是一次合并的完整输出。
type Result struct {
	Time   time.Time
	Fields []FieldResult // 按字段名排序
}

// compare 比较两个候选的裁决顺位：优先级降序，其次入库序号降序。
// 返回正数表示 a 更优，负数表示 b 更优，0 表示并列。
func compare(a, b Candidate) int {
	if a.Priority != b.Priority {
		if a.Priority > b.Priority {
			return 1
		}
		return -1
	}
	if a.Seq != b.Seq {
		if a.Seq > b.Seq {
			return 1
		}
		return -1
	}
	return 0
}

// decide 在并列组（同优先级同序号）中做最终判定，shared by 两种实现。
// 返回胜出值与是否冲突，并写入组内各候选的处置结果。
func decide(top []int, cands []Candidate, disps []Disposition) (Value, bool) {
	if len(top) == 0 {
		return MissingValue(), false
	}
	// 确定性代表选择：来源名升序，再按取值规范串升序。
	sorted := append([]int(nil), top...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := cands[sorted[i]], cands[sorted[j]]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Value.canonical() < b.Value.canonical()
	})
	win := sorted[0]
	wv := cands[win].Value
	conflict := false
	for _, j := range sorted[1:] {
		if !cands[j].Value.Equal(wv) {
			conflict = true
			break
		}
	}
	if conflict {
		disps[win] = ConflictWinner
		for _, j := range sorted[1:] {
			disps[j] = ConflictLoser
		}
	} else {
		disps[win] = Winner
		for _, j := range sorted[1:] {
			disps[j] = CoWinner
		}
	}
	return wv, conflict
}

// noteFor 生成人类可读的处置说明。
func noteFor(d Disposition, c Candidate, cands []Candidate, top []int) string {
	switch d {
	case Winner:
		return "胜出"
	case CoWinner:
		return "与胜出值一致的并列候选"
	case ConflictWinner:
		return "同优先级同序号异值冲突，按确定性规则选为代表"
	case ConflictLoser:
		return "同优先级同序号异值冲突，未被选为代表"
	case Superseded:
		if len(top) > 0 {
			w := cands[top[0]]
			return fmt.Sprintf("被优先级 %d / 序号 %d 的候选压制", w.Priority, w.Seq)
		}
		return "被更优候选压制"
	case OutOfWindow:
		return "查询时间不在有效区间内"
	case IgnoredMissing:
		return "缺失候选不参与裁决"
	}
	return ""
}

// groupByField 按字段分组，返回排序后的字段名与字段到输入下标的映射。
func groupByField(cands []Candidate) ([]string, map[string][]int) {
	idx := make(map[string][]int)
	for i, c := range cands {
		idx[c.Field] = append(idx[c.Field], i)
	}
	fields := make([]string, 0, len(idx))
	for f := range idx {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	return fields, idx
}

// buildFieldResult 由处置数组组装字段结果。
func buildFieldResult(field string, cands []Candidate, disps []Disposition, top []int, val Value, conflict bool) FieldResult {
	ex := make([]Explained, len(cands))
	for i, c := range cands {
		ex[i] = Explained{Candidate: c, Disposition: disps[i], Note: noteFor(disps[i], c, cands, top)}
	}
	return FieldResult{Field: field, Value: val, Conflict: conflict, Candidates: ex}
}

// Adjudicate 是单遍选择实现：不为每个字段做全量排序，
// 而是在一次扫描中维护当前最优（优先级, 序号）及其并列组。
func Adjudicate(cands []Candidate, t time.Time) Result {
	fields, idx := groupByField(cands)
	res := Result{Time: t, Fields: make([]FieldResult, 0, len(fields))}
	for _, f := range fields {
		sub := make([]Candidate, len(idx[f]))
		for i, j := range idx[f] {
			sub[i] = cands[j]
		}
		disps := make([]Disposition, len(sub))
		var top []int
		for i, c := range sub {
			switch {
			case !c.validAt(t):
				disps[i] = OutOfWindow
			case c.Value.Kind == Missing:
				disps[i] = IgnoredMissing
			default:
				if len(top) == 0 || compare(c, sub[top[0]]) > 0 {
					for _, j := range top {
						disps[j] = Superseded
					}
					top = []int{i}
				} else if compare(c, sub[top[0]]) == 0 {
					top = append(top, i)
				} else {
					disps[i] = Superseded
				}
			}
		}
		val, conflict := decide(top, sub, disps)
		res.Fields = append(res.Fields, buildFieldResult(f, sub, disps, top, val, conflict))
	}
	return res
}
