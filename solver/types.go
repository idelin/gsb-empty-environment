// Package solver 实现有限世界信号博弈的确定性策略求解。
//
// 模型：有限个世界，每个世界带有理数概率并产生一个可观察信号；
// 策略是 信号 -> 动作 的映射（同一信号必须选同一动作，即不可预知规则）。
// 目标按字典序：先最小化最坏情形损失，再最小化期望（平均）损失。
// 支持动作总容量约束：对每条约束 c，
//   sum_w prob(w) * weight_c(w, a(signal(w))) <= limit_c
// 所有算术均使用 math/big.Rat 精确进行。
package solver

import (
	"fmt"
	"math/big"
	"sort"
)

// Rat 是有理数类型别名。
type Rat = big.Rat

// World 描述一个可能世界。
type World struct {
	ID     string
	Prob   *Rat
	Signal string
}

// Capacity 描述一条共享容量约束。
// Weight[worldID][action] 表示在该世界选择该动作消耗的容量（缺省为 0）。
type Capacity struct {
	Name   string
	Limit  *Rat
	Weight map[string]map[string]*Rat
}

// Problem 是一次求解的完整输入。
type Problem struct {
	Worlds     []World
	Actions    []string
	Loss       map[string]map[string]*Rat // Loss[worldID][action]
	Capacities []Capacity
}

// Strategy 是确定性策略：信号 -> 动作。
type Strategy map[string]string

// Validate 检查问题定义是否自洽。
func (p *Problem) Validate() error {
	if len(p.Worlds) == 0 {
		return fmt.Errorf("no worlds defined")
	}
	if len(p.Actions) == 0 {
		return fmt.Errorf("no actions defined")
	}
	actionSet := make(map[string]bool, len(p.Actions))
	for _, a := range p.Actions {
		if actionSet[a] {
			return fmt.Errorf("duplicate action %q", a)
		}
		actionSet[a] = true
	}
	total := new(Rat)
	for _, w := range p.Worlds {
		if w.Prob == nil || w.Prob.Sign() < 0 {
			return fmt.Errorf("world %q: probability must be a non-negative rational", w.ID)
		}
		total.Add(total, w.Prob)
		if _, ok := p.Loss[w.ID]; !ok {
			return fmt.Errorf("world %q: missing loss row", w.ID)
		}
		for _, a := range p.Actions {
			if p.Loss[w.ID][a] == nil {
				return fmt.Errorf("world %q: missing loss for action %q", w.ID, a)
			}
		}
	}
	if total.Cmp(big.NewRat(1, 1)) != 0 {
		return fmt.Errorf("world probabilities sum to %s, want 1", total.RatString())
	}
	for _, c := range p.Capacities {
		if c.Limit == nil || c.Limit.Sign() < 0 {
			return fmt.Errorf("capacity %q: limit must be a non-negative rational", c.Name)
		}
	}
	return nil
}

// Signals 返回按字典序排序的全部信号。
func (p *Problem) Signals() []string {
	seen := make(map[string]bool)
	var out []string
	for _, w := range p.Worlds {
		if !seen[w.Signal] {
			seen[w.Signal] = true
			out = append(out, w.Signal)
		}
	}
	sort.Strings(out)
	return out
}

// SortedActions 返回按字典序排序的动作列表，保证枚举顺序确定。
func (p *Problem) SortedActions() []string {
	out := make([]string, len(p.Actions))
	copy(out, p.Actions)
	sort.Strings(out)
	return out
}
