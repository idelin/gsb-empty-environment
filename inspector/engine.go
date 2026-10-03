// Package inspector is an offline integrity-inspection and invariant
// assertion engine. It maintains a graph of entities (nodes with outgoing
// references and weights) and, on demand, runs a full-state patrol that
// proactively detects dangling references, orphan cycles and data skew,
// and evaluates user-registered invariants. All methods are safe for
// concurrent use; the engine spawns no goroutines and leaks nothing.
package inspector

import (
	"fmt"
	"sort"
	"sync"
)

// Finding kinds produced by Inspect.
const (
	KindDanglingRef  = "dangling-ref"
	KindOrphanCycle  = "orphan-cycle"
	KindDataSkew     = "data-skew"
	KindInvariant    = "invariant-violation"
)

// Node is one entity in the inspected graph.
type Node struct {
	ID     string
	Refs   []string // outgoing references to other node IDs
	Weight int64    // load/size metric used for skew analysis
	Attrs  map[string]string
}

// Finding is a single integrity violation.
type Finding struct {
	Kind   string
	Detail string
	Nodes  []string
}

// Report aggregates all findings of one inspection pass.
type Report struct {
	Findings []Finding
}

// OK reports whether the inspected state is clean.
func (r Report) OK() bool { return len(r.Findings) == 0 }

// Snapshot is an immutable deep copy of the engine state, handed to
// invariant checks so they run lock-free against a consistent view.
type Snapshot struct {
	Nodes map[string]Node
	Roots map[string]bool
}

// Invariant is a formal constraint asserted over a full-state snapshot.
type Invariant struct {
	Name  string
	Check func(Snapshot) error
}

// Engine holds the mutable graph plus registered invariants.
type Engine struct {
	mu         sync.RWMutex
	nodes      map[string]Node
	roots      map[string]bool
	invariants []Invariant
	skewFactor float64
}

// NewEngine creates an empty engine. skewFactor is the mean-weight
// multiplier above which a node is flagged as skewed (default 3.0).
func NewEngine() *Engine {
	return &Engine{
		nodes:      make(map[string]Node),
		roots:      make(map[string]bool),
		skewFactor: 3.0,
	}
}

// SetSkewFactor adjusts the skew sensitivity; <= 0 disables skew checks.
func (e *Engine) SetSkewFactor(f float64) {
	e.mu.Lock()
	e.skewFactor = f
	e.mu.Unlock()
}

// UpsertNode inserts or replaces a node (copying its slices/maps).
func (e *Engine) UpsertNode(n Node) {
	e.mu.Lock()
	e.nodes[n.ID] = cloneNode(n)
	e.mu.Unlock()
}

// RemoveNode deletes a node. Outgoing refs of other nodes are left as-is
// so the next Inspect pass reports them as dangling.
func (e *Engine) RemoveNode(id string) {
	e.mu.Lock()
	delete(e.nodes, id)
	delete(e.roots, id)
	e.mu.Unlock()
}

// AddRoot marks an existing node as a reachability root.
func (e *Engine) AddRoot(id string) {
	e.mu.Lock()
	e.roots[id] = true
	e.mu.Unlock()
}

// RegisterInvariant adds a formal constraint evaluated on every Inspect.
func (e *Engine) RegisterInvariant(name string, check func(Snapshot) error) {
	e.mu.Lock()
	e.invariants = append(e.invariants, Invariant{Name: name, Check: check})
	e.mu.Unlock()
}

// Snapshot returns a deep copy of the current state.
func (e *Engine) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotLocked()
}

func (e *Engine) snapshotLocked() Snapshot {
	snap := Snapshot{
		Nodes: make(map[string]Node, len(e.nodes)),
		Roots: make(map[string]bool, len(e.roots)),
	}
	for id, n := range e.nodes {
		snap.Nodes[id] = cloneNode(n)
	}
	for id := range e.roots {
		snap.Roots[id] = true
	}
	return snap
}

func cloneNode(n Node) Node {
	out := Node{ID: n.ID, Weight: n.Weight}
	if n.Refs != nil {
		out.Refs = append([]string(nil), n.Refs...)
	}
	if n.Attrs != nil {
		out.Attrs = make(map[string]string, len(n.Attrs))
		for k, v := range n.Attrs {
			out.Attrs[k] = v
		}
	}
	return out
}

// Inspect runs the full patrol over a consistent snapshot: dangling
// references, orphan cycles, data skew, and all registered invariants.
// Findings are deterministically ordered.
func (e *Engine) Inspect() Report {
	e.mu.RLock()
	snap := e.snapshotLocked()
	invs := append([]Invariant(nil), e.invariants...)
	skewFactor := e.skewFactor
	e.mu.RUnlock()

	var rep Report
	rep.Findings = append(rep.Findings, checkDangling(snap)...)
	rep.Findings = append(rep.Findings, checkOrphanCycles(snap)...)
	rep.Findings = append(rep.Findings, checkSkew(snap, skewFactor)...)
	for _, inv := range invs {
		if inv.Check == nil {
			continue
		}
		if err := inv.Check(snap); err != nil {
			rep.Findings = append(rep.Findings, Finding{
				Kind:   KindInvariant,
				Detail: fmt.Sprintf("invariant %q violated: %v", inv.Name, err),
			})
		}
	}
	sort.Slice(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].Kind != rep.Findings[j].Kind {
			return rep.Findings[i].Kind < rep.Findings[j].Kind
		}
		return rep.Findings[i].Detail < rep.Findings[j].Detail
	})
	return rep
}

// checkDangling flags references pointing at non-existent nodes.
func checkDangling(s Snapshot) []Finding {
	var out []Finding
	for _, id := range sortedIDs(s.Nodes) {
		n := s.Nodes[id]
		for _, ref := range n.Refs {
			if _, ok := s.Nodes[ref]; !ok {
				out = append(out, Finding{
					Kind:   KindDanglingRef,
					Detail: fmt.Sprintf("node %q references missing node %q", id, ref),
					Nodes:  []string{id, ref},
				})
			}
		}
	}
	return out
}

// checkOrphanCycles flags strongly-connected components that contain a
// cycle and are unreachable from every root.
func checkOrphanCycles(s Snapshot) []Finding {
	reachable := make(map[string]bool, len(s.Nodes))
	var queue []string
	for root := range s.Roots {
		if _, ok := s.Nodes[root]; ok && !reachable[root] {
			reachable[root] = true
			queue = append(queue, root)
		}
	}
	for len(queue) > 0 {
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, ref := range s.Nodes[cur].Refs {
			if _, ok := s.Nodes[ref]; ok && !reachable[ref] {
				reachable[ref] = true
				queue = append(queue, ref)
			}
		}
	}

	var out []Finding
	for _, scc := range tarjanSCC(s) {
		cyclic := len(scc) > 1
		if !cyclic { // self-loop?
			for _, ref := range s.Nodes[scc[0]].Refs {
				if ref == scc[0] {
					cyclic = true
					break
				}
			}
		}
		if !cyclic {
			continue
		}
		orphan := true
		for _, id := range scc {
			if reachable[id] {
				orphan = false
				break
			}
		}
		if orphan {
			out = append(out, Finding{
				Kind:   KindOrphanCycle,
				Detail: fmt.Sprintf("unreachable cycle of %d node(s): %v", len(scc), scc),
				Nodes:  scc,
			})
		}
	}
	return out
}

// checkSkew flags nodes whose weight exceeds factor * mean weight.
func checkSkew(s Snapshot, factor float64) []Finding {
	if factor <= 0 || len(s.Nodes) == 0 {
		return nil
	}
	var total int64
	for _, n := range s.Nodes {
		if n.Weight > 0 {
			total += n.Weight
		}
	}
	mean := float64(total) / float64(len(s.Nodes))
	if mean <= 0 {
		return nil
	}
	var out []Finding
	for _, id := range sortedIDs(s.Nodes) {
		n := s.Nodes[id]
		if float64(n.Weight) > mean*factor {
			out = append(out, Finding{
				Kind: KindDataSkew,
				Detail: fmt.Sprintf("node %q weight %d is %.1fx the mean %.1f (threshold %.1fx)",
					id, n.Weight, float64(n.Weight)/mean, mean, factor),
				Nodes: []string{id},
			})
		}
	}
	return out
}

// tarjanSCC computes strongly connected components (iterative Tarjan).
func tarjanSCC(s Snapshot) [][]string {
	index := make(map[string]int, len(s.Nodes))
	low := make(map[string]int, len(s.Nodes))
	onStack := make(map[string]bool, len(s.Nodes))
	var stack []string
	var sccs [][]string
	counter := 0

	type frame struct {
		id  string
		ref int // next Refs index to explore
	}
	for _, start := range sortedIDs(s.Nodes) {
		if _, seen := index[start]; seen {
			continue
		}
		var call []frame
		index[start] = counter
		low[start] = counter
		counter++
		stack = append(stack, start)
		onStack[start] = true
		call = append(call, frame{id: start})

		for len(call) > 0 {
			top := &call[len(call)-1]
			n := s.Nodes[top.id]
			if top.ref < len(n.Refs) {
				ref := n.Refs[top.ref]
				top.ref++
				if _, ok := s.Nodes[ref]; !ok {
					continue
				}
				if _, seen := index[ref]; !seen {
					index[ref] = counter
					low[ref] = counter
					counter++
					stack = append(stack, ref)
					onStack[ref] = true
					call = append(call, frame{id: ref})
				} else if onStack[ref] && index[ref] < low[top.id] {
					low[top.id] = index[ref]
				}
				continue
			}
			// Done exploring top.id.
			if low[top.id] == index[top.id] {
				var scc []string
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					scc = append(scc, w)
					if w == top.id {
						break
					}
				}
				sort.Strings(scc)
				sccs = append(sccs, scc)
			}
			call = call[:len(call)-1]
			if len(call) > 0 {
				parent := &call[len(call)-1]
				if low[top.id] < low[parent.id] {
					low[parent.id] = low[top.id]
				}
			}
		}
	}
	return sccs
}

func sortedIDs(m map[string]Node) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
