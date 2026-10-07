package plancmp

import "sort"

// node is a BFS vertex: the state plus the set of plan operations applied
// so far (bit i corresponds to plan.Ops[i]).
type node struct {
	state   State
	applied uint64
}

// Diff compares the executed prefix of the old plan against the new plan and
// returns the minimal supplementary operation set that continues from the
// committed state to the new goal. When the goal is unreachable, the result
// carries the complete search range in Explored.
func Diff(in *Input) (*Result, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	plan := in.New
	index := make(map[string]int, len(plan.Ops))
	for i, op := range plan.Ops {
		index[op.Name] = i
	}
	executed := make(map[string]bool, len(in.Executed))
	for _, name := range in.Executed {
		executed[name] = true
	}

	// Executed operations that are also part of the new plan count as
	// already applied; they can never be added again.
	var startApplied uint64
	for name := range executed {
		if i, ok := index[name]; ok {
			startApplied |= 1 << uint(i)
		}
	}
	full := uint64(1)<<uint(len(plan.Ops)) - 1
	lockedOn := in.Committed & in.Initial
	lockedOff := in.Committed &^ in.Initial

	type edge struct {
		prev node
		op   int
	}
	start := node{state: in.Initial, applied: startApplied}
	visited := map[node]bool{start: true}
	parent := make(map[node]edge)
	reached := map[State]bool{in.Initial: true}
	queue := []node{start}

	isGoal := func(n node) bool {
		return plan.goal(n.state) && (!plan.RequireAll || n.applied == full)
	}

	var goal node
	found := false
	for head := 0; head < len(queue) && !found; head++ {
		cur := queue[head]
		if isGoal(cur) {
			goal, found = cur, true
			break
		}
		for i := range plan.Ops {
			bit := uint64(1) << uint(i)
			if cur.applied&bit != 0 {
				continue
			}
			op := &plan.Ops[i]
			ready := true
			for _, dep := range op.Deps {
				if j, ok := index[dep]; ok {
					if cur.applied&(uint64(1)<<uint(j)) == 0 {
						ready = false
						break
					}
				}
				// Dependencies outside the plan are validated to be
				// executed operations, hence already satisfied.
			}
			if !ready {
				continue
			}
			if !appliesTo(op, cur.state) || !respectsCommitment(op, lockedOn, lockedOff) {
				continue
			}
			next := node{state: apply(op, cur.state), applied: cur.applied | bit}
			if visited[next] {
				continue
			}
			visited[next] = true
			parent[next] = edge{prev: cur, op: i}
			reached[next.state] = true
			queue = append(queue, next)
		}
	}

	if !found {
		explored := make([]State, 0, len(reached))
		for state := range reached {
			explored = append(explored, state)
		}
		sort.Slice(explored, func(i, j int) bool { return explored[i] < explored[j] })
		return &Result{Reachable: false, Explored: explored}, nil
	}

	// Reconstruct the minimal sequence by walking parents back to start.
	var added []string
	for cur := goal; cur != start; {
		e := parent[cur]
		added = append(added, plan.Ops[e.op].Name)
		cur = e.prev
	}
	for i, j := 0, len(added)-1; i < j; i, j = i+1, j-1 {
		added[i], added[j] = added[j], added[i]
	}

	// Replay the proposed sequence to prove no committed variable is
	// silently reset and every precondition and dependency holds.
	final, err := Replay(in.Initial, in.Committed, plan, in.Executed, added)
	if err != nil {
		return nil, err
	}
	return &Result{
		Reachable: true,
		Added:     added,
		Final:     final,
		Verified:  true,
	}, nil
}
