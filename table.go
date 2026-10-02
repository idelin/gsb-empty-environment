package decisiontable

import (
	"fmt"
	"strings"
)

// Dimension 描述一个条件维度（决策表的一列）。
type Dimension struct {
	Name string
	Kind Kind
}

// Rule 是一条规则：各维度约束 + 动作。Conds 长度必须与维度数一致。
type Rule struct {
	ID     int // 1 起始的规则编号（规则行号）
	Action string
	Conds  []Constraint
}

// String 返回规则的可读形式，如 "规则3: VIP=true 且 金额>500 => 0.8"。
func (r Rule) String(dims []Dimension) string {
	parts := make([]string, 0, len(dims))
	for i, c := range r.Conds {
		if !c.Any {
			parts = append(parts, c.String(dims[i]))
		}
	}
	cond := "任意条件"
	if len(parts) > 0 {
		cond = strings.Join(parts, " 且 ")
	}
	return fmt.Sprintf("规则%d: %s => %s", r.ID, cond, r.Action)
}

// Table 是条件分支判定表：N 个条件维度 + 若干规则行。
type Table struct {
	Dims  []Dimension
	Rules []Rule
}

// NewTable 创建判定表。
func NewTable(dims ...Dimension) *Table {
	return &Table{Dims: dims}
}

// Add 追加一条规则，返回规则编号（从 1 开始）。conds 数量必须与维度数一致。
func (t *Table) Add(action string, conds ...Constraint) int {
	if len(conds) != len(t.Dims) {
		panic(fmt.Sprintf("decisiontable: 规则条件数 %d 与维度数 %d 不一致", len(conds), len(t.Dims)))
	}
	id := len(t.Rules) + 1
	t.Rules = append(t.Rules, Rule{ID: id, Action: action, Conds: conds})
	return id
}

// SetAction 修改指定编号规则的动作（用于修复冲突）。
func (t *Table) SetAction(id int, action string) {
	for i := range t.Rules {
		if t.Rules[i].ID == id {
			t.Rules[i].Action = action
			return
		}
	}
	panic(fmt.Sprintf("decisiontable: 规则%d 不存在", id))
}

// RemoveRule 删除指定编号的规则（用于修复冲突）。
func (t *Table) RemoveRule(id int) {
	for i := range t.Rules {
		if t.Rules[i].ID == id {
			t.Rules = append(t.Rules[:i], t.Rules[i+1:]...)
			return
		}
	}
	panic(fmt.Sprintf("decisiontable: 规则%d 不存在", id))
}

// Input 是一次匹配输入：维度名 -> 值（bool / float64 / int / string）。
type Input map[string]any

// Match 线性扫描规则，返回第一条匹配规则的动作（未压缩时的朴素匹配）。
func (t *Table) Match(in Input) (string, bool) {
	for _, r := range t.Rules {
		if ruleMatches(t.Dims, r, in) {
			return r.Action, true
		}
	}
	return "", false
}

func ruleMatches(dims []Dimension, r Rule, in Input) bool {
	for i, c := range r.Conds {
		if !c.matchValue(dims[i].Kind, in[dims[i].Name]) {
			return false
		}
	}
	return true
}

// Conflict 描述一对重叠且动作矛盾的规则，Region 是逐维度的重叠区域。
type Conflict struct {
	A, B   Rule
	Region []Constraint
}

// String 返回冲突的可读定位，如：
// "冲突: 规则3(动作=0.8) 与 规则7(动作=0.7) 重叠于 [VIP=true 且 金额>500]"
func (c Conflict) String(dims []Dimension) string {
	parts := make([]string, 0, len(dims))
	for i, rc := range c.Region {
		if !rc.Any {
			parts = append(parts, rc.String(dims[i]))
		}
	}
	region := "任意条件"
	if len(parts) > 0 {
		region = strings.Join(parts, " 且 ")
	}
	return fmt.Sprintf("冲突: 规则%d(动作=%s) 与 规则%d(动作=%s) 重叠于 [%s]",
		c.A.ID, c.A.Action, c.B.ID, c.B.Action, region)
}

// Conflicts 检测所有重叠且动作矛盾的规则对（O(n²)，逐维度求交）。
func (t *Table) Conflicts() []Conflict {
	var out []Conflict
	for i := 0; i < len(t.Rules); i++ {
		for j := i + 1; j < len(t.Rules); j++ {
			a, b := t.Rules[i], t.Rules[j]
			if a.Action == b.Action {
				continue // 动作一致，重叠不构成冲突
			}
			if region, ok := intersectRules(t.Dims, a, b); ok {
				out = append(out, Conflict{A: a, B: b, Region: region})
			}
		}
	}
	return out
}

// intersectRules 逐维度求交；全部维度相交则两规则重叠。
func intersectRules(dims []Dimension, a, b Rule) ([]Constraint, bool) {
	region := make([]Constraint, len(dims))
	for d := range dims {
		c, ok := a.Conds[d].intersect(b.Conds[d], dims[d].Kind)
		if !ok {
			return nil, false
		}
		region[d] = c
	}
	return region, true
}

// Compress 反复合并"动作相同、仅一个维度不同且该维度可合并"的规则对，
// 直到不动点，返回压缩后的极小规则集（通配符规则）。
func (t *Table) Compress() []Rule {
	rules := make([]Rule, len(t.Rules))
	copy(rules, t.Rules)
	for {
		merged := false
	scan:
		for i := 0; i < len(rules); i++ {
			for j := i + 1; j < len(rules); j++ {
				if rules[i].Action != rules[j].Action {
					continue
				}
				if m, ok := mergeRules(t.Dims, rules[i], rules[j]); ok {
					rules[i] = m
					rules = append(rules[:j], rules[j+1:]...)
					merged = true
					break scan
				}
			}
		}
		if !merged {
			return rules
		}
	}
}

// mergeRules 若两条规则完全相同，或仅一个维度不同且该维度约束可合并，
// 则返回合并后的规则（保留较小编号）。
func mergeRules(dims []Dimension, a, b Rule) (Rule, bool) {
	diff := -1
	for d := range dims {
		if !a.Conds[d].equal(b.Conds[d], dims[d].Kind) {
			if diff != -1 {
				return Rule{}, false // 超过一个维度不同，无法一步合并
			}
			diff = d
		}
	}
	m := Rule{ID: a.ID, Action: a.Action, Conds: make([]Constraint, len(dims))}
	if b.ID < a.ID {
		m.ID = b.ID
	}
	copy(m.Conds, a.Conds)
	if diff == -1 {
		return m, true // 完全重复的规则，去重
	}
	mc, ok := a.Conds[diff].merge(b.Conds[diff], dims[diff].Kind)
	if !ok {
		return Rule{}, false
	}
	m.Conds[diff] = mc
	return m, true
}
