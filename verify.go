package dom

import "fmt"

// Verify cross-checks an Analysis against the graph with a method that is
// independent from the dataflow computation: for every pair of reachable
// nodes (d, n) it removes d and tests whether n stays reachable from the
// entries. d dominates n iff n becomes unreachable. It also checks the
// reachability partition and the immediate-dominator links. It is
// quadratic in the number of nodes and meant for small graphs and tests.
func (g *Graph) Verify(a *Analysis) error {
	want := g.reachableSet(nil)
	for n := range want {
		if !a.Reachable(n) {
			return fmt.Errorf("dom: node %d is reachable but the analysis reports it unreachable", n)
		}
	}
	for _, n := range a.Unreachable() {
		if _, ok := want[n]; ok {
			return fmt.Errorf("dom: node %d is unreachable but the analysis reports it reachable", n)
		}
	}
	for _, n := range g.Nodes() {
		if !a.Reachable(n) {
			continue
		}
		for _, d := range g.Nodes() {
			if d == n || !a.Reachable(d) {
				continue
			}
			claimed := a.Dominates(d, n)
			actual := !g.reachableWithRemoved(d, n)
			if claimed != actual {
				return fmt.Errorf("dom: Dominates(%d, %d) = %v, but the removal test says %v", d, n, claimed, actual)
			}
		}
	}
	for _, n := range g.Nodes() {
		if !a.Reachable(n) || g.IsEntry(n) {
			continue
		}
		id, ok := a.IDom(n)
		if !ok {
			// Only possible for nodes dominated by nothing but
			// themselves, e.g. reachable from several entries.
			if doms := a.Dominators(n); len(doms) != 1 {
				return fmt.Errorf("dom: node %d has no idom but %d dominators", n, len(doms))
			}
			continue
		}
		if id == n || !a.Dominates(id, n) {
			return fmt.Errorf("dom: idom(%d) = %d is not a strict dominator", n, id)
		}
		for _, d := range a.Dominators(n) {
			if d != n && d != id && !a.Dominates(d, id) {
				return fmt.Errorf("dom: dominator %d of %d does not dominate idom %d", d, n, id)
			}
		}
	}
	return nil
}

// VerifyEvidence independently validates a dominator chain as produced by
// Analysis.Evidence. For every adjacent pair (x, y) it checks with
// removal tests that y is the immediate dominator of x: removing y must
// disconnect x from the entries, and every other node whose removal
// disconnects x must also disconnect y. The whole chain then proves
// chain[last] dominates chain[0] through genuine idom links.
func (g *Graph) VerifyEvidence(chain []int) error {
	if len(chain) == 0 {
		return fmt.Errorf("dom: empty evidence chain")
	}
	reach := g.reachableSet(nil)
	for _, n := range chain {
		if _, ok := reach[n]; !ok {
			return fmt.Errorf("dom: evidence chain contains unreachable node %d", n)
		}
	}
	for i := 0; i+1 < len(chain); i++ {
		x, y := chain[i], chain[i+1]
		if x == y {
			return fmt.Errorf("dom: evidence chain repeats node %d", x)
		}
		if g.reachableWithRemoved(y, x) {
			return fmt.Errorf("dom: invalid evidence: %d stays reachable after removing %d, so %d does not dominate it", x, y, y)
		}
		for _, z := range g.Nodes() {
			if z == x || z == y {
				continue
			}
			if !g.reachableWithRemoved(z, x) && g.reachableWithRemoved(z, y) {
				return fmt.Errorf("dom: invalid evidence: %d dominates %d but not %d, so %d is not its immediate dominator", z, x, y, y)
			}
		}
	}
	return nil
}

// reachableWithRemoved reports whether target is reachable from the
// entries when removed is treated as deleted from the graph.
func (g *Graph) reachableWithRemoved(removed, target int) bool {
	if removed == target {
		return false
	}
	_, ok := g.reachableSet(map[int]struct{}{removed: {}})[target]
	return ok
}
