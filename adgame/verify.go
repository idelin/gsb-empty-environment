package adgame

import (
	"fmt"
	"sort"
)

// FailureWitness 是一个动作在某个极小事实集上失效的见证。
type FailureWitness struct {
	// ActionID 是失效动作。
	ActionID string
	// Reason 是失效类别。
	Reason FailureReason
	// States 是触发该失效的包含极小可达事实集 (按事实数升序)。
	States []State
}

// FailureReason 区分动作失效的具体原因。
type FailureReason uint8

const (
	// Inapplicable 表示前置条件不满足 (Require 缺失或 Forbid 命中)。
	Inapplicable FailureReason = iota
	// Ineffective 表示前置满足但执行后状态没有任何变化。
	Ineffective
	// BlockedByBudget 表示守方动作前置满足但剩余预算不足以支付成本。
	BlockedByBudget
)

// String 返回失效类别的可读名称。
func (r FailureReason) String() string {
	switch r {
	case Inapplicable:
		return "inapplicable"
	case Ineffective:
		return "ineffective"
	case BlockedByBudget:
		return "blocked-by-budget"
	default:
		return fmt.Sprintf("reason(%d)", int(r))
	}
}

// NoLegalMoveWitness 描述某行棋方在极小事实集上没有任何合法动作。
type NoLegalMoveWitness struct {
	// Side 是无合法动作的行棋方。
	Side Side
	// States 是触发停着的包含极小可达事实集。
	States []State
}

// ExhaustionWitness 描述预算耗尽: 守方仍有前置满足的防护动作,
// 但全部被预算阻断。
type ExhaustionWitness struct {
	// State 是出现预算耗尽的可达状态。
	State State
	// Ply 是该状态出现时已经走过的半步数。
	Ply int
	// Budget 是当时守方的剩余预算 (必然小于全部可用防护动作的成本)。
	Budget int
	// RemainingDefenderPlies 是此后守方剩余的行棋次数。
	RemainingDefenderPlies int
	// BlockedActions 是前置满足但成本高于剩余预算的守方动作 ID。
	BlockedActions []string
	// ExtraBudget 是恢复至少一个被阻断动作所需的最小追加预算。
	ExtraBudget int
}

// ClosureCertificate 是与极小极大求解独立的最小 Horn 闭包证书。
type ClosureCertificate struct {
	// Closure 是只考虑增加型规则时, 从初始状态出发的最小事实闭包。
	Closure State
	// Rules 是参与闭包计算的单调规则 (来自全部动作的 Add 部分)。
	Rules []ClosureRule
	// GoalReachable 为 false 时闭包都缺少目标必需事实, 目标在任何
	// 攻守交互下都不可达, 构成守方必胜的独立证明。
	GoalReachable bool
	// MissingFacts 是目标 Required 中未进入闭包的事实。
	MissingFacts State
}

// VerificationReport 汇总极小模型穷举验证结果。
type VerificationReport struct {
	// ReachableStates 是忽略预算、从初始状态按动作语义可达的全部状态,
	// 作为极小模型穷举的论域。
	ReachableStates []State
	// Failures 按动作给出全部失效类别的极小事实集见证。
	Failures []FailureWitness
	// NoLegalMoves 给出攻方/守方无合法动作的极小事实集见证。
	NoLegalMoves []NoLegalMoveWitness
	// Exhaustion 给出带预算 BFS 观察到的全部预算耗尽局面。
	Exhaustion []ExhaustionWitness
	// EarliestExhaustion 是半步数最小的预算耗尽见证, 没有时为 nil。
	EarliestExhaustion *ExhaustionWitness
	// MinExtraBudget 是全部耗尽见证中的最小追加预算, 没有时为 -1。
	MinExtraBudget int
	// Closure 是最小 Horn 闭包证书。
	Closure ClosureCertificate
}

type reachConfig struct {
	state  State
	ply    int
	budget int
}

// ReachableStatesBFS 忽略预算做可达性 BFS, 返回从初始状态可达的全部状态
// (含初始状态), 按 (事实数, 掩码) 升序。它是极小模型穷举的论域来源。
func ReachableStatesBFS(g *Game) ([]State, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	seen := make([]bool, 1<<FactCount)
	queue := []State{g.Initial}
	seen[g.Initial] = true
	var out []State
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		out = append(out, s)
		for i := range g.Actions {
			a := &g.Actions[i]
			// 可达性分析忽略预算: 只看前置条件与增删语义,
			// 保证“买不起”的防护动作仍进入论域, 从而能观察其失效形态。
			if !a.Applicable(s) {
				continue
			}
			next := a.Apply(s)
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	sortStates(out)
	return out, nil
}

// Verify 用极小模型穷举验证动作失效、无合法动作与预算耗尽,
// 并附带最小 Horn 闭包证书。验证独立于极小极大求解器, 可交叉核对。
func Verify(g *Game) (*VerificationReport, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	reachable, err := ReachableStatesBFS(g)
	if err != nil {
		return nil, err
	}
	inReach := make([]bool, 1<<FactCount)
	for _, s := range reachable {
		inReach[s] = true
	}

	report := &VerificationReport{
		ReachableStates: reachable,
		MinExtraBudget:  -1,
	}

	for i := range g.Actions {
		a := &g.Actions[i]
		// 失效类别一: 前置条件不满足。论域中该动作不可执行的极小事实集。
		bad := EnumerateInclusionMinimal(reachable, func(s State) bool {
			return !a.Applicable(s)
		})
		if len(bad) > 0 {
			report.Failures = append(report.Failures, FailureWitness{
				ActionID: a.ID, Reason: Inapplicable, States: bad,
			})
		}
		// 失效类别二: 前置满足但执行后无变化。
		noEffect := EnumerateInclusionMinimal(reachable, func(s State) bool {
			return a.Applicable(s) && a.NoOp(s)
		})
		if len(noEffect) > 0 {
			report.Failures = append(report.Failures, FailureWitness{
				ActionID: a.ID, Reason: Ineffective, States: noEffect,
			})
		}
		// 失效类别三: 守方动作前置满足但预算阻断。只在有正成本时有意义。
		if a.Owner == Defender && a.Cost > 0 {
			blocked := EnumerateInclusionMinimal(reachable, func(s State) bool {
				return a.Applicable(s) && a.Cost > g.Budget
			})
			if len(blocked) > 0 {
				report.Failures = append(report.Failures, FailureWitness{
					ActionID: a.ID, Reason: BlockedByBudget, States: blocked,
				})
			}
		}
	}

	for _, side := range []Side{Attacker, Defender} {
		noMoves := EnumerateInclusionMinimal(reachable, func(s State) bool {
			return len(g.LegalMoves(side, s, g.Budget)) == 0
		})
		if len(noMoves) > 0 {
			report.NoLegalMoves = append(report.NoLegalMoves, NoLegalMoveWitness{
				Side: side, States: noMoves,
			})
		}
	}

	report.Exhaustion = budgetExhaustionBFS(g)
	if len(report.Exhaustion) > 0 {
		earliest := report.Exhaustion[0]
		report.EarliestExhaustion = &earliest
		report.MinExtraBudget = earliest.ExtraBudget
		for i := range report.Exhaustion {
			if report.Exhaustion[i].ExtraBudget < report.MinExtraBudget {
				report.MinExtraBudget = report.Exhaustion[i].ExtraBudget
			}
		}
	}

	report.Closure = buildClosureCertificate(g)
	return report, nil
}

// budgetExhaustionBFS 在带预算的真实博弈图上做 BFS, 收集“守方仍有
// 前置满足的动作但全部买不起”的局面。同一 (状态, 半步, 预算) 只记录一次。
func budgetExhaustionBFS(g *Game) []ExhaustionWitness {
	seen := make(map[reachConfig]bool)
	queue := []reachConfig{{state: g.Initial, ply: 0, budget: g.Budget}}
	seen[queue[0]] = true
	var out []ExhaustionWitness

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if g.Goal.Satisfied(cur.state) || cur.ply >= g.Plies() {
			continue
		}
		side := g.sideAt(cur.ply)

		if side == Defender {
			var affordable, blocked []string
			minCost := -1
			for i := range g.Actions {
				a := &g.Actions[i]
				if a.Owner != Defender || !a.Applicable(cur.state) {
					continue
				}
				if a.Cost <= cur.budget {
					affordable = append(affordable, a.ID)
				} else {
					blocked = append(blocked, a.ID)
					if minCost < 0 || a.Cost < minCost {
						minCost = a.Cost
					}
				}
			}
			if len(affordable) == 0 && len(blocked) > 0 {
				remaining := 0
				for p := cur.ply; p < g.Plies(); p++ {
					if g.sideAt(p) == Defender {
						remaining++
					}
				}
				out = append(out, ExhaustionWitness{
					State:                  cur.state,
					Ply:                    cur.ply,
					Budget:                 cur.budget,
					RemainingDefenderPlies: remaining,
					BlockedActions:         blocked,
					ExtraBudget:            minCost - cur.budget,
				})
			}
		}

		for _, a := range g.LegalMoves(side, cur.state, cur.budget) {
			nextBudget := cur.budget
			if side == Defender {
				nextBudget -= a.Cost
			}
			next := reachConfig{
				state:  a.Apply(cur.state),
				ply:    cur.ply + 1,
				budget: nextBudget,
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
		// 无合法动作时显式加入停着后继, 保证停着链也被穷举。
		if len(g.LegalMoves(side, cur.state, cur.budget)) == 0 {
			next := reachConfig{state: cur.state, ply: cur.ply + 1, budget: cur.budget}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Ply != out[j].Ply {
			return out[i].Ply < out[j].Ply
		}
		if out[i].State != out[j].State {
			return out[i].State < out[j].State
		}
		return out[i].Budget < out[j].Budget
	})
	return out
}

func buildClosureCertificate(g *Game) ClosureCertificate {
	rules := make([]ClosureRule, 0, len(g.Actions))
	for i := range g.Actions {
		a := &g.Actions[i]
		if a.Add != 0 {
			rules = append(rules, ClosureRule{Require: a.Require, Add: a.Add})
		}
	}
	closure := LeastFactClosure(g.Initial, rules)
	missing := g.Goal.Required &^ closure
	return ClosureCertificate{
		Closure:       closure,
		Rules:         rules,
		GoalReachable: missing == 0,
		MissingFacts:  missing,
	}
}

func sortStates(states []State) {
	sort.Slice(states, func(i, j int) bool {
		if states[i].Size() != states[j].Size() {
			return states[i].Size() < states[j].Size()
		}
		return states[i] < states[j]
	})
}
