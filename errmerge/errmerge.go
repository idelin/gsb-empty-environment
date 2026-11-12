// Package errmerge computes the finest merging of protocol error outputs
// that keeps secret states indistinguishable under public observation.
//
// A security protocol may reject a request with several distinct error
// categories. If the emitted category depends on which secret state the
// machine occupies, a client can learn secret information from the error
// alone. Given a Mealy machine and a grouping of its secret states,
// Partition derives the finest sufficient equivalence over error
// outputs: two errors are merged only when some pair of secret states
// (or their observable successors, reached by the same public input
// string) can emit them in the same position. Normal success outputs
// are never merged. When no error-only merging can hide the divergence,
// Partition returns a witness input string instead.
//
// Check verifies the result by enumerating all input strings up to a
// bounded length, both before merging (identity mapping) and after.
package errmerge

import (
	"fmt"
	"sort"
)

type (
	// State names a machine state.
	State string
	// Input names a single input symbol.
	Input string
	// Output names an observable output (error category or normal reply).
	Output string
)

// Transition is one labeled edge of the machine.
type Transition struct {
	Next State
	Out  Output
}

// Machine is a deterministic finite-state Mealy machine.
type Machine struct {
	States []State
	Inputs []Input
	// Trans[s][a] is the transition of state s on input a. Every state
	// must define a transition for every input.
	Trans map[State]map[Input]Transition
}

// Validate reports whether the machine is total and well formed.
func (m *Machine) Validate() error {
	if len(m.Inputs) == 0 {
		return fmt.Errorf("errmerge: machine has no inputs")
	}
	known := make(map[State]bool, len(m.States))
	for _, s := range m.States {
		if known[s] {
			return fmt.Errorf("errmerge: duplicate state %q", s)
		}
		known[s] = true
	}
	for _, s := range m.States {
		row, ok := m.Trans[s]
		if !ok {
			return fmt.Errorf("errmerge: state %q has no transitions", s)
		}
		for _, a := range m.Inputs {
			t, ok := row[a]
			if !ok {
				return fmt.Errorf("errmerge: state %q missing transition on input %q", s, a)
			}
			if !known[t.Next] {
				return fmt.Errorf("errmerge: state %q on input %q leads to unknown state %q", s, a, t.Next)
			}
		}
	}
	return nil
}

// Witness describes a leak that merging errors alone cannot remove.
type Witness struct {
	// States is the divergent pair of machine states, reached from a
	// same-group secret pair by feeding Prefix.
	States [2]State
	// Prefix is the public input string leading to States.
	Prefix []Input
	// Input is the next input on which the two states observably differ.
	Input Input
	// Outputs are the conflicting outputs; at least one is a non-error
	// output, so no error merging can equate them.
	Outputs [2]Output
}

// String renders the witness as a compact human-readable summary.
func (w *Witness) String() string {
	return fmt.Sprintf("states (%s,%s) diverge on %q after %q: %q vs %q",
		w.States[0], w.States[1], w.Input, w.Prefix, w.Outputs[0], w.Outputs[1])
}

// Result is the outcome of Partition. Exactly one of the two outcomes
// holds: Witness != nil (no error-only merging suffices), or Merged and
// Classes describe the finest sufficient error partition.
type Result struct {
	// Merged maps every error output of the machine to the canonical
	// representative of its class. Non-error outputs never appear.
	Merged map[Output]Output
	// Classes lists all error equivalence classes, each sorted, sorted
	// by first element. Singletons are included.
	Classes [][]Output
	// Witness is non-nil when merging errors alone cannot hide a leak.
	Witness *Witness
}

// Leak is a counterexample found by Check: a short public input string
// on which two same-group secret states produce different observations.
type Leak struct {
	States   [2]State
	Inputs   []Input
	Position int // index of the first differing output
	Outputs  [2]Output
}

func (l *Leak) String() string {
	return fmt.Sprintf("states (%s,%s) differ on %q at step %d: %q vs %q",
		l.States[0], l.States[1], l.Inputs, l.Position, l.Outputs[0], l.Outputs[1])
}

type unionFind struct {
	parent map[Output]Output
}

func newUnionFind() *unionFind { return &unionFind{parent: make(map[Output]Output)} }

func (u *unionFind) find(x Output) Output {
	p, ok := u.parent[x]
	if !ok {
		u.parent[x] = x
		return x
	}
	if p != x {
		u.parent[x] = u.find(p)
	}
	return u.parent[x]
}

func (u *unionFind) union(a, b Output) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if rb < ra {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
}

// pairNode is a node of the BFS over pairs of machine states.
type pairNode struct {
	a, b   State
	parent *pairNode
	via    Input // input taken from parent to reach this node
	depth  int
}

// path returns the input string leading from a seed pair to n.
func (n *pairNode) path() []Input {
	var rev []Input
	for p := n; p.parent != nil; p = p.parent {
		rev = append(rev, p.via)
	}
	out := make([]Input, len(rev))
	for i, v := range rev {
		out[len(rev)-1-i] = v
	}
	return out
}

// pairBFS is a breadth-first search over unordered state pairs, seeded
// with every pair of distinct states drawn from the same secret group.
type pairBFS struct {
	m       *Machine
	visited map[[2]State]bool
	queue   []*pairNode
}

func newPairBFS(m *Machine, groups [][]State) (*pairBFS, error) {
	known := make(map[State]bool, len(m.States))
	for _, s := range m.States {
		known[s] = true
	}
	b := &pairBFS{m: m, visited: make(map[[2]State]bool)}
	for gi, g := range groups {
		for i := 0; i < len(g); i++ {
			if !known[g[i]] {
				return nil, fmt.Errorf("errmerge: group %d contains unknown state %q", gi, g[i])
			}
			for j := i + 1; j < len(g); j++ {
				b.push(&pairNode{a: g[i], b: g[j]})
			}
		}
	}
	return b, nil
}

func (b *pairBFS) push(n *pairNode) {
	if n.a == n.b {
		return
	}
	key := [2]State{n.a, n.b}
	rev := [2]State{n.b, n.a}
	if b.visited[key] || b.visited[rev] {
		return
	}
	b.visited[key] = true
	b.queue = append(b.queue, n)
}

func (b *pairBFS) pop() (*pairNode, bool) {
	if len(b.queue) == 0 {
		return nil, false
	}
	n := b.queue[0]
	b.queue = b.queue[1:]
	return n, true
}

// Partition computes the finest merging of error outputs such that, for
// every pair of secret states in the same group, every public input
// string yields identical observable output sequences. Success and
// other non-error outputs are never merged; if a required merge would
// involve one, Partition returns a Witness for the shortest divergent
// string.
func Partition(m *Machine, groups [][]State, isError func(Output) bool) (*Result, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	uf := newUnionFind()
	for _, s := range m.States {
		for _, a := range m.Inputs {
			if o := m.Trans[s][a].Out; isError(o) {
				uf.find(o)
			}
		}
	}
	bfs, err := newPairBFS(m, groups)
	if err != nil {
		return nil, err
	}
	for {
		n, ok := bfs.pop()
		if !ok {
			break
		}
		for _, inp := range m.Inputs {
			ta := m.Trans[n.a][inp]
			tb := m.Trans[n.b][inp]
			ea, eb := isError(ta.Out), isError(tb.Out)
			switch {
			case ea && eb:
				uf.union(ta.Out, tb.Out)
			case ta.Out != tb.Out:
				return &Result{Witness: &Witness{
					States:  [2]State{n.a, n.b},
					Prefix:  n.path(),
					Input:   inp,
					Outputs: [2]Output{ta.Out, tb.Out},
				}}, nil
			}
			bfs.push(&pairNode{a: ta.Next, b: tb.Next, parent: n, via: inp, depth: n.depth + 1})
		}
	}
	merged := make(map[Output]Output, len(uf.parent))
	byRep := make(map[Output][]Output)
	for o := range uf.parent {
		r := uf.find(o)
		merged[o] = r
		byRep[r] = append(byRep[r], o)
	}
	classes := make([][]Output, 0, len(byRep))
	for _, members := range byRep {
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		classes = append(classes, members)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i][0] < classes[j][0] })
	return &Result{Merged: merged, Classes: classes}, nil
}

// Check enumerates every input string up to length maxLen and reports
// the first (shortest) string on which two same-group secret states
// produce different observations, or nil if none exists. merge maps
// outputs to their merged representatives; pass nil for the pre-merge
// machine and Result.Merged for the post-merge machine.
func Check(m *Machine, groups [][]State, merge map[Output]Output, maxLen int) (*Leak, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if maxLen < 1 {
		return nil, fmt.Errorf("errmerge: maxLen must be positive")
	}
	look := func(o Output) Output {
		if r, ok := merge[o]; ok {
			return r
		}
		return o
	}
	bfs, err := newPairBFS(m, groups)
	if err != nil {
		return nil, err
	}
	for {
		n, ok := bfs.pop()
		if !ok {
			return nil, nil
		}
		if n.depth >= maxLen {
			continue
		}
		for _, inp := range m.Inputs {
			ta := m.Trans[n.a][inp]
			tb := m.Trans[n.b][inp]
			if oa, ob := look(ta.Out), look(tb.Out); oa != ob {
				return &Leak{
					States:   [2]State{n.a, n.b},
					Inputs:   append(n.path(), inp),
					Position: n.depth,
					Outputs:  [2]Output{oa, ob},
				}, nil
			}
			bfs.push(&pairNode{a: ta.Next, b: tb.Next, parent: n, via: inp, depth: n.depth + 1})
		}
	}
}
