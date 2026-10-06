package tasksched

import "fmt"

// ConflictKind classifies why an update was rejected.
type ConflictKind int

const (
	// ConflictInvalid marks malformed input (bad index, deleted endpoint,
	// deleting an already deleted task).
	ConflictInvalid ConflictKind = iota
	// ConflictCycle marks a constraint that would close a loop.
	ConflictCycle
	// ConflictLocked marks a constraint that would require reordering
	// the already executed prefix, which is immutable.
	ConflictLocked
	// ConflictExecutedDelete marks an attempt to delete an executed task.
	ConflictExecutedDelete
)

// Conflict describes a rejected update. Chain is the shortest concrete
// evidence: for ConflictCycle the task loop To ~> From that the new
// constraint From->To would close; for ConflictLocked the executed
// segment (or [executedTask, newPredecessor]) proving the past would
// have to be reordered.
type Conflict struct {
	Kind   ConflictKind
	Chain  []int
	Detail string
}

func (c *Conflict) Error() string { return c.Detail }

// Planner incrementally executes tasks under evolving constraints.
// The executed prefix is locked: updates may only reshape the future,
// never reset or reorder the past.
type Planner struct {
	tasks    []string
	deleted  []bool
	edges    map[Edge]struct{}
	executed []int
}

func NewPlanner(tasks []string) *Planner {
	return &Planner{
		tasks:   tasks,
		deleted: make([]bool, len(tasks)),
		edges:   make(map[Edge]struct{}),
	}
}

// State is a serializable snapshot of a Planner, used for recovery.
type State struct {
	Tasks    []string
	Edges    []Edge
	Executed []int
	Deleted  []int
}

// Snapshot exports the full planner state.
func (p *Planner) Snapshot() State {
	s := State{Tasks: append([]string(nil), p.tasks...), Executed: append([]int(nil), p.executed...)}
	for e := range p.edges {
		s.Edges = append(s.Edges, e)
	}
	for i, d := range p.deleted {
		if d {
			s.Deleted = append(s.Deleted, i)
		}
	}
	return s
}

// Restore rebuilds a planner from a snapshot. The restored state is not
// trusted: call Verify to validate it against the execution evidence.
func Restore(s State) *Planner {
	p := NewPlanner(s.Tasks)
	for _, e := range s.Edges {
		p.edges[e] = struct{}{}
	}
	p.executed = append([]int(nil), s.Executed...)
	for _, t := range s.Deleted {
		if t >= 0 && t < len(p.deleted) {
			p.deleted[t] = true
		}
	}
	return p
}

// Executed returns the locked execution prefix.
func (p *Planner) Executed() []int { return append([]int(nil), p.executed...) }

// Plan returns the deterministic full plan: the locked executed prefix
// followed by the freshly sorted unexecuted remainder. A non-empty
// Cycle in the result means the remaining constraints are unsatisfiable.
func (p *Planner) Plan() Result {
	rem := p.remaining()
	index := make(map[int]int, len(rem))
	for i, t := range rem {
		index[t] = i
	}
	var edges []Edge
	for e := range p.edges {
		fi, fok := index[e.From]
		ti, tok := index[e.To]
		if fok && tok {
			edges = append(edges, Edge{fi, ti})
		}
	}
	res := Sort(len(rem), edges)
	order := append([]int(nil), p.executed...)
	for _, i := range res.Order {
		order = append(order, rem[i])
	}
	var cycle []int
	for _, i := range res.Cycle {
		cycle = append(cycle, rem[i])
	}
	return Result{Order: order, Cycle: cycle}
}

// ExecuteNext locks in the next task of the deterministic plan.
// It reports false when everything is executed or the remainder cycles.
func (p *Planner) ExecuteNext() (int, bool) {
	res := p.Plan()
	if res.Cycle != nil || len(res.Order) <= len(p.executed) {
		return 0, false
	}
	next := res.Order[len(p.executed)]
	p.executed = append(p.executed, next)
	return next, true
}

// Apply atomically deletes tasks and adds constraints: either every
// change is committed or none is. Deleting a task removes all
// constraints touching it. Executed tasks can never be deleted.
// On rejection it returns the shortest contradiction chain.
func (p *Planner) Apply(del []int, add []Edge) *Conflict {
	deleted := append([]bool(nil), p.deleted...)
	for _, t := range del {
		if t < 0 || t >= len(p.tasks) {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{t}, Detail: fmt.Sprintf("task index %d out of range", t)}
		}
		if p.executedPos(t) >= 0 {
			return &Conflict{Kind: ConflictExecutedDelete, Chain: []int{t}, Detail: fmt.Sprintf("task %q already executed and cannot be deleted", p.tasks[t])}
		}
		if deleted[t] {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{t}, Detail: fmt.Sprintf("task %q already deleted", p.tasks[t])}
		}
		deleted[t] = true
	}

	edges := make(map[Edge]struct{}, len(p.edges)+len(add))
	for e := range p.edges {
		if deleted[e.From] || deleted[e.To] {
			continue
		}
		edges[e] = struct{}{}
	}
	for _, e := range add {
		if e.From < 0 || e.From >= len(p.tasks) || e.To < 0 || e.To >= len(p.tasks) {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{e.From, e.To}, Detail: "constraint endpoint out of range"}
		}
		if deleted[e.From] || deleted[e.To] {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{e.From, e.To}, Detail: "constraint references a deleted task"}
		}
		if _, dup := edges[e]; dup {
			continue // duplicate constraints are deduplicated
		}
		if c := p.checkEdge(edges, e); c != nil {
			return c
		}
		edges[e] = struct{}{}
	}

	p.deleted = deleted
	p.edges = edges
	return nil
}

// AddEdge adds a single constraint atomically.
func (p *Planner) AddEdge(e Edge) *Conflict { return p.Apply(nil, []Edge{e}) }

// DeleteTasks removes tasks (and their constraints) atomically.
func (p *Planner) DeleteTasks(tasks ...int) *Conflict { return p.Apply(tasks, nil) }

// checkEdge validates a candidate constraint From->To against the edge
// set and the locked executed prefix.
func (p *Planner) checkEdge(edges map[Edge]struct{}, e Edge) *Conflict {
	if e.From == e.To {
		return &Conflict{Kind: ConflictCycle, Chain: []int{e.From}, Detail: fmt.Sprintf("constraint %q -> %q is a self loop", p.tasks[e.From], p.tasks[e.To])}
	}
	if path := shortestPath(edges, e.To, e.From); path != nil {
		return &Conflict{
			Kind:   ConflictCycle,
			Chain:  path,
			Detail: fmt.Sprintf("constraint %q -> %q closes the loop %s", p.tasks[e.From], p.tasks[e.To], p.chainNames(append(path, e.To))),
		}
	}
	pb := p.executedPos(e.To)
	if pb < 0 {
		return nil
	}
	pa := p.executedPos(e.From)
	if pa < 0 {
		return &Conflict{
			Kind:   ConflictLocked,
			Chain:  []int{e.To, e.From},
			Detail: fmt.Sprintf("task %q already executed at position %d, but new constraint requires %q before it", p.tasks[e.To], pb, p.tasks[e.From]),
		}
	}
	if pa > pb {
		return &Conflict{
			Kind:   ConflictLocked,
			Chain:  append([]int(nil), p.executed[pb:pa+1]...),
			Detail: fmt.Sprintf("task %q executed at position %d before %q at position %d, contradicting the new constraint", p.tasks[e.To], pb, p.tasks[e.From], pa),
		}
	}
	return nil
}

// Verify cross-checks the state against the execution-prefix evidence
// and a full sort of the remainder. It is the recovery check: a
// restored planner is trustworthy only when Verify returns nil.
func (p *Planner) Verify() *Conflict {
	pos := make(map[int]int, len(p.executed))
	for i, t := range p.executed {
		if t < 0 || t >= len(p.tasks) {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{t}, Detail: fmt.Sprintf("executed task index %d out of range", t)}
		}
		if p.deleted[t] {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{t}, Detail: fmt.Sprintf("executed task %q marked deleted", p.tasks[t])}
		}
		if _, dup := pos[t]; dup {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{t}, Detail: fmt.Sprintf("task %q executed twice", p.tasks[t])}
		}
		pos[t] = i
	}
	for _, e := range p.sortedEdges() {
		if p.deleted[e.From] || p.deleted[e.To] {
			return &Conflict{Kind: ConflictInvalid, Chain: []int{e.From, e.To}, Detail: fmt.Sprintf("constraint %q -> %q references a deleted task", p.tasks[e.From], p.tasks[e.To])}
		}
		pb, toExecuted := pos[e.To]
		if !toExecuted {
			continue
		}
		pa, fromExecuted := pos[e.From]
		if !fromExecuted {
			return &Conflict{
				Kind:   ConflictLocked,
				Chain:  []int{e.To, e.From},
				Detail: fmt.Sprintf("task %q executed at position %d but its predecessor %q never executed", p.tasks[e.To], pb, p.tasks[e.From]),
			}
		}
		if pa > pb {
			return &Conflict{
				Kind:   ConflictLocked,
				Chain:  append([]int(nil), p.executed[pb:pa+1]...),
				Detail: fmt.Sprintf("task %q executed at position %d before its predecessor %q at position %d", p.tasks[e.To], pb, p.tasks[e.From], pa),
			}
		}
	}
	if res := p.Plan(); res.Cycle != nil {
		return &Conflict{Kind: ConflictCycle, Chain: res.Cycle, Detail: fmt.Sprintf("remaining constraints contain the loop %s", p.chainNames(append(res.Cycle, res.Cycle[0])))}
	}
	return nil
}

func (p *Planner) remaining() []int {
	inExec := make(map[int]bool, len(p.executed))
	for _, t := range p.executed {
		inExec[t] = true
	}
	var rem []int
	for i := range p.tasks {
		if !p.deleted[i] && !inExec[i] {
			rem = append(rem, i)
		}
	}
	return rem
}

func (p *Planner) executedPos(t int) int {
	for i, u := range p.executed {
		if u == t {
			return i
		}
	}
	return -1
}

func (p *Planner) sortedEdges() []Edge {
	edges := make([]Edge, 0, len(p.edges))
	for e := range p.edges {
		edges = append(edges, e)
	}
	for i := 1; i < len(edges); i++ {
		for j := i; j > 0 && edgeLess(edges[j], edges[j-1]); j-- {
			edges[j], edges[j-1] = edges[j-1], edges[j]
		}
	}
	return edges
}

func edgeLess(a, b Edge) bool {
	return a.From < b.From || (a.From == b.From && a.To < b.To)
}

func (p *Planner) chainNames(chain []int) string {
	s := ""
	for i, t := range chain {
		if i > 0 {
			s += " -> "
		}
		s += p.tasks[t]
	}
	return s
}

// shortestPath returns the shortest constraint path src ~> dst
// (including both endpoints), or nil. BFS with ascending neighbor
// order keeps the result deterministic.
func shortestPath(edges map[Edge]struct{}, src, dst int) []int {
	adj := make(map[int][]int)
	for e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
	for _, list := range adj {
		for i := 1; i < len(list); i++ {
			for j := i; j > 0 && list[j] < list[j-1]; j-- {
				list[j], list[j-1] = list[j-1], list[j]
			}
		}
	}
	prev := map[int]int{src: -1}
	queue := []int{src}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v == dst {
			var path []int
			for u := dst; u != -1; u = prev[u] {
				path = append([]int{u}, path...)
			}
			return path
		}
		for _, w := range adj[v] {
			if _, seen := prev[w]; !seen {
				prev[w] = v
				queue = append(queue, w)
			}
		}
	}
	return nil
}
