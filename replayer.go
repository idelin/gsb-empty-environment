package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
)

// ReplayReport 记录回放过程，便于核对。
type ReplayReport struct {
	Steps     []StepReport
	GoalMet   bool
	TotalCost int64
}

// StepReport 是单步回放结果。
type StepReport struct {
	Index  int
	Action string
	Cost   int64
	Before string
	After  string
}

// Replay 独立地逐步回放计划：逐步检查前置条件、应用增删效果、
// 最后验证目标达成并累计成本。任何一步不满足前置条件都会返回错误。
func Replay(p *Problem, plan Plan) (*ReplayReport, error) {
	byName := make(map[string]*Action, len(p.Actions))
	for i := range p.Actions {
		byName[p.Actions[i].Name] = &p.Actions[i]
	}
	rep := &ReplayReport{}
	state := p.initState
	for i, name := range plan.Steps {
		a, ok := byName[name]
		if !ok {
			return rep, fmt.Errorf("第 %d 步: 未定义的动作 %q", i+1, name)
		}
		if missing := missingPreconditions(p, a, state); len(missing) > 0 {
			return rep, fmt.Errorf("第 %d 步: 动作 %q 前置条件不满足（%s），当前状态 %s",
				i+1, name, strings.Join(missing, "; "), p.stateString(state))
		}
		next := a.apply(state)
		rep.Steps = append(rep.Steps, StepReport{
			Index:  i + 1,
			Action: name,
			Cost:   a.Cost,
			Before: p.stateString(state),
			After:  p.stateString(next),
		})
		rep.TotalCost += a.Cost
		state = next
	}
	rep.GoalMet = p.isGoal(state)
	if !rep.GoalMet {
		return rep, fmt.Errorf("计划执行完毕但目标未达成，最终状态 %s", p.stateString(state))
	}
	if plan.Cost != 0 && plan.Cost != rep.TotalCost {
		return rep, fmt.Errorf("计划声明成本 %d 与实际累计成本 %d 不一致", plan.Cost, rep.TotalCost)
	}
	return rep, nil
}

// missingPreconditions 返回状态 s 下动作未满足的前置条件描述。
func missingPreconditions(p *Problem, a *Action, s uint32) []string {
	var missing []string
	for _, n := range a.Pre {
		if s&p.bitOf[n] == 0 {
			missing = append(missing, fmt.Sprintf("要求 %q 为真", n))
		}
	}
	for _, n := range a.PreNeg {
		if s&p.bitOf[n] != 0 {
			missing = append(missing, fmt.Sprintf("要求 %q 为假", n))
		}
	}
	return missing
}

// LoadPlan 从 JSON 文件读取计划。
func LoadPlan(path string) (Plan, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return Plan{}, fmt.Errorf("解析 %s 失败: %v", path, err)
	}
	return plan, nil
}
