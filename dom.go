// Package dom computes and incrementally maintains the dominator tree of a
// control-flow graph, and produces counterexample paths for dominance claims
// that do not hold.
package dom

import "sort"

// Graph is a directed control-flow graph over string node labels.
type Graph struct {
	succ map[string]map[string]bool
	pred map[string]map[string]bool
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		succ: make(map[string]map[string]bool),
		pred: make(map[string]map[string]bool),
	}
}

// AddNode inserts a node if it does not exist yet.
func (g *Graph) AddNode(n string) {
	if _, ok := g.succ[n]; !ok {
		g.succ[n] = make(map[string]bool)
		g.pred[n] = make(map[string]bool)
	}
}

// AddEdge inserts the control-flow edge u -> v.
func (g *Graph) AddEdge(u, v string) {
	g.AddNode(u)
	g.AddNode(v)
	g.succ[u][v] = true
	g.pred[v][u] = true
}

// RemoveEdge deletes the control-flow edge u -> v.
func (g *Graph) RemoveEdge(u, v string) {
	delete(g.succ[u], v)
	delete(g.pred[v], u)
}

// HasEdge reports whether the edge u -> v exists.
func (g *Graph) HasEdge(u, v string) bool {
	return g.succ[u][v]
}

// Nodes returns all node labels in sorted order.
func (g *Graph) Nodes() []string {
	out := make([]string, 0, len(g.succ))
	for n := range g.succ {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) sortedSucc(n string) []string {
	out := make([]string, 0, len(g.succ[n]))
	for s := range g.succ[n] {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) sortedPreds(n string) []string {
	out := make([]string, 0, len(g.pred[n]))
	for p := range g.pred[n] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Tree is the dominator tree of a graph rooted at an entry node. It is kept
// up to date incrementally as edges are added or removed through its methods.
type Tree struct {
	g         *Graph
	entry     string
	reachable map[string]bool
	dom       map[string]map[string]bool // dominator set per reachable node
	idom      map[string]string          // immediate dominator per reachable node
}

// Compute builds the dominator tree of g rooted at entry from scratch.
func Compute(g *Graph, entry string) *Tree {
	t := &Tree{
		g:     g,
		entry: entry,
		dom:   make(map[string]map[string]bool),
		idom:  make(map[string]string),
	}
	t.computeReachable()
	all := make(map[string]bool, len(t.reachable))
	for n := range t.reachable {
		all[n] = true
	}
	t.runDataflow(all, func(n string) map[string]bool { return t.top() })
	t.computeIdom(all)
	return t
}

// AddEdge inserts u -> v and incrementally updates the dominator tree.
func (t *Tree) AddEdge(u, v string) {
	t.g.AddEdge(u, v)
	t.update(v, true)
}

// RemoveEdge deletes u -> v and incrementally updates the dominator tree.
func (t *Tree) RemoveEdge(u, v string) {
	t.g.RemoveEdge(u, v)
	t.update(v, false)
}

// Idom returns the immediate dominator of n. The second result is false when
// n is the entry node or is unreachable from the entry.
func (t *Tree) Idom(n string) (string, bool) {
	d, ok := t.idom[n]
	return d, ok
}

// Dominators returns the full dominator set of n (including n itself).
func (t *Tree) Dominators(n string) map[string]bool {
	return copySet(t.dom[n])
}

// Dominates reports whether x dominates y: every path from the entry to y
// passes through x.
func (t *Tree) Dominates(x, y string) bool {
	return t.reachable[y] && t.dom[y][x]
}

// Reachable reports whether n is reachable from the entry.
func (t *Tree) Reachable(n string) bool {
	return t.reachable[n]
}

// Counterexample disproves the claim "x dominates y" by returning a path
// from the entry to y that never visits x. The second result is false when
// no such path exists (i.e. the claim actually holds or y is unreachable).
func (t *Tree) Counterexample(x, y string) ([]string, bool) {
	if !t.reachable[y] || x == y || x == t.entry || t.Dominates(x, y) {
		return nil, false
	}
	prev := map[string]string{t.entry: ""}
	queue := []string{t.entry}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n == y {
			var path []string
			for cur := y; cur != ""; cur = prev[cur] {
				path = append([]string{cur}, path...)
			}
			return path, true
		}
		for _, s := range t.g.sortedSucc(n) {
			if s == x {
				continue
			}
			if _, seen := prev[s]; !seen {
				prev[s] = n
				queue = append(queue, s)
			}
		}
	}
	return nil, false
}

// update incrementally repairs the tree after the edge into v changed.
// Only nodes reachable from v can have their dominator sets affected by a
// single edge change at v, so the dataflow is re-run on that region alone.
func (t *Tree) update(v string, insert bool) {
	oldReachable := t.reachable
	t.computeReachable()

	// Drop nodes that fell out of the reachable set.
	for n := range oldReachable {
		if !t.reachable[n] {
			delete(t.dom, n)
			delete(t.idom, n)
		}
	}

	affected := make(map[string]bool)
	if t.reachable[v] {
		for _, n := range t.reachableFrom(v) {
			affected[n] = true
		}
	}

	var seed func(string) map[string]bool
	if insert {
		// Insertion only shrinks dominator sets; warm-starting from the
		// current solution converges monotonically to the new fixpoint.
		seed = func(n string) map[string]bool {
			if oldReachable[n] {
				return copySet(t.dom[n])
			}
			return t.top()
		}
	} else {
		// Deletion only grows dominator sets; reset the affected region to
		// the top element and iterate down to the greatest fixpoint.
		seed = func(string) map[string]bool { return t.top() }
	}
	t.runDataflow(affected, seed)
	t.computeIdom(affected)
}

// runDataflow iterates the dominator equations over the affected nodes until
// a fixpoint is reached. Nodes outside the region keep their current sets.
func (t *Tree) runDataflow(affected map[string]bool, seed func(string) map[string]bool) {
	t.dom[t.entry] = map[string]bool{t.entry: true}
	for n := range affected {
		if n != t.entry {
			t.dom[n] = seed(n)
		}
	}
	order := make([]string, 0, len(affected))
	for n := range affected {
		order = append(order, n)
	}
	sort.Strings(order)
	for changed := true; changed; {
		changed = false
		for _, n := range order {
			if n == t.entry {
				continue
			}
			next := t.meet(n)
			if !equalSet(next, t.dom[n]) {
				t.dom[n] = next
				changed = true
			}
		}
	}
}

// meet computes dom(n) = {n} ∪ (∩ dom(p)) over reachable predecessors p.
func (t *Tree) meet(n string) map[string]bool {
	var acc map[string]bool
	first := true
	for _, p := range t.g.sortedPreds(n) {
		if !t.reachable[p] {
			continue
		}
		if first {
			acc = copySet(t.dom[p])
			first = false
		} else {
			acc = intersectSet(acc, t.dom[p])
		}
	}
	if acc == nil {
		acc = make(map[string]bool)
	}
	acc[n] = true
	return acc
}

// computeIdom derives the immediate dominator of every node in region from
// the current dominator sets: idom(n) is the strict dominator of n that is
// dominated by all other strict dominators of n.
func (t *Tree) computeIdom(region map[string]bool) {
	for n := range region {
		if n == t.entry || !t.reachable[n] {
			continue
		}
		best := ""
		for d := range t.dom[n] {
			if d == n {
				continue
			}
			deepest := true
			for e := range t.dom[n] {
				if e == n || e == d {
					continue
				}
				if !t.dom[d][e] {
					deepest = false
					break
				}
			}
			if deepest {
				best = d
				break
			}
		}
		t.idom[n] = best
	}
}

func (t *Tree) computeReachable() {
	t.reachable = make(map[string]bool)
	if _, ok := t.g.succ[t.entry]; !ok {
		return
	}
	for _, n := range t.reachableFrom(t.entry) {
		t.reachable[n] = true
	}
}

func (t *Tree) reachableFrom(start string) []string {
	seen := map[string]bool{start: true}
	queue := []string{start}
	var out []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		out = append(out, n)
		for _, s := range t.g.sortedSucc(n) {
			if !seen[s] {
				seen[s] = true
				queue = append(queue, s)
			}
		}
	}
	return out
}

// top returns the top element of the dominator lattice: all reachable nodes.
func (t *Tree) top() map[string]bool {
	s := make(map[string]bool, len(t.reachable))
	for n := range t.reachable {
		s[n] = true
	}
	return s
}

func copySet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for k := range s {
		out[k] = true
	}
	return out
}

func intersectSet(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func equalSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
