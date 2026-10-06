package plancmp

import (
	"fmt"
	"sort"
)

// ExploreNode is one visited node of the search space: a state together with
// the set of operations executed to reach it.
type ExploreNode struct {
	State State
	Done  []string
}

// Result is the outcome of Reconcile.
type Result struct {
	// Reachable reports whether the goal can be reached from the committed
	// state using the available operations.
	Reachable bool
	// Steps is the minimal supplementary operation sequence, in execution
	// order. Empty when the goal is already satisfied or unreachable.
	Steps []string
	// Explored lists the complete search range (every visited state and the
	// operations used to reach it). It is populated only when the goal is
	// unreachable, so callers can inspect how far the search could get.
	Explored []ExploreNode
}

// Reconcile merges the old and new operation groups, replays the executed
// prefix from the initial state, and searches for the minimal supplementary
// operation set that reaches the goal. Executed operations are irrevocable:
// any supplementary operation whose effects would reset a committed variable
// is excluded from the search.
func Reconcile(old, newPlan *Plan, initial State, executed []string, goal Assign) (*Result, error) {
	p, err := MergePlans(old, newPlan)
	if err != nil {
		return nil, err
	}
	return p.reconcile(initial, executed, goal)
}

type searchKey struct {
	state State
	done  uint64
}

type searchNode struct {
	key    searchKey
	parent int // index into the nodes slice, -1 for the root
	op     string
}

func (p *Plan) reconcile(initial State, executed []string, goal Assign) (*Result, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if err := checkAssign(goal); err != nil {
		return nil, fmt.Errorf("plancmp: goal: %v", err)
	}

	current, commits, err := p.replay(initial, executed)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(p.Ops))
	for name := range p.Ops {
		names = append(names, name)
	}
	sort.Strings(names)
	index := make(map[string]int, len(names))
	depMask := make([]uint64, len(names))
	for i, name := range names {
		index[name] = i
		for _, dep := range p.Ops[name].Deps {
			depMask[i] |= 1 << uint(index[dep])
		}
	}

	var rootDone uint64
	for _, name := range executed {
		rootDone |= 1 << uint(index[name])
	}

	if goal.satisfiedBy(current) {
		return &Result{Reachable: true}, nil
	}

	root := searchNode{key: searchKey{state: current, done: rootDone}, parent: -1}
	nodes := []searchNode{root}
	visited := map[searchKey]bool{root.key: true}

	for head := 0; head < len(nodes); head++ {
		n := nodes[head]
		for i, name := range names {
			if n.key.done&(1<<uint(i)) != 0 {
				continue
			}
			if depMask[i]&^n.key.done != 0 {
				continue
			}
			op := p.Ops[name]
			if !op.Pre.satisfiedBy(n.key.state) {
				continue
			}
			if conflictsWith(op.Eff, commits) {
				continue
			}
			next := searchKey{state: n.key.state.apply(op.Eff), done: n.key.done | 1<<uint(i)}
			if visited[next] {
				continue
			}
			visited[next] = true
			nodes = append(nodes, searchNode{key: next, parent: head, op: name})
			if goal.satisfiedBy(next.state) {
				steps := reconstruct(nodes, len(nodes)-1)
				if err := p.Verify(initial, executed, steps, goal); err != nil {
					return nil, fmt.Errorf("plancmp: replay verification failed: %v", err)
				}
				return &Result{Reachable: true, Steps: steps}, nil
			}
		}
	}

	explored := make([]ExploreNode, len(nodes))
	for i, n := range nodes {
		explored[i] = ExploreNode{State: n.key.state, Done: maskNames(n.key.done, names)}
	}
	return &Result{Reachable: false, Explored: explored}, nil
}

func reconstruct(nodes []searchNode, last int) []string {
	var rev []string
	for i := last; nodes[i].parent >= 0; i = nodes[i].parent {
		rev = append(rev, nodes[i].op)
	}
	steps := make([]string, len(rev))
	for i, name := range rev {
		steps[len(rev)-1-i] = name
	}
	return steps
}

func maskNames(mask uint64, names []string) []string {
	var out []string
	for i, name := range names {
		if mask&(1<<uint(i)) != 0 {
			out = append(out, name)
		}
	}
	return out
}
