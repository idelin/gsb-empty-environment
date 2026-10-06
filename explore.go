package rescheck

import (
	"fmt"
	"strconv"
	"strings"
)

// node is one global configuration: every resource's state plus every
// script's program counter.
type node struct {
	states []string
	pcs    []int
}

// edge records how a node was reached, for trace reconstruction.
type edge struct {
	from string
	step Step
}

func keyOf(n node) string {
	var b strings.Builder
	for _, st := range n.states {
		b.WriteString(st)
		b.WriteByte(0)
	}
	b.WriteByte(1)
	for _, pc := range n.pcs {
		b.WriteString(strconv.Itoa(pc))
		b.WriteByte(0)
	}
	return b.String()
}

func (s *Spec) budget() int {
	if s.Budget <= 0 {
		return DefaultBudget
	}
	return s.Budget
}

// Check enumerates all order-preserving interleavings of the scripts with
// breadth-first search, so any counterexample returned is a shortest one.
// A counterexample is a terminated configuration (all scripts done) where
// at least one resource is not in a final (released) state. Every found
// trace is independently replay-verified before being reported. If the
// exploration budget is exhausted first, the verdict is unknown.
func (s *Spec) Check() (*Report, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	ms := s.machines()
	ridx := s.resourceIndex()
	budget := s.budget()

	start := node{
		states: make([]string, len(s.Resources)),
		pcs:    make([]int, len(s.Scripts)),
	}
	for i, r := range s.Resources {
		start.states[i] = r.Initial
	}

	visited := map[string]bool{keyOf(start): true}
	parent := map[string]edge{}
	queue := []node{start}
	explored := 0

	for len(queue) > 0 {
		if explored >= budget {
			return &Report{
				Status:   StatusUnknown,
				Explored: explored,
				Reason:   fmt.Sprintf("budget of %d explored states exhausted", budget),
			}, nil
		}
		cur := queue[0]
		queue = queue[1:]
		explored++

		done := true
		for i := range s.Scripts {
			if cur.pcs[i] < len(s.Scripts[i].Actions) {
				done = false
				break
			}
		}
		if done {
			if leaked := leakedResources(s, cur.states); len(leaked) > 0 {
				trace := reconstruct(parent, cur)
				if err := s.Verify(trace); err != nil {
					return nil, fmt.Errorf("internal error: replay rejected found trace: %v", err)
				}
				return &Report{
					Status:   StatusViolated,
					Trace:    trace,
					Leaked:   leaked,
					Explored: explored,
					Reason:   "terminated with unreleased resources: " + strings.Join(leaked, ", "),
				}, nil
			}
			continue
		}

		for i := range s.Scripts {
			sc := &s.Scripts[i]
			if cur.pcs[i] >= len(sc.Actions) {
				continue
			}
			a := sc.Actions[cur.pcs[i]]
			ri := ridx[a.Resource]
			next, result := ms[ri].step(cur.states[ri], a.Op)

			succ := node{
				states: append(append([]string{}, cur.states[:ri]...), append([]string{next}, cur.states[ri+1:]...)...),
				pcs:    append([]int{}, cur.pcs...),
			}
			succ.pcs[i]++
			k := keyOf(succ)
			if visited[k] {
				continue
			}
			visited[k] = true
			parent[k] = edge{
				from: keyOf(cur),
				step: Step{
					Script:   sc.Name,
					Resource: a.Resource,
					Op:       a.Op,
					Result:   result,
					From:     cur.states[ri],
					To:       next,
				},
			}
			queue = append(queue, succ)
		}
	}

	return &Report{
		Status:   StatusSafe,
		Explored: explored,
		Reason:   "every interleaving terminates with all resources released",
	}, nil
}

// leakedResources lists resources not in a final state.
func leakedResources(s *Spec, states []string) []string {
	ms := s.machines()
	var leaked []string
	for i, r := range s.Resources {
		if !ms[i].final[states[i]] {
			leaked = append(leaked, r.Name+"@"+states[i])
		}
	}
	return leaked
}

// reconstruct walks parent edges back to the start node.
func reconstruct(parent map[string]edge, end node) []Step {
	var rev []Step
	k := keyOf(end)
	for {
		e, ok := parent[k]
		if !ok {
			break
		}
		rev = append(rev, e.step)
		k = e.from
	}
	trace := make([]Step, len(rev))
	for i := range rev {
		trace[len(rev)-1-i] = rev[i]
	}
	return trace
}
