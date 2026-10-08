package topo

import "fmt"

// CycleError reports that constraints form a cycle among unordered tasks.
type CycleError struct{ Cycle []int }

func (e *CycleError) Error() string { return fmt.Sprintf("topo: constraint cycle %v", e.Cycle) }

// ContradictionError reports that a change would require reordering the
// already-executed prefix. Chain is the shortest contradiction chain: a
// closed loop whose first step is the offending new constraint and whose
// remaining steps are existing constraints or execution-order evidence.
type ContradictionError struct{ Chain []int }

func (e *ContradictionError) Error() string {
	return fmt.Sprintf("topo: constraint contradicts executed prefix, chain %v", e.Chain)
}

// Planner turns the deterministic sort into an incremental execution
// plan. The executed prefix is immutable: updates never reset progress,
// and any change that would reorder the past is rejected with the
// shortest contradiction chain.
type Planner struct {
	n        int
	deleted  map[int]bool
	edges    map[Edge]struct{}
	executed []int
	plan     []int // deterministic order of the not-yet-executed tasks
}

// NewPlanner builds a planner for tasks 0..n-1. It fails with CycleError
// if the constraints are cyclic.
func NewPlanner(n int, edges []Edge) (*Planner, error) {
	p := &Planner{
		n:       n,
		deleted: make(map[int]bool),
		edges:   make(map[Edge]struct{}, len(edges)),
	}
	for _, e := range edges {
		p.edges[e] = struct{}{}
	}
	if err := p.replan(); err != nil {
		return nil, err
	}
	return p, nil
}

// Plan returns the deterministic order of the not-yet-executed tasks.
func (p *Planner) Plan() []int { return append([]int(nil), p.plan...) }

// Executed returns the immutable executed prefix.
func (p *Planner) Executed() []int { return append([]int(nil), p.executed...) }

// Execute marks the first k planned tasks as executed and returns them.
// Executed tasks can never be moved again.
func (p *Planner) Execute(k int) ([]int, error) {
	if k < 0 || k > len(p.plan) {
		return nil, fmt.Errorf("topo: cannot execute %d of %d planned tasks", k, len(p.plan))
	}
	done := append([]int(nil), p.plan[:k]...)
	p.executed = append(p.executed, p.plan[:k]...)
	p.plan = p.plan[k:]
	return done, nil
}

// CheckAdd returns nil if adding constraint e is compatible with the
// executed prefix, otherwise the shortest contradiction chain.
func (p *Planner) CheckAdd(e Edge) []int {
	edges := p.cloneEdges()
	edges[e] = struct{}{}
	return p.contradiction(edges, e)
}

// Tx is an atomic transaction: task deletions plus constraint removals
// and additions are validated together and either all applied or none.
type Tx struct {
	Delete []int
	Remove []Edge
	Add    []Edge
}

// Apply validates tx against the executed prefix and the remaining
// graph, then commits it atomically. On any error the planner is left
// completely unchanged and the current plan stays valid.
func (p *Planner) Apply(tx Tx) error {
	deleted := make(map[int]bool, len(p.deleted)+len(tx.Delete))
	for t := range p.deleted {
		deleted[t] = true
	}
	inExecuted := make(map[int]bool, len(p.executed))
	for _, t := range p.executed {
		inExecuted[t] = true
	}
	for _, t := range tx.Delete {
		if t < 0 || t >= p.n {
			return fmt.Errorf("topo: delete unknown task %d", t)
		}
		if inExecuted[t] {
			return fmt.Errorf("topo: cannot delete executed task %d", t)
		}
		deleted[t] = true
	}

	edges := p.cloneEdges()
	for _, e := range tx.Remove {
		delete(edges, e)
	}
	for _, e := range tx.Add {
		if e.From < 0 || e.From >= p.n || e.To < 0 || e.To >= p.n {
			return fmt.Errorf("topo: constraint references unknown task %v", e)
		}
		edges[e] = struct{}{}
	}
	// Drop constraints touching deleted tasks.
	for e := range edges {
		if deleted[e.From] || deleted[e.To] {
			delete(edges, e)
		}
	}

	// Cycles purely among pending tasks are reported as such...
	plan, err := planRemaining(p.n, deleted, edges, p.executed)
	if err != nil {
		return err
	}

	// ...while any remaining loop through an added constraint must
	// involve the frozen past, i.e. contradict the executed prefix.
	for _, e := range tx.Add {
		if deleted[e.From] || deleted[e.To] {
			continue
		}
		if chain := p.contradiction(edges, e); chain != nil {
			return &ContradictionError{Chain: chain}
		}
	}

	p.deleted, p.edges, p.plan = deleted, edges, plan
	return nil
}

// contradiction returns the shortest closed chain proving that new edge
// e forces the executed prefix to be reordered, or nil. The chain starts
// with e and closes back to e.From through existing constraints and
// execution-order evidence (every executed task precedes every
// not-yet-executed task, and earlier executed tasks precede later ones).
func (p *Planner) contradiction(edges map[Edge]struct{}, e Edge) []int {
	adj := make(map[int][]int)
	for c := range edges {
		adj[c.From] = append(adj[c.From], c.To)
	}
	// Execution evidence: the frozen prefix precedes everything pending.
	pending := make(map[int]bool, len(p.plan))
	for _, t := range p.plan {
		pending[t] = true
	}
	for i, t := range p.executed {
		if i+1 < len(p.executed) {
			adj[t] = append(adj[t], p.executed[i+1])
		}
		for u := range pending {
			adj[t] = append(adj[t], u)
		}
	}
	path := shortestPath(adj, e.To, e.From)
	if path == nil {
		return nil
	}
	chain := make([]int, 0, len(path)+1)
	chain = append(chain, e.From)
	chain = append(chain, path...)
	return chain
}

// shortestPath BFS from src to dst, returning the node sequence
// src..dst inclusive, or nil when unreachable.
func shortestPath(adj map[int][]int, src, dst int) []int {
	if src == dst {
		return []int{src}
	}
	prev := map[int]int{src: -1}
	queue := []int{src}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range adj[u] {
			if _, seen := prev[v]; seen {
				continue
			}
			prev[v] = u
			if v == dst {
				path := []int{dst}
				for cur := u; cur != -1; cur = prev[cur] {
					path = append([]int{cur}, path...)
				}
				return path
			}
			queue = append(queue, v)
		}
	}
	return nil
}

// replan recomputes the deterministic order of the pending tasks.
func (p *Planner) replan() error {
	plan, err := planRemaining(p.n, p.deleted, p.edges, p.executed)
	if err != nil {
		return err
	}
	p.plan = plan
	return nil
}

// planRemaining computes the deterministic order of all tasks that are
// neither executed nor deleted, under the constraints among them.
func planRemaining(n int, deleted map[int]bool, edges map[Edge]struct{}, executed []int) ([]int, error) {
	done := make(map[int]bool, len(executed))
	for _, t := range executed {
		done[t] = true
	}
	idx := make([]int, 0, n)
	remap := make(map[int]int, n)
	for t := 0; t < n; t++ {
		if done[t] || deleted[t] {
			continue
		}
		remap[t] = len(idx)
		idx = append(idx, t)
	}
	sub := make([]Edge, 0, len(edges))
	for e := range edges {
		a, okA := remap[e.From]
		b, okB := remap[e.To]
		if okA && okB {
			sub = append(sub, Edge{a, b})
		}
	}
	res := Sort(len(idx), sub)
	if res.Cycle != nil {
		cycle := make([]int, len(res.Cycle))
		for i, t := range res.Cycle {
			cycle[i] = idx[t]
		}
		return nil, &CycleError{Cycle: cycle}
	}
	plan := make([]int, len(res.Order))
	for i, t := range res.Order {
		plan[i] = idx[t]
	}
	return plan, nil
}

func (p *Planner) cloneEdges() map[Edge]struct{} {
	edges := make(map[Edge]struct{}, len(p.edges))
	for e := range p.edges {
		edges[e] = struct{}{}
	}
	return edges
}

// Snapshot captures everything needed to recover the planner.
type Snapshot struct {
	N        int
	Executed []int
	Edges    []Edge
	Deleted  []int
}

// Snapshot exports the current state for persistence.
func (p *Planner) Snapshot() Snapshot {
	s := Snapshot{N: p.n, Executed: p.Executed(), Deleted: make([]int, 0, len(p.deleted))}
	for e := range p.edges {
		s.Edges = append(s.Edges, e)
	}
	for t := range p.deleted {
		s.Deleted = append(s.Deleted, t)
	}
	return s
}

// Verify recomputes the deterministic plan from scratch and checks the
// executed-prefix evidence: every constraint whose endpoints both
// executed must agree with the recorded execution order, and no executed
// task may be deleted. It returns nil when the state is consistent.
func (p *Planner) Verify() error {
	if err := verifyPrefix(p.n, p.edges, p.deleted, p.executed); err != nil {
		return err
	}
	fresh, err := planRemaining(p.n, p.deleted, p.edges, p.executed)
	if err != nil {
		return err
	}
	if !equalInts(fresh, p.plan) {
		return fmt.Errorf("topo: plan %v diverges from full re-sort %v", p.plan, fresh)
	}
	return nil
}

// Recover rebuilds a planner from a snapshot and cross-checks it with a
// full sort plus the executed-prefix evidence, so a corrupted log is
// rejected instead of silently resetting progress.
func Recover(s Snapshot) (*Planner, error) {
	p := &Planner{
		n:        s.N,
		deleted:  make(map[int]bool, len(s.Deleted)),
		edges:    make(map[Edge]struct{}, len(s.Edges)),
		executed: append([]int(nil), s.Executed...),
	}
	for _, t := range s.Deleted {
		p.deleted[t] = true
	}
	for _, e := range s.Edges {
		p.edges[e] = struct{}{}
	}
	if err := verifyPrefix(p.n, p.edges, p.deleted, p.executed); err != nil {
		return nil, err
	}
	if err := p.replan(); err != nil {
		return nil, err
	}
	if err := p.Verify(); err != nil {
		return nil, err
	}
	return p, nil
}

// verifyPrefix checks the executed-prefix evidence: tasks are valid,
// distinct, not deleted, and every constraint internal to the prefix
// agrees with the recorded execution order.
func verifyPrefix(n int, edges map[Edge]struct{}, deleted map[int]bool, executed []int) error {
	pos := make(map[int]int, len(executed))
	for i, t := range executed {
		if t < 0 || t >= n {
			return fmt.Errorf("topo: executed unknown task %d", t)
		}
		if deleted[t] {
			return fmt.Errorf("topo: executed task %d was deleted", t)
		}
		if _, dup := pos[t]; dup {
			return fmt.Errorf("topo: task %d executed twice", t)
		}
		pos[t] = i
	}
	for e := range edges {
		i, okA := pos[e.From]
		j, okB := pos[e.To]
		if okA && okB && i > j {
			return fmt.Errorf("topo: constraint %v contradicts executed order (%d before %d)", e, e.To, e.From)
		}
	}
	return nil
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
