package recovery

import (
	"errors"
	"fmt"
	"sort"
)

// outcome 是信念 b 执行某动作后的一个观察分支:环境选择的转移若公开
// 观察相同,则留于同一公开历史等价类,构成后继信念 sub;cost 为该分支
// 内最坏转移成本。
type outcome struct {
	obs  Observation
	sub  string
	cost int
}

// node 是信念图中的一个信念。
type node struct {
	states []State
	goal   bool                 // 所有状态均已完成
	acts   map[Action][]outcome // 仅含对信念内所有未完成状态均有效的动作
}

// analysis 是模型的信念图与必胜域。
type analysis struct {
	nodes map[string]*node
	start string
	win   map[string]bool
}

// analyze 枚举从初始信念可达的全部信念,并计算必胜域(可强制完成的
// 信念集合)。必胜域是最小不动点:目标信念必胜;某信念存在某动作使其
// 所有观察分支的后继均必胜,则该信念必胜。
func analyze(m *Model) (*analysis, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	nodes := map[string]*node{}
	statesOf := map[string][]State{}
	start := BeliefKey(m.Start...)
	statesOf[start] = sortedUnique(m.Start)
	queue := []string{start}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		states := statesOf[key]
		n := &node{states: states, goal: true, acts: map[Action][]outcome{}}
		var nonGoal []State
		for _, s := range states {
			if !m.Goal[s] {
				nonGoal = append(nonGoal, s)
				n.goal = false
			}
		}
		actionSet := map[Action]bool{}
		for _, s := range nonGoal {
			for a := range m.Steps[s] {
				actionSet[a] = true
			}
		}
		for a := range actionSet {
			valid := true
			type group struct {
				cost int
				to   map[State]bool
			}
			groups := map[Observation]*group{}
			for _, s := range nonGoal {
				trs, ok := m.Steps[s][a]
				if !ok || len(trs) == 0 {
					valid = false
					break
				}
				for _, t := range trs {
					g := groups[t.Obs]
					if g == nil {
						g = &group{to: map[State]bool{}}
						groups[t.Obs] = g
					}
					if t.Cost > g.cost {
						g.cost = t.Cost
					}
					g.to[t.To] = true
				}
			}
			if !valid {
				continue
			}
			var outs []outcome
			for obs, g := range groups {
				sub := make([]State, 0, len(g.to))
				for s := range g.to {
					sub = append(sub, s)
				}
				subKey := BeliefKey(sub...)
				outs = append(outs, outcome{obs: obs, sub: subKey, cost: g.cost})
				if _, ok := statesOf[subKey]; !ok {
					statesOf[subKey] = sortedUnique(sub)
					queue = append(queue, subKey)
				}
			}
			sort.Slice(outs, func(i, j int) bool { return outs[i].sub < outs[j].sub })
			n.acts[a] = outs
		}
		nodes[key] = n
	}
	win := map[string]bool{}
	for k, n := range nodes {
		if n.goal {
			win[k] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for k, n := range nodes {
			if win[k] {
				continue
			}
			for _, outs := range n.acts {
				all := len(outs) > 0
				for _, o := range outs {
					if !win[o.sub] {
						all = false
						break
					}
				}
				if all {
					win[k] = true
					changed = true
					break
				}
			}
		}
	}
	return &analysis{nodes: nodes, start: start, win: win}, nil
}

// solveValues 在必胜域上从 0 向上做值迭代,收敛到 Bellman 方程
// V(b) = min_a max_branch (cost + V(sub)) 的最小不动点,即最小化的
// 最坏额外帧数;同时记录每个信念的选定动作。由于转移成本 >= 1,
// 有限不动点唯一,故该值就是博弈真值。
func (an *analysis) solveValues() (map[string]int, map[string]Action, error) {
	V := map[string]int{}
	for k := range an.win {
		V[k] = 0
	}
	choice := map[string]Action{}
	maxIter := len(an.nodes) + 1
	for i := 0; ; i++ {
		stable := true
		for k := range an.win {
			n := an.nodes[k]
			if n.goal {
				continue
			}
			best := -1
			var bestA Action
			for a, outs := range n.acts {
				worst := 0
				ok := true
				for _, o := range outs {
					if !an.win[o.sub] {
						ok = false
						break
					}
					if c := o.cost + V[o.sub]; c > worst {
						worst = c
					}
				}
				if !ok {
					continue
				}
				if best < 0 || worst < best {
					best = worst
					bestA = a
				}
			}
			if best < 0 {
				return nil, nil, fmt.Errorf("recovery: 必胜信念 %q 无全必胜动作", k)
			}
			choice[k] = bestA
			if V[k] != best {
				V[k] = best
				stable = false
			}
		}
		if stable {
			return V, choice, nil
		}
		if i > maxIter {
			return nil, nil, errors.New("recovery: 值迭代未在信念数上界内收敛")
		}
	}
}

// Result 是 Solve 的判定结果。
type Result struct {
	// Feasible 表示是否存在在所有相容发送历史中均可完成的公开恢复策略。
	Feasible bool
	// Bound 是可行时最小化的最坏额外帧数。
	Bound int
	// Cert 是独立证书:可行时为取值函数与策略,不可行时为对抗陷阱。
	Cert *Certificate
}

// Solve 判定模型是否存在公开恢复策略,若存在则最小化最坏额外帧数。
func Solve(m *Model) (*Result, error) {
	an, err := analyze(m)
	if err != nil {
		return nil, err
	}
	if !an.win[an.start] {
		var trap []string
		for k := range an.nodes {
			if !an.win[k] {
				trap = append(trap, k)
			}
		}
		sort.Strings(trap)
		return &Result{
			Feasible: false,
			Cert:     &Certificate{Feasible: false, Trap: trap},
		}, nil
	}
	V, choice, err := an.solveValues()
	if err != nil {
		return nil, err
	}
	cert := &Certificate{
		Feasible: true,
		Bound:    V[an.start],
		Values:   V,
		Choice:   choice,
	}
	return &Result{Feasible: true, Bound: V[an.start], Cert: cert}, nil
}
