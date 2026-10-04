// Package fieldauth 提供字段级更新授权库。
//
// 核心语义：
//   - 一次 Update 可携带多个字段赋值，引擎对整批修改做原子校验：
//     要么全部允许（调用方提交 Decision.New），要么整体拒绝，绝不部分生效。
//   - 每条规则的条件显式声明读取哪个快照：Old（提交前的已提交值）
//     或 New（本次更新后的待提交值）。授权判断读取 Old，一致性/约束
//     判断通常读取 New。
//   - 派生字段（Deriver）在校验前按依赖拓扑序自动重算，重算结果同样
//     参与规则检查——绕过派生字段等于绕过其守卫规则。
//   - 拒绝时给出最小拒绝解释：原始赋值集合的一个极小子集，该子集单独
//     提交仍会被拒绝，且再删掉任意一个赋值就不再被拒绝。
package fieldauth

import (
	"fmt"
	"reflect"
	"sort"
)

// View 表示条件读取的记录快照。
type View int

const (
	// Old 是更新前的已提交快照。授权类条件必须读取 Old，
	// 否则“先把自己改成管理员再改敏感字段”的批量更新会
	// 用未提交的新身份通过校验。
	Old View = iota
	// New 是应用本次更新（含派生重算）后的待提交快照，
	// 适合结果一致性、不变量类条件。
	New
)

func (v View) String() string {
	if v == Old {
		return "old"
	}
	return "new"
}

// Record 是一条记录的字段集合。
type Record map[string]interface{}

// Actor 是发起更新的主体，来自已认证的会话快照，
// 其属性始终是“已提交”状态，不受本次更新影响。
type Actor struct {
	ID    string
	Roles []string
}

// HasRole 报告主体是否拥有指定角色。
func (a Actor) HasRole(role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Cond 是一条条件，在声明的快照上求值。
type Cond struct {
	// View 决定 Func 收到的 rec 是旧快照还是新快照。
	View View
	// Desc 用于拒绝解释中的人类可读描述。
	Desc string
	// Func 返回 true 表示条件满足。rec 按 View 取值，actor 为会话主体。
	Func func(rec Record, actor Actor) bool
}

// Rule 守卫一个字段：该字段的值发生变化时，所有条件都必须满足。
type Rule struct {
	Field string
	Name  string
	Conds []Cond
}

// Deriver 声明派生字段的自动重算逻辑。
type Deriver struct {
	Field   string
	Depends []string
	// Recompute 基于当前（已合并显式赋值的）新快照计算派生值。
	Recompute func(rec Record) (interface{}, error)
}

// Update 是一次待校验的批量字段修改。
type Update struct {
	Set Record
}

// Violation 描述一条被违反的条件。
type Violation struct {
	Field string // 被守卫的字段
	Rule  string // 规则名
	Cond  string // 条件描述
	View  View   // 条件读取的快照
}

func (v Violation) String() string {
	return fmt.Sprintf("field %q: rule %q violated (%s, read %s snapshot)",
		v.Field, v.Rule, v.Cond, v.View)
}

// Explanation 是最小拒绝解释。
type Explanation struct {
	// Assignments 是 Update.Set 的极小子集：单独提交它仍被拒绝，
	// 且移除其中任意一个赋值后即变为可提交。
	Assignments Record
	// Violations 是仅提交 Assignments 时产生的违反项。
	Violations []Violation
}

// Decision 是一次原子校验的结果。
type Decision struct {
	Allowed bool
	// New 是合并显式赋值与派生重算后的完整新记录。
	// 仅当 Allowed 为 true 时才应被提交。
	New Record
	// Changed 是值发生变化的字段（含派生字段），按字典序。
	Changed []string
	// Violations 是完整更新被拒绝时的全部违反项。
	Violations []Violation
	// Minimal 是最小拒绝解释，仅在被拒绝时非 nil。
	Minimal *Explanation
}

// Engine 持有规则与派生器，执行原子校验。
type Engine struct {
	rules    map[string][]Rule
	derivers []Deriver // 已按依赖拓扑排序
}

// NewEngine 构建引擎；派生器依赖成环时返回错误。
func NewEngine(rules []Rule, derivers []Deriver) (*Engine, error) {
	sorted, err := topoSortDerivers(derivers)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		rules:    make(map[string][]Rule),
		derivers: sorted,
	}
	for _, r := range rules {
		e.rules[r.Field] = append(e.rules[r.Field], r)
	}
	return e, nil
}

// Check 对整批更新做原子校验，不修改 old 与 update。
func (e *Engine) Check(actor Actor, old Record, update Update) (Decision, error) {
	return e.check(actor, old, update.Set, true)
}

// check 是内部实现；wantExplain 控制拒绝时是否计算最小解释。
func (e *Engine) check(actor Actor, old Record, set Record, wantExplain bool) (Decision, error) {
	merged := mergeRecords(old, set)
	newRec, err := e.applyDerivers(merged)
	if err != nil {
		return Decision{}, err
	}
	changed := changedFields(old, newRec)
	var violations []Violation
	for _, field := range changed {
		for _, rule := range e.rules[field] {
			for _, cond := range rule.Conds {
				rec := newRec
				if cond.View == Old {
					rec = old
				}
				if !cond.Func(rec, actor) {
					violations = append(violations, Violation{
						Field: field,
						Rule:  rule.Name,
						Cond:  cond.Desc,
						View:  cond.View,
					})
				}
			}
		}
	}
	d := Decision{
		Allowed:    len(violations) == 0,
		New:        newRec,
		Changed:    changed,
		Violations: violations,
	}
	if d.Allowed || !wantExplain {
		return d, nil
	}
	minimal, err := e.minimalExplanation(actor, old, set)
	if err != nil {
		return Decision{}, err
	}
	d.Minimal = minimal
	return d, nil
}

// minimalExplanation 用删除法求 set 的极小被拒子集：
// 逐个尝试移除赋值，若移除后仍被拒绝则确认移除，
// 最终剩下的子集再删任意一个都会变为允许。
func (e *Engine) minimalExplanation(actor Actor, old Record, set Record) (*Explanation, error) {
	keep := mergeRecords(nil, set)
	for _, k := range sortedKeys(set) {
		if _, ok := keep[k]; !ok {
			continue
		}
		trial := mergeRecords(nil, keep)
		delete(trial, k)
		d, err := e.check(actor, old, trial, false)
		if err != nil {
			return nil, err
		}
		if !d.Allowed {
			keep = trial
		}
	}
	d, err := e.check(actor, old, keep, false)
	if err != nil {
		return nil, err
	}
	return &Explanation{Assignments: keep, Violations: d.Violations}, nil
}

// applyDerivers 按拓扑序对每个派生器执行一次重算。
// 依赖关系构成 DAG（构建时已校验），单趟即可达到不动点。
func (e *Engine) applyDerivers(rec Record) (Record, error) {
	out := mergeRecords(nil, rec)
	for _, d := range e.derivers {
		v, err := d.Recompute(out)
		if err != nil {
			return nil, fmt.Errorf("fieldauth: derive %q: %v", d.Field, err)
		}
		out[d.Field] = v
	}
	return out, nil
}

// mergeRecords 返回 base 与 overlay 合并后的新 map，不修改入参。
func mergeRecords(base, overlay Record) Record {
	out := make(Record, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// changedFields 返回 old 与 newRec 之间值不同的字段，按字典序。
func changedFields(old, newRec Record) []string {
	seen := make(map[string]bool, len(old)+len(newRec))
	var changed []string
	for k, nv := range newRec {
		seen[k] = true
		if !reflect.DeepEqual(old[k], nv) {
			changed = append(changed, k)
		}
	}
	for k, ov := range old {
		if !seen[k] {
			if !reflect.DeepEqual(ov, newRec[k]) {
				changed = append(changed, k)
			}
		}
	}
	sort.Strings(changed)
	return changed
}

func sortedKeys(rec Record) []string {
	keys := make([]string, 0, len(rec))
	for k := range rec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
