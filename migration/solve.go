package migration

import (
	"errors"
	"sort"

	"prescreen.local/ddd-10554-v3/spec"
)

// policyChoice 是某个（信念，预算）格点选出的动作及其最坏烧录次数。
type policyChoice struct {
	action *spec.Action
	value  int
}

// solve 对枚举得到的信念图求解最小最坏烧录次数。
//
// 格点为（信念，剩余预算）。burn 消耗 1 次预算，掉电后预算重置为 B，
// 非 burn 动作零代价。环境在 ack/boot 分支取较差者（最大烧录），
// 宿主在合法动作中取较优者（最小最坏烧录）。
//
// 采用 Bellman–Ford 式松弛：所有非终止格点初值取有限上界，反复扫描
// 直到没有任何格点可再下降。零代价自环（如 pending 下重放 request）
// 不会造成虚假不动点，因为后继 confirm 的更小值会经多轮扫描回传。
func (p *Planner) solve() error {
	// 全量核验：枚举到的每个具体状态都必须满足安全不变量。
	for sk, s := range p.stateOf {
		if !p.model.Safe(s) {
			return errors.New("枚举发现不安全状态: " + sk)
		}
	}

	B := p.model.chip.MaxBurns
	// 有限上界：任何最优策略的烧录次数都不会超过具体状态空间规模，
	// 用有限初值代替 +inf，避免零代价自环（yield/ack）阻断最小值传播。
	finiteBound := len(p.stateOf)*B + 1
	inf := finiteBound + 1

	if !p.terminalReachable() {
		return errors.New("信念图中不存在从根到终止信念的安全路径")
	}

	nodeKeys := make([]string, 0, len(p.nodes))
	for k := range p.nodes {
		nodeKeys = append(nodeKeys, k)
	}
	sort.Strings(nodeKeys)

	// legal[b][beliefKey] = 该预算下的合法动作交集。
	legal := make([]map[string][]*spec.Action, B+1)
	for b := 0; b <= B; b++ {
		legal[b] = map[string][]*spec.Action{}
		for _, k := range nodeKeys {
			n := p.nodes[k]
			if p.allTerminal(n) {
				legal[b][k] = nil
			} else {
				var usable []*spec.Action
				for _, a := range p.intersectActions(n, b) {
					if _, ok := n.edges[actionFingerprint(a)]; ok {
						usable = append(usable, a)
					}
				}
				legal[b][k] = usable
			}
		}
	}

	// V[b][beliefKey]。
	V := make([]map[string]int, B+1)
	for b := 0; b <= B; b++ {
		V[b] = map[string]int{}
		for _, k := range nodeKeys {
			if p.allTerminal(p.nodes[k]) {
				V[b][k] = 0
			} else {
				V[b][k] = finiteBound
			}
		}
	}

	// branchValue 计算某格点执行动作后，环境取最差分支的代价。
	branchValue := func(b int, n *beliefNode, action *spec.Action) int {
		ak := actionFingerprint(action)
		burnCost := 0
		if action.Kind == spec.ActionBurn {
			burnCost = 1
		}
		ackKey, _ := spec.ObservationKey(&spec.Observation{Kind: spec.ObsAck})

		worst := 0
		for obsFingerprint, childKey := range n.edges[ak] {
			var nextBudget int
			if obsFingerprint == ackKey {
				// 芯片确认：不掉电，仅扣减本次烧录预算。
				nextBudget = b - burnCost
			} else {
				// 掉电重启：新会话预算重置为芯片上限。
				nextBudget = B
			}
			cand := burnCost + V[nextBudget][childKey]
			if cand > worst {
				worst = cand
			}
		}
		return worst
	}

	// 格点总数有限，松弛上界取格点数；超出仍在下降说明存在异常负环
	// （本问题烧录代价非负，理论上不可能）。
	maxRounds := (B+1)*len(nodeKeys) + 1
	for iter := 0; iter < maxRounds; iter++ {
		changed := false
		for _, k := range nodeKeys {
			n := p.nodes[k]
			if p.allTerminal(n) {
				continue
			}
			for b := 0; b <= B; b++ {
				acts := legal[b][k]
				if len(acts) == 0 {
					continue
				}
				best := inf
				for _, a := range acts {
					if v := branchValue(b, n, a); v < best {
						best = v
					}
				}
				if best < V[b][k] {
					V[b][k] = best
					changed = true
				}
			}
		}
		if !changed {
			// 收敛后再做一次完整扫描确认，避免自环初值造成的伪不动点。
			stable := true
			for _, k := range nodeKeys {
				n := p.nodes[k]
				if p.allTerminal(n) {
					continue
				}
				for b := 0; b <= B; b++ {
					acts := legal[b][k]
					if len(acts) == 0 {
						continue
					}
					best := inf
					for _, a := range acts {
						if v := branchValue(b, n, a); v < best {
							best = v
						}
					}
					if best < V[b][k] {
						stable = false
						V[b][k] = best
					}
				}
			}
			if stable {
				break
			}
			changed = true
		}
		if iter == maxRounds-1 {
			return errors.New("松弛未在格点上界内收敛")
		}
	}

	// 提取确定性策略（动作已按动作指纹排序，平局可复现）。
	// 到终止信念的最小边距用于在等代价动作间优先选择“推进型”动作，
	// 避免选择零代价自环（如 pending 下重放 request）造成策略空转。
	distance := p.terminalDistances()
	p.choice = map[string]map[int]*policyChoice{}
	for _, k := range nodeKeys {
		n := p.nodes[k]
		if p.allTerminal(n) {
			continue
		}
		p.choice[k] = map[int]*policyChoice{}
		for b := 0; b <= B; b++ {
			acts := legal[b][k]
			if len(acts) == 0 {
				continue
			}
			best := inf
			bestDist := inf
			var bestAction *spec.Action
			for _, a := range acts {
				v := branchValue(b, n, a)
				d := distance[k+"|"+actionFingerprint(a)]
				if v < best || (v == best && d < bestDist) {
					best = v
					bestAction = a
					bestDist = d
				}
			}
			p.choice[k][b] = &policyChoice{action: bestAction, value: best}
		}
	}

	if V[B][p.rootKey] >= finiteBound {
		return errors.New("根信念在完整预算下不存在可达迁移完成的策略")
	}
	p.values = V
	p.legal = legal
	return nil
}

// RootKey 返回根信念指纹。
func (p *Planner) RootKey() string { return p.rootKey }

// Nodes 返回信念数量（供测试与诊断）。
func (p *Planner) Nodes() int { return len(p.nodes) }

// ConcreteStates 返回枚举到的具体状态数量。
func (p *Planner) ConcreteStates() int { return len(p.stateOf) }

// WorstCaseBurns 返回根信念在完整预算下的最小最坏烧录次数。
func (p *Planner) WorstCaseBurns() int {
	return p.values[p.model.chip.MaxBurns][p.rootKey]
}
