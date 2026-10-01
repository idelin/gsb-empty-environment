// Package dominator implements a dynamic control-flow-graph dominator
// calculator. It computes the immediate dominator of every node reachable
// from a designated entry, supports incremental local updates after adding
// or removing a single edge, and produces counterexample witness paths for
// non-domination queries.
//
// The algorithm is the iterative dataflow method of Cooper, Harvey and
// Kennedy ("A Simple, Fast Dominance Algorithm"), warm-started from the
// previously computed dominator tree so that single-edge updates only
// propagate through the affected region of the graph.
package dominator

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoPath is returned when no witness path exists for a query.
var ErrNoPath = errors.New("dominator: no such path")

// Graph is a directed control-flow graph with a designated entry node.
// The zero value is not ready for use; build one with New.
type Graph struct {
	entry string
	succ  map[string]map[string]struct{}
	pred  map[string]map[string]struct{}

	// idom holds the current immediate dominator of each reachable node.
	// idom[entry] == entry. It is kept consistent after every mutation.
	idom map[string]string
	// rpo is the reverse postorder of reachable nodes from the last
	// (re)computation, used to warm-start incremental updates.
	rpo []string
}

// New creates a Graph with the given entry node.
func New(entry string) *Graph {
	g := &Graph{
		entry: entry,
		succ:  map[string]map[string]struct{}{},
		pred:  map[string]map[string]struct{}{},
	}
	g.addNode(entry)
	g.recompute()
	return g
}

// Entry returns the entry node of the graph.
func (g *Graph) Entry() string { return g.entry }

func (g *Graph) addNode(n string) {
	if _, ok := g.succ[n]; !ok {
		g.succ[n] = map[string]struct{}{}
	}
	if _, ok := g.pred[n]; !ok {
		g.pred[n] = map[string]struct{}{}
	}
}

// AddEdge inserts the control-flow edge from->to (creating endpoints if
// needed) and incrementally updates the dominator tree. Adding an edge
// that already exists is a no-op.
func (g *Graph) AddEdge(from, to string) {
	g.addNode(from)
	g.addNode(to)
	if _, ok := g.succ[from][to]; ok {
		return
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	g.incrementalUpdate()
}

// RemoveEdge deletes the control-flow edge from->to and incrementally
// updates the dominator tree. Removing a non-existent edge is a no-op.
func (g *Graph) RemoveEdge(from, to string) {
	if _, ok := g.succ[from]; !ok {
		return
	}
	if _, ok := g.succ[from][to]; !ok {
		return
	}
	delete(g.succ[from], to)
	delete(g.pred[to], from)
	g.incrementalUpdate()
}

// Idom returns the immediate dominator of n and whether n is reachable
// from the entry. The entry is its own immediate dominator.
func (g *Graph) Idom(n string) (string, bool) {
	d, ok := g.idom[n]
	return d, ok
}

// Dominators returns the full set of dominators of n (including n itself)
// by walking the idom chain up to the entry. ok is false if n is
// unreachable from the entry.
func (g *Graph) Dominators(n string) (set map[string]bool, ok bool) {
	if _, ok := g.idom[n]; !ok {
		return nil, false
	}
	set = map[string]bool{}
	for cur := n; ; cur = g.idom[cur] {
		set[cur] = true
		if cur == g.entry {
			break
		}
	}
	return set, true
}

// Dominates reports whether a dominates b, i.e. every path from the entry
// to b passes through a.
func (g *Graph) Dominates(a, b string) bool {
	set, ok := g.Dominators(b)
	return ok && set[a]
}

// Counterexample returns a witness path from the entry to target that
// avoids node, proving that node does not dominate target. It returns
// ErrNoPath if no such path exists (i.e. node really does dominate
// target, or target is unreachable).
func (g *Graph) Counterexample(node, target string) ([]string, error) {
	if node == g.entry {
		// The entry dominates every reachable node by definition; a
		// witness would have to avoid the entry itself, impossible.
		return nil, ErrNoPath
	}
	if _, ok := g.idom[target]; !ok {
		return nil, ErrNoPath
	}
	// BFS from entry to target, forbidden to step on node.
	prev := map[string]string{g.entry: ""}
	queue := []string{g.entry}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			path := []string{}
			for at := target; ; {
				path = append([]string{at}, path...)
				p, isStart := prev[at]
				if p == "" && isStart {
					break
				}
				at = p
			}
			return path, nil
		}
		for next := range g.succ[cur] {
			if next == node {
				continue
			}
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = cur
			queue = append(queue, next)
		}
	}
	return nil, ErrNoPath
}

// incrementalUpdate recomputes the dominator tree after a single-edge
// mutation. Reachability is recomputed from scratch (a single edge can
// (dis)connect whole subgraphs), while the idom fixpoint iteration is
// warm-started from the previous tree so unchanged regions converge
// immediately and only affected nodes are reprocessed.
func (g *Graph) incrementalUpdate() {
	rpo := g.reversePostOrder()
	reachable := make(map[string]bool, len(rpo))
	for _, n := range rpo {
		reachable[n] = true
	}

	// Warm start: keep previous idom guesses for still-reachable nodes.
	seed := make(map[string]string, len(rpo))
	for _, n := range rpo {
		if n == g.entry {
			seed[n] = g.entry
			continue
		}
		if old, ok := g.idom[n]; ok && reachable[old] {
			seed[n] = old
		}
	}

	g.idom = g.computeIdom(rpo, seed)
	g.rpo = rpo
}

// recompute performs a cold full computation (used at construction).
func (g *Graph) recompute() {
	rpo := g.reversePostOrder()
	seed := map[string]string{g.entry: g.entry}
	g.idom = g.computeIdom(rpo, seed)
	g.rpo = rpo
}

// reversePostOrder returns the nodes reachable from the entry in reverse
// postorder.
func (g *Graph) reversePostOrder() []string {
	visited := map[string]bool{}
	var post []string
	var dfs func(n string)
	dfs = func(n string) {
		if visited[n] {
			return
		}
		visited[n] = true
		for _, s := range g.sortedSucc(n) {
			dfs(s)
		}
		post = append(post, n)
	}
	dfs(g.entry)
	for i, j := 0, len(post)-1; i < j; i, j = i+1, j-1 {
		post[i], post[j] = post[j], post[i]
	}
	return post
}

// computeIdom runs the Cooper-Harvey-Kennedy iterative algorithm over the
// given reverse postorder, seeded with initial idom guesses.
func (g *Graph) computeIdom(rpo []string, seed map[string]string) map[string]string {
	idom := make(map[string]string, len(rpo))
	for n, d := range seed {
		idom[n] = d
	}
	idom[g.entry] = g.entry

	rpoIndex := make(map[string]int, len(rpo))
	for i, n := range rpo {
		rpoIndex[n] = i
	}

	intersect := func(b1, b2 string) string {
		for b1 != b2 {
			for rpoIndex[b1] > rpoIndex[b2] {
				b1 = idom[b1]
			}
			for rpoIndex[b2] > rpoIndex[b1] {
				b2 = idom[b2]
			}
		}
		return b1
	}

	for changed := true; changed; {
		changed = false
		for _, n := range rpo {
			if n == g.entry {
				continue
			}
			var newIDom string
			first := true
			for _, p := range g.sortedPred(n) {
				if _, ok := idom[p]; !ok {
					continue // predecessor not yet processed
				}
				if first {
					newIDom = p
					first = false
				} else {
					newIDom = intersect(p, newIDom)
				}
			}
			if first {
				continue // no processed predecessor yet
			}
			if old, ok := idom[n]; !ok || old != newIDom {
				idom[n] = newIDom
				changed = true
			}
		}
	}
	return idom
}

func (g *Graph) sortedSucc(n string) []string {
	out := make([]string, 0, len(g.succ[n]))
	for s := range g.succ[n] {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) sortedPred(n string) []string {
	out := make([]string, 0, len(g.pred[n]))
	for p := range g.pred[n] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// String renders the current dominator tree for debugging and demos.
func (g *Graph) String() string {
	nodes := make([]string, 0, len(g.idom))
	for n := range g.idom {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	var sb strings.Builder
	sb.WriteString("dominator tree:\n")
	for _, n := range nodes {
		fmt.Fprintf(&sb, "  idom(%s) = %s\n", n, g.idom[n])
	}
	return sb.String()
}
