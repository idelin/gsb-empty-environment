package verifier

import (
	"fmt"
	"sort"

	"prescreen.local/ddd-10554-v3/spec"
)

type vChoice struct {
	action *spec.Action
	value  int
}

type solution struct {
	values [](map[string]int)
	choice map[string]map[int]*vChoice
	legal  []map[string][]*spec.Action
	root   string
}

// solve 独立执行最小最坏烧录次数的值迭代。
func (en *enumeration) solve() (*solution, error) {
	e := en.engine
	B := e.chip.MaxBurns
	finiteBound := len(en.stateOf)*B + 1
	inf := finiteBound + 1

	if !en.terminalReachable() {
		return nil, fmt.Errorf("核验器：不存在从根到终止信念的安全路径")
	}

	keys := make([]string, 0, len(en.nodes))
	for k := range en.nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	legal := make([]map[string][]*spec.Action, B+1)
	values := make([]map[string]int, B+1)
	edgeActions := func(n *vBelief, budget int) []*spec.Action {
		var out []*spec.Action
		for _, a := range e.commonActions(n, budget) {
			if _, ok := n.edges[actionKey(a)]; ok {
				out = append(out, a)
			}
		}
		return out
	}
	for b := 0; b <= B; b++ {
		legal[b] = map[string][]*spec.Action{}
		values[b] = map[string]int{}
		for _, k := range keys {
			n := en.nodes[k]
			if e.terminalBelief(n) {
				values[b][k] = 0
				legal[b][k] = nil
			} else {
				values[b][k] = finiteBound
				legal[b][k] = edgeActions(n, b)
			}
		}
	}

	branch := func(b int, n *vBelief, a *spec.Action) int {
		ak := actionKey(a)
		cost := 0
		if a.Kind == spec.ActionBurn {
			cost = 1
		}
		ackK, _ := spec.ObservationKey(&spec.Observation{Kind: spec.ObsAck})
		worst := 0
		for obsFP, child := range n.edges[ak] {
			nextBudget := B
			if obsFP == ackK {
				nextBudget = b - cost
			}
			if v := cost + values[nextBudget][child]; v > worst {
				worst = v
			}
		}
		return worst
	}

	for {
		changed := false
		for _, k := range keys {
			n := en.nodes[k]
			if e.terminalBelief(n) {
				continue
			}
			for b := 0; b <= B; b++ {
				acts := legal[b][k]
				if len(acts) == 0 {
					continue
				}
				best := inf
				for _, a := range acts {
					if v := branch(b, n, a); v < best {
						best = v
					}
				}
				if best < values[b][k] {
					values[b][k] = best
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	choice := map[string]map[int]*vChoice{}
	for _, k := range keys {
		n := en.nodes[k]
		if e.terminalBelief(n) {
			continue
		}
		choice[k] = map[int]*vChoice{}
		for b := 0; b <= B; b++ {
			acts := legal[b][k]
			if len(acts) == 0 {
				continue
			}
			best := inf
			var bestA *spec.Action
			for _, a := range acts {
				if v := branch(b, n, a); v < best {
					best = v
					bestA = a
				}
			}
			choice[k][b] = &vChoice{action: bestA, value: best}
		}
	}

	return &solution{values: values, choice: choice, legal: legal, root: en.root}, nil
}

// RootWorstCase 返回独立求解得到的根信念最小最坏烧录次数。
func (s *solution) RootWorstCase() int {
	B := len(s.values) - 1
	return s.values[B][s.root]
}
