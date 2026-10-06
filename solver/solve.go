package solver

import (
	"fmt"
	"sort"
)

// Evaluation 是一个策略的精确评估结果。
type Evaluation struct {
	Strategy  Strategy
	Feasible  bool
	Violation string // 若不可行，说明违反的约束
	Worst     *Rat   // 最坏情形损失：max_w loss(w, a(signal(w)))
	Average   *Rat   // 期望损失：sum_w prob(w) * loss(w, a(signal(w)))
}

// Better 按字典序比较：(Worst, Average) 更小者优。
func Better(x, y *Evaluation) bool {
	if c := x.Worst.Cmp(y.Worst); c != 0 {
		return c < 0
	}
	return x.Average.Cmp(y.Average) < 0
}

// Evaluate 评估一个策略：核对共享容量约束并计算两个目标。
// 策略本身定义在信号上，因此天然满足不可预知规则；
// 此处仍显式校验每个世界的动作与所属信号一致，作为规则核对。
func (p *Problem) Evaluate(st Strategy) (*Evaluation, error) {
	ev := &Evaluation{Strategy: st, Feasible: true, Worst: new(Rat), Average: new(Rat)}
	actionSet := make(map[string]bool)
	for _, a := range p.Actions {
		actionSet[a] = true
	}
	// 不可预知规则核对：同信号的世界必须执行同一动作。
	for _, w := range p.Worlds {
		a, ok := st[w.Signal]
		if !ok {
			return nil, fmt.Errorf("strategy missing action for signal %q", w.Signal)
		}
		if !actionSet[a] {
			return nil, fmt.Errorf("strategy assigns unknown action %q to signal %q", a, w.Signal)
		}
	}
	// 容量约束核对（精确有理数累加）。
	for _, c := range p.Capacities {
		used := new(Rat)
		for _, w := range p.Worlds {
			a := st[w.Signal]
			if wt := c.Weight[w.ID][a]; wt != nil {
				term := new(Rat).Mul(w.Prob, wt)
				used.Add(used, term)
			}
		}
		if used.Cmp(c.Limit) > 0 {
			ev.Feasible = false
			ev.Violation = fmt.Sprintf("capacity %q: used %s > limit %s", c.Name, used.RatString(), c.Limit.RatString())
			return ev, nil
		}
	}
	// 目标值。
	first := true
	for _, w := range p.Worlds {
		loss := p.Loss[w.ID][st[w.Signal]]
		if first || loss.Cmp(ev.Worst) > 0 {
			ev.Worst.Set(loss)
			first = false
		}
		ev.Average.Add(ev.Average, new(Rat).Mul(w.Prob, loss))
	}
	return ev, nil
}

// Result 是求解结果。
type Result struct {
	Best       *Evaluation
	Enumerated int // 枚举的策略总数
	Feasible   int // 可行策略数
}

// Enumerate 枚举全部 信号->动作 确定性策略，逐一核对容量约束，
// 返回字典序 (最坏损失, 期望损失) 最优的可行策略。
func (p *Problem) Enumerate() (*Result, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	signals := p.Signals()
	actions := p.SortedActions()
	res := &Result{}
	idx := make([]int, len(signals))
	for {
		res.Enumerated++
		st := make(Strategy, len(signals))
		for i, s := range signals {
			st[s] = actions[idx[i]]
		}
		ev, err := p.Evaluate(st)
		if err != nil {
			return nil, err
		}
		if ev.Feasible {
			res.Feasible++
			if res.Best == nil || Better(ev, res.Best) {
				res.Best = ev
			}
		}
		// 进位：枚举下一个动作组合。
		i := len(idx) - 1
		for ; i >= 0; i-- {
			idx[i]++
			if idx[i] < len(actions) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			break
		}
	}
	if res.Best == nil {
		return res, fmt.Errorf("no feasible strategy: all %d enumerated strategies violate capacity constraints", res.Enumerated)
	}
	return res, nil
}

// String 以确定性顺序格式化策略。
func (st Strategy) String() string {
	var sigs []string
	for s := range st {
		sigs = append(sigs, s)
	}
	sort.Strings(sigs)
	out := ""
	for i, s := range sigs {
		if i > 0 {
			out += ", "
		}
		out += s + "->" + st[s]
	}
	return "{" + out + "}"
}
