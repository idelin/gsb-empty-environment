package dom

import (
	"fmt"
	"sort"
	"strings"
)

// Analysis holds the dominator information of a Graph at a point in time.
// It is built by Graph.Analyze or by an incremental edge update applied to
// an existing Analysis.
type Analysis struct {
	g           *Graph
	reachable   map[int]struct{}
	unreachable []int
	// dom[n] is the set of dominators of n, n included. Only reachable
	// nodes have an entry.
	dom map[int]map[int]struct{}
	// idom[n] is the immediate dominator of n. Entries and reachable nodes
	// dominated only by themselves (possible with several entries) have
	// no idom; they are roots of the dominator forest.
	idom map[int]int
}

// Analyze computes the dominator relation for the whole graph.
func (g *Graph) Analyze() *Analysis {
	a := &Analysis{
		g:         g,
		reachable: g.reachableSet(nil),
		dom:       make(map[int]map[int]struct{}),
		idom:      make(map[int]int),
	}
	for _, n := range g.Nodes() {
		if _, ok := a.reachable[n]; !ok {
			a.unreachable = append(a.unreachable, n)
		}
	}
	region := make(map[int]struct{}, len(a.reachable))
	for n := range a.reachable {
		region[n] = struct{}{}
	}
	a.computeRegion(region)
	a.deriveIDom(region)
	return a
}

// computeRegion runs the dominator dataflow
//
//	dom(n) = {n} ∪ ⋂ dom(p)  over reachable predecessors p
//
// restricted to region, which must be a subset of the reachable nodes.
// Nodes outside the region keep their current dom sets and act as a fixed
// boundary. Entries are fixed points with dom(entry) = {entry}, matching
// the virtual super-root convention.
func (a *Analysis) computeRegion(region map[int]struct{}) {
	for n := range region {
		if a.g.IsEntry(n) {
			a.dom[n] = map[int]struct{}{n: {}}
		} else {
			a.dom[n] = copySet(a.reachable)
		}
	}
	changed := true
	for changed {
		changed = false
		for _, n := range sortedKeys(region) {
			if a.g.IsEntry(n) {
				continue
			}
			acc := make(map[int]struct{})
			first := true
			for _, p := range a.g.Predecessors(n) {
				if _, ok := a.reachable[p]; !ok {
					continue
				}
				if first {
					acc = copySet(a.dom[p])
					first = false
				} else {
					acc = intersectSets(acc, a.dom[p])
				}
			}
			acc[n] = struct{}{}
			if !equalSets(acc, a.dom[n]) {
				a.dom[n] = acc
				changed = true
			}
		}
	}
}

// deriveIDom computes the immediate dominator of every node in region as
// its strict dominator with the largest dominator set (the closest one on
// the chain to the virtual root).
func (a *Analysis) deriveIDom(region map[int]struct{}) {
	for n := range region {
		if a.g.IsEntry(n) {
			delete(a.idom, n)
			continue
		}
		best, bestSize, found := 0, -1, false
		for d := range a.dom[n] {
			if d == n {
				continue
			}
			if size := len(a.dom[d]); !found || size > bestSize {
				best, bestSize, found = d, size, true
			}
		}
		if found {
			a.idom[n] = best
		} else {
			delete(a.idom, n)
		}
	}
}

// Reachable reports whether n is reachable from an entry.
func (a *Analysis) Reachable(n int) bool {
	_, ok := a.reachable[n]
	return ok
}

// Unreachable returns the nodes of the graph that no entry can reach, in
// ascending order. They carry no dominator information.
func (a *Analysis) Unreachable() []int {
	out := make([]int, len(a.unreachable))
	copy(out, a.unreachable)
	return out
}

// Dominates reports whether d dominates n: every path from an entry to n
// goes through d. Both nodes must be reachable; queries involving
// unreachable nodes return false.
func (a *Analysis) Dominates(d, n int) bool {
	dom, ok := a.dom[n]
	if !ok {
		return false
	}
	_, ok = dom[d]
	return ok
}

// Dominators returns all dominators of n (n included) in ascending order,
// or nil if n is unreachable.
func (a *Analysis) Dominators(n int) []int {
	dom, ok := a.dom[n]
	if !ok {
		return nil
	}
	return sortedKeys(dom)
}

// IDom returns the immediate dominator of n. The second result is false
// when n is unreachable, is an entry, or is only dominated by itself
// (possible with several entries).
func (a *Analysis) IDom(n int) (int, bool) {
	d, ok := a.idom[n]
	return d, ok
}

// Tree is a dominator forest. Roots are the nodes without an immediate
// dominator: entries and, with several entries, nodes reachable from more
// than one entry. Conceptually every root is a child of a virtual
// super-root.
type Tree struct {
	Roots    []int
	Children map[int][]int
}

// Tree builds the dominator forest of the reachable nodes.
func (a *Analysis) Tree() *Tree {
	t := &Tree{Children: make(map[int][]int)}
	for n := range a.reachable {
		if _, ok := a.idom[n]; !ok {
			t.Roots = append(t.Roots, n)
		}
	}
	for child, parent := range a.idom {
		t.Children[parent] = append(t.Children[parent], child)
	}
	sort.Ints(t.Roots)
	for k := range t.Children {
		sort.Ints(t.Children[k])
	}
	return t
}

// String renders the forest, one node per line, children indented.
func (t *Tree) String() string {
	var b strings.Builder
	var walk func(n, depth int)
	walk = func(n, depth int) {
		fmt.Fprintf(&b, "%s%d\n", strings.Repeat("  ", depth), n)
		for _, c := range t.Children[n] {
			walk(c, depth+1)
		}
	}
	for _, r := range t.Roots {
		walk(r, 0)
	}
	return b.String()
}

// Evidence returns the dominator chain that proves d dominates n: the
// immediate-dominator links from n up to d, both included. It fails when d
// does not dominate n.
func (a *Analysis) Evidence(d, n int) ([]int, error) {
	if !a.Dominates(d, n) {
		return nil, fmt.Errorf("dom: %d does not dominate %d", d, n)
	}
	chain := []int{n}
	for cur := n; cur != d; {
		next, ok := a.idom[cur]
		if !ok {
			return nil, fmt.Errorf("dom: broken dominator chain at %d", cur)
		}
		chain = append(chain, next)
		cur = next
	}
	return chain, nil
}

func copySet(s map[int]struct{}) map[int]struct{} {
	out := make(map[int]struct{}, len(s))
	for k := range s {
		out[k] = struct{}{}
	}
	return out
}

func intersectSets(a, b map[int]struct{}) map[int]struct{} {
	out := make(map[int]struct{})
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func equalSets(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
