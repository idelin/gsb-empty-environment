package netsec

// interpOutcome aggregates the bounded concrete interpreter run.
type interpOutcome struct {
	perAccess []ConcreteResult
	truncated bool
}

type interpState struct {
	node int
	env  []int64
}

func checkedAdd(a, b int64) (int64, bool) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, false
	}
	return s, true
}

func checkedSub(a, b int64) (int64, bool) {
	d := a - b
	if (b > 0 && d > a) || (b < 0 && d < a) {
		return 0, false
	}
	return d, true
}

// interpret explores concrete executions from the entry node with all
// variables zero, bounded by opt.InterpSteps states and opt.InterpPaths
// completed paths, and checks every registered array access concretely.
// It validates the static proofs: a proven access must never violate.
func (g *Graph) interpret(opt Options) interpOutcome {
	out := interpOutcome{perAccess: make([]ConcreteResult, len(g.accesses))}
	if opt.InterpSteps <= 0 || opt.InterpPaths <= 0 {
		out.truncated = true
		return out
	}
	byNode := make(map[int][]int)
	for i, a := range g.accesses {
		byNode[a.Node] = append(byNode[a.Node], i)
	}

	steps, paths := 0, 0
	stack := []interpState{{node: g.Entry(), env: make([]int64, len(g.vars))}}
	for len(stack) > 0 {
		if steps >= opt.InterpSteps || paths >= opt.InterpPaths {
			out.truncated = true
			break
		}
		st := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		steps++

		for _, ai := range byNode[st.node] {
			a := g.accesses[ai]
			cr := &out.perAccess[ai]
			idx, ok := checkedAdd(st.env[a.Index], a.Offset)
			if !ok {
				cr.Violations++
				continue
			}
			if !cr.Reached {
				cr.Reached = true
				cr.MinIndex, cr.MaxIndex = idx, idx
			} else {
				if idx < cr.MinIndex {
					cr.MinIndex = idx
				}
				if idx > cr.MaxIndex {
					cr.MaxIndex = idx
				}
			}
			if idx < 0 || idx >= g.arrays[a.Array] {
				cr.Violations++
			}
		}

		successors := 0
		for _, ei := range g.out[st.node] {
			e := &g.edges[ei]
			switch e.Kind {
			case AssignEdge:
				v, ok := checkedAdd(st.env[e.X], e.C)
				if !ok {
					continue // concrete overflow: abandon path
				}
				env := make([]int64, len(st.env))
				copy(env, st.env)
				env[e.X] = v
				stack = append(stack, interpState{node: e.To, env: env})
				successors++
			case GuardEdge:
				d, ok := checkedSub(st.env[e.X], st.env[e.Y])
				if !ok {
					continue
				}
				if d >= e.Lo && d <= e.Hi {
					stack = append(stack, interpState{node: e.To, env: st.env})
					successors++
				}
			}
		}
		if successors == 0 {
			paths++
		}
	}
	return out
}
