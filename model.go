package main

import (
	"fmt"
	"sort"
	"strings"
)

// Field 描述一个有限取值的字段。
type Field struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Condition 描述规则触发条件；未指定的部分不参与匹配。
type Condition struct {
	Queue  string            `json:"queue,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// Action 描述规则命中后的动作；未指定的部分保持不变。
type Action struct {
	Queue  string            `json:"queue,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// Rule 一条路由规则：条件命中时执行动作。
type Rule struct {
	Name string    `json:"name,omitempty"`
	When Condition `json:"when"`
	Set  Action    `json:"set"`
}

// Spec 分析器的完整输入。
type Spec struct {
	Queues []string `json:"queues"`
	Fields []Field  `json:"fields"`
	Rules  []Rule   `json:"rules"`
}

type fieldValue struct {
	field int
	value int
}

type compiledRule struct {
	name       string
	condQueue  int // -1 表示任意队列
	condFields []fieldValue
	setQueue   int // -1 表示不改变队列
	setFields  []fieldValue
}

// Model 编译状态下的迁移系统。状态 = (队列, 各字段取值)，
// 按 (队列, 字段1, 字段2, ...) 字典序编码为 0..NumStates-1 的整数，
// 编号最小者即报告中的“最小输入”。
type Model struct {
	queues    []string
	fields    []Field
	radix     []int
	rules     []compiledRule
	NumStates int
	fieldProd int // 所有字段取值数的乘积
}

// Compile 校验并编译 Spec。
func Compile(spec *Spec, maxStates int) (*Model, error) {
	if len(spec.Queues) == 0 {
		return nil, fmt.Errorf("至少需要定义一个队列")
	}
	if maxStates <= 0 || maxStates > 1<<30 {
		return nil, fmt.Errorf("max-states 必须在 1 和 %d 之间", 1<<30)
	}
	queueIdx := make(map[string]int, len(spec.Queues))
	for i, q := range spec.Queues {
		if q == "" {
			return nil, fmt.Errorf("队列名不能为空")
		}
		if _, dup := queueIdx[q]; dup {
			return nil, fmt.Errorf("队列 %q 重复定义", q)
		}
		queueIdx[q] = i
	}
	fieldIdx := make(map[string]int, len(spec.Fields))
	valIdx := make([]map[string]int, len(spec.Fields))
	radix := make([]int, len(spec.Fields))
	for i, f := range spec.Fields {
		if f.Name == "" {
			return nil, fmt.Errorf("字段名不能为空")
		}
		if _, dup := fieldIdx[f.Name]; dup {
			return nil, fmt.Errorf("字段 %q 重复定义", f.Name)
		}
		if len(f.Values) == 0 {
			return nil, fmt.Errorf("字段 %q 至少需要一个取值", f.Name)
		}
		vm := make(map[string]int, len(f.Values))
		for j, v := range f.Values {
			if _, dup := vm[v]; dup {
				return nil, fmt.Errorf("字段 %q 的取值 %q 重复", f.Name, v)
			}
			vm[v] = j
		}
		fieldIdx[f.Name] = i
		valIdx[i] = vm
		radix[i] = len(f.Values)
	}
	total := int64(len(spec.Queues))
	for _, r := range radix {
		total *= int64(r)
		if total > int64(maxStates) {
			return nil, fmt.Errorf("状态空间超过上限 %d，可用 -max-states 调整", maxStates)
		}
	}
	m := &Model{
		queues:    spec.Queues,
		fields:    spec.Fields,
		radix:     radix,
		NumStates: int(total),
		fieldProd: int(total) / len(spec.Queues),
	}
	for i, r := range spec.Rules {
		cr := compiledRule{name: r.Name, condQueue: -1, setQueue: -1}
		if cr.name == "" {
			cr.name = fmt.Sprintf("rule-%d", i)
		}
		if r.When.Queue != "" {
			q, ok := queueIdx[r.When.Queue]
			if !ok {
				return nil, fmt.Errorf("规则 %q 的条件引用了未知队列 %q", cr.name, r.When.Queue)
			}
			cr.condQueue = q
		}
		cf, err := compileFieldValues(r.When.Fields, fieldIdx, valIdx, cr.name, "条件")
		if err != nil {
			return nil, err
		}
		cr.condFields = cf
		if r.Set.Queue != "" {
			q, ok := queueIdx[r.Set.Queue]
			if !ok {
				return nil, fmt.Errorf("规则 %q 的动作引用了未知队列 %q", cr.name, r.Set.Queue)
			}
			cr.setQueue = q
		}
		sf, err := compileFieldValues(r.Set.Fields, fieldIdx, valIdx, cr.name, "动作")
		if err != nil {
			return nil, err
		}
		cr.setFields = sf
		m.rules = append(m.rules, cr)
	}
	return m, nil
}

func compileFieldValues(in map[string]string, fieldIdx map[string]int, valIdx []map[string]int, rule, what string) ([]fieldValue, error) {
	var out []fieldValue
	for name, val := range in {
		fi, ok := fieldIdx[name]
		if !ok {
			return nil, fmt.Errorf("规则 %q 的%s引用了未知字段 %q", rule, what, name)
		}
		vi, ok := valIdx[fi][val]
		if !ok {
			return nil, fmt.Errorf("规则 %q 的%s引用了字段 %q 的未知取值 %q", rule, what, name, val)
		}
		out = append(out, fieldValue{field: fi, value: vi})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].field < out[b].field })
	return out, nil
}

// decode 将状态编码还原为队列下标和各字段取值下标。
func (m *Model) decode(s int) (int, []int) {
	vals := make([]int, len(m.radix))
	for i := len(m.radix) - 1; i >= 0; i-- {
		vals[i] = s % m.radix[i]
		s /= m.radix[i]
	}
	return s, vals
}

func (m *Model) encode(q int, vals []int) int {
	s := q
	for i, v := range vals {
		s = s*m.radix[i] + v
	}
	return s
}

// QueueOf 返回状态所在队列的下标。
func (m *Model) QueueOf(s int) int { return s / m.fieldProd }

// Transition 一条状态迁移：由规则 Rule 触发，到达 Next。
type Transition struct {
	Rule int
	Next int
}

// Successors 返回状态下所有命中规则产生的迁移（每个命中规则一条）。
func (m *Model) Successors(s int) []Transition {
	q, vals := m.decode(s)
	var out []Transition
	for ri := range m.rules {
		r := &m.rules[ri]
		if r.condQueue >= 0 && r.condQueue != q {
			continue
		}
		matched := true
		for _, fv := range r.condFields {
			if vals[fv.field] != fv.value {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		nq := q
		if r.setQueue >= 0 {
			nq = r.setQueue
		}
		nvals := vals
		if len(r.setFields) > 0 {
			nvals = make([]int, len(vals))
			copy(nvals, vals)
			for _, fv := range r.setFields {
				nvals[fv.field] = fv.value
			}
		}
		out = append(out, Transition{Rule: ri, Next: m.encode(nq, nvals)})
	}
	return out
}

// StateString 返回状态的可读表示。
func (m *Model) StateString(s int) string {
	q, vals := m.decode(s)
	var b strings.Builder
	fmt.Fprintf(&b, "queue=%s", m.queues[q])
	for i, f := range m.fields {
		fmt.Fprintf(&b, ", %s=%s", f.Name, f.Values[vals[i]])
	}
	return b.String()
}

// QueueName 返回队列下标对应的名字。
func (m *Model) QueueName(q int) string { return m.queues[q] }
