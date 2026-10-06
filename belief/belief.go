// Package belief solves partially observable planning problems over a
// finite set of worlds.
//
// The agent cannot tell worlds apart directly: each world emits an
// observation, and worlds with equal observations are indistinguishable.
// Action outcomes may be non-deterministic and are only revealed after
// execution, through the observation of the world actually reached. A
// policy must therefore choose the same action for every world sharing an
// observation history, and it must bring every possible world safely to a
// goal world.
//
// Solve performs a full belief-state search (exact value iteration over the
// finite space of belief states) and returns a policy tree whose worst-case
// number of steps is minimal, or the set of unsolvable belief states when
// no policy exists. Verify independently replays a policy tree over the
// whole belief-state space to confirm it.
package belief

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// World is one possible state of the environment.
type World struct {
	Name   string
	Obs    string // observation emitted while in this world
	Goal   bool   // a goal world: the run may end here
	Unsafe bool   // an unsafe world: must never be entered
}

// Action maps each world to its possible successor worlds. An action is
// applicable in a belief state only when it is defined (non-empty) for
// every world in that belief state.
type Action struct {
	Name string
	Next map[int][]int
}

// Problem is a partially observable planning instance.
type Problem struct {
	Worlds  []World
	Actions []Action
	Initial []int // initial belief state: indices into Worlds
}

// Policy is a tree keyed by observation history: the agent executes
// Action, observes the outcome, and descends into Branches[observation].
// A leaf (Action == "") is valid only on belief states that are all goal.
type Policy struct {
	Action   string
	Branches map[string]*Policy
}

// Result is the outcome of Solve.
type Result struct {
	Policy     *Policy // nil when unsolvable
	WorstCase  int     // minimal worst-case number of steps (0 if already at goal)
	Unsolvable [][]int // belief states with no safe policy, when unsolvable
}

// belief is a canonical (sorted, deduplicated) set of world indices.
type belief []int

func normalize(ws []int) belief {
	s := append([]int(nil), ws...)
	sort.Ints(s)
	out := s[:0]
	for i, w := range s {
		if i == 0 || w != s[i-1] {
			out = append(out, w)
		}
	}
	return out
}

func key(b belief) string {
	parts := make([]string, len(b))
	for i, w := range b {
		parts[i] = strconv.Itoa(w)
	}
	return strings.Join(parts, ",")
}

func (p Problem) allGoal(b belief) bool {
	for _, w := range b {
		if !p.Worlds[w].Goal {
			return false
		}
	}
	return true
}

func (p Problem) hasUnsafe(b belief) bool {
	for _, w := range b {
		if p.Worlds[w].Unsafe {
			return true
		}
	}
	return false
}

// branches computes, for one action applied to a belief state, the
// successor belief states grouped by the observation that reveals them.
// The second result is false when the action is not applicable to every
// world in the belief state.
func (p Problem) branches(b belief, a Action) (map[string]belief, bool) {
	groups := map[string]map[int]bool{}
	for _, w := range b {
		nexts, ok := a.Next[w]
		if !ok || len(nexts) == 0 {
			return nil, false
		}
		for _, n := range nexts {
			if n < 0 || n >= len(p.Worlds) {
				return nil, false
			}
			obs := p.Worlds[n].Obs
			if groups[obs] == nil {
				groups[obs] = map[int]bool{}
			}
			groups[obs][n] = true
		}
	}
	out := make(map[string]belief, len(groups))
	for obs, set := range groups {
		b := belief{}
		for w := range set {
			b = append(b, w)
		}
		out[obs] = normalize(b)
	}
	return out, true
}

// reachable enumerates every belief state reachable from the initial one.
func (p Problem) reachable() []belief {
	seen := map[string]belief{key(normalize(p.Initial)): normalize(p.Initial)}
	queue := []belief{normalize(p.Initial)}
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		for _, a := range p.Actions {
			brs, ok := p.branches(b, a)
			if !ok {
				continue
			}
			for _, nb := range brs {
				if _, ok := seen[key(nb)]; !ok {
					seen[key(nb)] = nb
					queue = append(queue, nb)
				}
			}
		}
	}
	out := make([]belief, 0, len(seen))
	for _, b := range seen {
		out = append(out, b)
	}
	return out
}

const inf = int(^uint(0) >> 1)

// Solve searches the full belief-state space for a policy that brings every
// world in the initial belief state to a goal world without ever entering
// an unsafe world, minimizing the worst-case number of steps. When no such
// policy exists it returns the reachable belief states that are unsolvable.
func Solve(p Problem) Result {
	beliefs := p.reachable()
	cost := make(map[string]int, len(beliefs))
	choice := make(map[string]string, len(beliefs))
	for _, b := range beliefs {
		if p.allGoal(b) && !p.hasUnsafe(b) {
			cost[key(b)] = 0
		} else {
			cost[key(b)] = inf
		}
	}
	// Value iteration over the finite AND/OR hypergraph of belief states:
	// cost(b) = 1 + min over actions of max over observation branches.
	// Costs only decrease, so this converges in at most len(beliefs) rounds.
	for changed := true; changed; {
		changed = false
		for _, b := range beliefs {
			k := key(b)
			if cost[k] == 0 {
				continue
			}
			best, bestAct := inf, ""
			for _, a := range p.Actions {
				brs, ok := p.branches(b, a)
				if !ok {
					continue
				}
				c := 0
				for _, nb := range brs {
					bc := cost[key(nb)]
					if p.hasUnsafe(nb) {
						bc = inf
					}
					if bc > c {
						c = bc
					}
				}
				if c < inf {
					c++
				}
				if c < best {
					best, bestAct = c, a.Name
				}
			}
			if best < cost[k] {
				cost[k] = best
				choice[k] = bestAct
				changed = true
			}
		}
	}
	init := normalize(p.Initial)
	if cost[key(init)] == inf {
		var uns [][]int
		for _, b := range beliefs {
			if cost[key(b)] == inf {
				uns = append(uns, []int(b))
			}
		}
		sort.Slice(uns, func(i, j int) bool { return key(uns[i]) < key(uns[j]) })
		return Result{Unsolvable: uns}
	}
	return Result{
		Policy:    p.extract(init, cost, choice),
		WorstCase: cost[key(init)],
	}
}

// extract builds the policy tree by following the best action at each
// belief state. Branch costs are strictly smaller than the parent's, so
// the recursion terminates.
func (p Problem) extract(b belief, cost map[string]int, choice map[string]string) *Policy {
	if p.allGoal(b) {
		return &Policy{}
	}
	actName := choice[key(b)]
	var act Action
	for _, a := range p.Actions {
		if a.Name == actName {
			act = a
			break
		}
	}
	brs, _ := p.branches(b, act)
	node := &Policy{Action: actName, Branches: make(map[string]*Policy, len(brs))}
	for obs, nb := range brs {
		node.Branches[obs] = p.extract(nb, cost, choice)
	}
	return node
}

// Verify independently replays a policy tree over the full belief-state
// space reachable from the initial belief state and returns the worst-case
// number of steps to a goal leaf. It fails when the policy chooses an
// inapplicable action, misses an observation branch, adds a spurious one,
// enters an unsafe world, ends on a non-goal belief state, or loops.
func Verify(p Problem, pol *Policy) (int, error) {
	if pol == nil {
		return 0, fmt.Errorf("belief: nil policy")
	}
	visiting := map[*Policy]bool{}
	worst := 0
	var walk func(b belief, node *Policy, depth int) error
	walk = func(b belief, node *Policy, depth int) error {
		if node == nil {
			return fmt.Errorf("belief: missing policy for belief state %v", []int(b))
		}
		if p.hasUnsafe(b) {
			return fmt.Errorf("belief: policy enters unsafe belief state %v", []int(b))
		}
		if p.allGoal(b) {
			if node.Action != "" || len(node.Branches) != 0 {
				return fmt.Errorf("belief: goal belief state %v must be a leaf", []int(b))
			}
			if depth > worst {
				worst = depth
			}
			return nil
		}
		if node.Action == "" {
			return fmt.Errorf("belief: no action chosen for non-goal belief state %v", []int(b))
		}
		if visiting[node] {
			return fmt.Errorf("belief: policy contains a cycle at action %q", node.Action)
		}
		var act Action
		found := false
		for _, a := range p.Actions {
			if a.Name == node.Action {
				act, found = a, true
				break
			}
		}
		if !found {
			return fmt.Errorf("belief: unknown action %q", node.Action)
		}
		brs, ok := p.branches(b, act)
		if !ok {
			return fmt.Errorf("belief: action %q not applicable to belief state %v", node.Action, []int(b))
		}
		if len(node.Branches) != len(brs) {
			return fmt.Errorf("belief: action %q on %v: policy has %d branches, model has %d",
				node.Action, []int(b), len(node.Branches), len(brs))
		}
		visiting[node] = true
		defer delete(visiting, node)
		for obs, nb := range brs {
			child, ok := node.Branches[obs]
			if !ok {
				return fmt.Errorf("belief: action %q on %v: missing branch for observation %q",
					node.Action, []int(b), obs)
			}
			if err := walk(nb, child, depth+1); err != nil {
				return err
			}
		}
		for obs := range node.Branches {
			if _, ok := brs[obs]; !ok {
				return fmt.Errorf("belief: action %q on %v: spurious branch for observation %q",
					node.Action, []int(b), obs)
			}
		}
		return nil
	}
	if err := walk(normalize(p.Initial), pol, 0); err != nil {
		return 0, err
	}
	return worst, nil
}
