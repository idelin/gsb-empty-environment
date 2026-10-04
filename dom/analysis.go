package dom

import (
	"errors"
	"fmt"
	"sort"
)

// Analysis holds the dominance computation for a Graph. Dominance is
// only defined over nodes reachable from the entry; unreachable nodes
// are reported separately via Unreachable and never dominate (nor are
// dominated by) any node.
//
// The immediate dominators are computed with the iterative
// Cooper-Harvey-Kennedy algorithm over the reverse postorder, which
// handles arbitrary cycles and self-loops.
type Analysis struct {
	g        *Graph
	idom     map[int]int // reachable node -> immediate dominator; entry maps to itself
	reach    map[int]struct{}
	rpo      []int // reverse postorder of reachable nodes
	children map[int][]int
}

// NewAnalysis computes dominance information for g.
func NewAnalysis(g *Graph) *Analysis {
	a := &Analysis{g: g}
	a.compute()
	return a
}

// compute runs the full dominator computation from scratch.
func (a *Analysis) compute() {
	g := a.g
	a.idom = make(map[int]int)
	a.reach = make(map[int]struct{})
	a.children = make(map[int][]int)

	// Iterative DFS from the entry to obtain a postorder; the reverse
	// postorder (RPO) is the processing order for the dataflow.
	var post []int
	visited := map[int]bool{g.entry: true}
	type frame struct {
		n     int
		succs []int
		next  int
	}
	stack := []frame{{n: g.entry, succs: g.Successors(g.entry)}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next < len(top.succs) {
			s := top.succs[top.next]
			top.next++
			if !visited[s] {
				visited[s] = true
				stack = append(stack, frame{n: s, succs: g.Successors(s)})
			}
			continue
		}
		post = append(post, top.n)
		stack = stack[:len(stack)-1]
	}

	a.rpo = make([]int, len(post))
	rpoNum := make(map[int]int, len(post))
	for i, n := range post {
		a.rpo[len(post)-1-i] = n
	}
	for i, n := range a.rpo {
		rpoNum[n] = i
		a.reach[n] = struct{}{}
	}

	// Cooper-Harvey-Kennedy iterative fixed point.
	a.idom[g.entry] = g.entry
	intersect := func(b1, b2 int) int {
		for b1 != b2 {
			for rpoNum[b1] > rpoNum[b2] {
				b1 = a.idom[b1]
			}
			for rpoNum[b2] > rpoNum[b1] {
				b2 = a.idom[b2]
			}
		}
		return b1
	}
	for changed := true; changed; {
		changed = false
		for _, b := range a.rpo {
			if b == g.entry {
				continue
			}
			newIDom, found := 0, false
			for _, p := range g.Predecessors(b) {
				if _, ok := a.idom[p]; !ok {
					continue // predecessor not yet processed (e.g. self-loop)
				}
				if !found {
					newIDom, found = p, true
				} else {
					newIDom = intersect(p, newIDom)
				}
			}
			if !found {
				continue
			}
			if old, ok := a.idom[b]; !ok || old != newIDom {
				a.idom[b] = newIDom
				changed = true
			}
		}
	}

	for n, p := range a.idom {
		if n != g.entry {
			a.children[p] = append(a.children[p], n)
		}
	}
	for _, kids := range a.children {
		sort.Ints(kids)
	}
}

// Dominates reports whether x dominates y: every path from the entry
// to y passes through x. Both nodes must be reachable; queries
// involving unreachable nodes return false. A reachable node always
// dominates itself.
func (a *Analysis) Dominates(x, y int) bool {
	if _, ok := a.reach[x]; !ok {
		return false
	}
	if _, ok := a.reach[y]; !ok {
		return false
	}
	for n := y; ; {
		if n == x {
			return true
		}
		if n == a.g.entry {
			return false
		}
		n = a.idom[n]
	}
}

// StrictlyDominates reports whether x dominates y and x != y.
func (a *Analysis) StrictlyDominates(x, y int) bool {
	return x != y && a.Dominates(x, y)
}

// ImmediateDominator returns the immediate dominator of n. The second
// result is false when n is the entry or is unreachable.
func (a *Analysis) ImmediateDominator(n int) (int, bool) {
	if n == a.g.entry {
		return 0, false
	}
	p, ok := a.idom[n]
	return p, ok
}

// Reachable reports whether n is reachable from the entry.
func (a *Analysis) Reachable(n int) bool {
	_, ok := a.reach[n]
	return ok
}

// Unreachable returns, in ascending order, the nodes that cannot be
// reached from the entry. They take no part in dominance relations.
func (a *Analysis) Unreachable() []int {
	var out []int
	for n := range a.g.nodes {
		if _, ok := a.reach[n]; !ok {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// DominatorTree returns the dominator tree as a child map: for each
// reachable node, its immediately dominated children in ascending
// order. The tree root is the graph entry.
func (a *Analysis) DominatorTree() map[int][]int {
	out := make(map[int][]int, len(a.children))
	for n, kids := range a.children {
		cp := make([]int, len(kids))
		copy(cp, kids)
		out[n] = cp
	}
	return out
}

// DominancePath returns the chain of dominators from "from" down to
// "to" (both inclusive) in the dominator tree. It returns nil when
// from does not dominate to.
func (a *Analysis) DominancePath(from, to int) []int {
	if !a.Dominates(from, to) {
		return nil
	}
	var rev []int
	for n := to; ; n = a.idom[n] {
		rev = append(rev, n)
		if n == from {
			break
		}
	}
	path := make([]int, len(rev))
	for i, n := range rev {
		path[len(rev)-1-i] = n
	}
	return path
}

// VerifyDominancePath checks dominator-path evidence: path must be a
// non-empty chain in which every step is an immediate-dominator edge
// of the current analysis, i.e. path[i] is the immediate dominator of
// path[i+1], and every node is reachable. A single-node path is valid
// evidence that the node dominates itself.
func (a *Analysis) VerifyDominancePath(path []int) error {
	if len(path) == 0 {
		return errors.New("dom: empty dominance path")
	}
	for i, n := range path {
		if !a.Reachable(n) {
			return fmt.Errorf("dom: node %d at position %d is unreachable", n, i)
		}
		if i+1 < len(path) {
			parent, ok := a.idom[path[i+1]]
			if !ok || parent != n || path[i+1] == a.g.entry {
				return fmt.Errorf("dom: node %d is not the immediate dominator of %d", n, path[i+1])
			}
		}
	}
	return nil
}

// Recompute recomputes dominance information after the underlying
// graph has been mutated (edges added or removed) and returns the
// affected region: the sorted set of nodes whose immediate dominator
// or reachability changed relative to the previous computation.
func (a *Analysis) Recompute() []int {
	oldIDom := a.idom
	a.compute()

	affected := make(map[int]struct{})
	for n, p := range a.idom {
		if old, ok := oldIDom[n]; !ok || old != p {
			affected[n] = struct{}{}
		}
	}
	for n := range oldIDom {
		if _, ok := a.idom[n]; !ok {
			affected[n] = struct{}{}
		}
	}
	out := make([]int, 0, len(affected))
	for n := range affected {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}
