package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
)

// MaxPropositions 是规划器支持的命题数量上限。
const MaxPropositions = 20

// Action 描述一个动作：前置条件（正/负）、增删效果与非负成本。
type Action struct {
	Name   string   `json:"name"`
	Cost   int64    `json:"cost"`
	Pre    []string `json:"pre"`     // 必须为真的命题
	PreNeg []string `json:"pre_neg"` // 必须为假的命题
	Add    []string `json:"add"`
	Del    []string `json:"del"`

	preMask    uint32
	preNegMask uint32
	addMask    uint32
	delMask    uint32
}

// Problem 是一个以布尔命题描述的有限状态规划问题。
type Problem struct {
	Propositions []string `json:"propositions"`
	Initial      []string `json:"initial"`
	Goal         []string `json:"goal"`
	GoalNeg      []string `json:"goal_neg"`
	Actions      []Action `json:"actions"`

	bitOf     map[string]uint32
	initState uint32
	goalTrue  uint32
	goalFalse uint32
}

// LoadProblem 从 JSON 文件读取并校验问题定义。
func LoadProblem(path string) (*Problem, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Problem
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %v", path, err)
	}
	if err := p.compile(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Problem) compile() error {
	if len(p.Propositions) == 0 {
		return fmt.Errorf("至少需要一个命题")
	}
	if len(p.Propositions) > MaxPropositions {
		return fmt.Errorf("命题数量 %d 超过上限 %d", len(p.Propositions), MaxPropositions)
	}
	p.bitOf = make(map[string]uint32, len(p.Propositions))
	for i, name := range p.Propositions {
		if name == "" {
			return fmt.Errorf("第 %d 个命题名为空", i)
		}
		if _, dup := p.bitOf[name]; dup {
			return fmt.Errorf("命题 %q 重复定义", name)
		}
		p.bitOf[name] = 1 << uint(i)
	}
	mask := func(names []string, what string) (uint32, error) {
		var m uint32
		for _, n := range names {
			b, ok := p.bitOf[n]
			if !ok {
				return 0, fmt.Errorf("%s 引用了未定义的命题 %q", what, n)
			}
			m |= b
		}
		return m, nil
	}
	var err error
	if p.initState, err = mask(p.Initial, "初始状态"); err != nil {
		return err
	}
	if p.goalTrue, err = mask(p.Goal, "目标"); err != nil {
		return err
	}
	if p.goalFalse, err = mask(p.GoalNeg, "负目标"); err != nil {
		return err
	}
	if p.goalTrue&p.goalFalse != 0 {
		return fmt.Errorf("目标中同一命题既要求为真又要求为假")
	}
	seen := make(map[string]bool)
	for i := range p.Actions {
		a := &p.Actions[i]
		if a.Name == "" {
			return fmt.Errorf("第 %d 个动作缺少名称", i)
		}
		if seen[a.Name] {
			return fmt.Errorf("动作名 %q 重复", a.Name)
		}
		seen[a.Name] = true
		if a.Cost < 0 {
			return fmt.Errorf("动作 %q 成本为负 (%d)，成本必须非负", a.Name, a.Cost)
		}
		if a.preMask, err = mask(a.Pre, fmt.Sprintf("动作 %q 的前置条件", a.Name)); err != nil {
			return err
		}
		if a.preNegMask, err = mask(a.PreNeg, fmt.Sprintf("动作 %q 的负前置条件", a.Name)); err != nil {
			return err
		}
		if a.preMask&a.preNegMask != 0 {
			return fmt.Errorf("动作 %q 的前置条件自相矛盾", a.Name)
		}
		if a.addMask, err = mask(a.Add, fmt.Sprintf("动作 %q 的增效果", a.Name)); err != nil {
			return err
		}
		if a.delMask, err = mask(a.Del, fmt.Sprintf("动作 %q 的删效果", a.Name)); err != nil {
			return err
		}
	}
	return nil
}

// applicable 判断动作在状态 s 下是否可执行。
func (a *Action) applicable(s uint32) bool {
	return s&a.preMask == a.preMask && s&a.preNegMask == 0
}

// apply 计算执行动作后的后继状态：先删后增（增效果优先）。
func (a *Action) apply(s uint32) uint32 {
	return (s &^ a.delMask) | a.addMask
}

// isGoal 判断状态是否满足目标条件。
func (p *Problem) isGoal(s uint32) bool {
	return s&p.goalTrue == p.goalTrue && s&p.goalFalse == 0
}

// stateString 以命题名列表形式展示状态，便于阅读。
func (p *Problem) stateString(s uint32) string {
	var names []string
	for i, name := range p.Propositions {
		if s&(1<<uint(i)) != 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "{}"
	}
	return "{" + joinNames(names) + "}"
}

func joinNames(names []string) string {
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}

// sortedStates 按成本升序、状态值升序排序，保证摘要输出稳定。
func sortedStates(m map[uint32]int64) []uint32 {
	states := make([]uint32, 0, len(m))
	for s := range m {
		states = append(states, s)
	}
	sort.Slice(states, func(i, j int) bool {
		ci, cj := m[states[i]], m[states[j]]
		if ci != cj {
			return ci < cj
		}
		return states[i] < states[j]
	})
	return states
}
