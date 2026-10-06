package trail

import (
	"fmt"
	"sort"
)

// exhaustiveLimit is the maximum number of edges for which Solve
// cross-checks the greedy route against a full permutation search. This
// guards against greedy choices that strand the remaining edges.
const exhaustiveLimit = 10

// Solve returns the lexicographically smallest edge-ID sequence that forms
// a route from start using every edge exactly once. When no such route
// exists the returned error is a *Failure describing the degree or
// connectivity problem.
func Solve(start string, edges []Edge) ([]string, error) {
	if err := checkUniqueIDs(edges); err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}
	if f := checkDegrees(start, edges); f != nil {
		return nil, f
	}
	if f := checkConnectivity(start, edges); f != nil {
		return nil, f
	}
	route, ok := greedyRoute(start, edges)
	if !ok {
		return nil, fmt.Errorf("trail: greedy solver failed on a feasible instance")
	}
	if len(edges) <= exhaustiveLimit {
		opt, ok := exhaustiveRoute(start, edges, nil)
		if !ok {
			return nil, fmt.Errorf("trail: exhaustive search found no route on a feasible instance")
		}
		if !equalIDs(route, opt) {
			return nil, fmt.Errorf("trail: greedy route %v diverges from exhaustive optimum %v", route, opt)
		}
	}
	return route, nil
}

func checkUniqueIDs(edges []Edge) error {
	seen := make(map[string]bool, len(edges))
	for _, e := range edges {
		if seen[e.ID] {
			return &DuplicateEdgeIDError{ID: e.ID}
		}
		seen[e.ID] = true
	}
	return nil
}

// checkDegrees verifies the directed Eulerian trail degree conditions:
// either every node is balanced (circuit), or the start has exactly one
// more out-edge, exactly one other node has one more in-edge, and all
// remaining nodes are balanced.
func checkDegrees(start string, edges []Edge) *Failure {
	in := map[string]int{}
	out := map[string]int{}
	for _, e := range edges {
		out[e.From]++
		in[e.To]++
	}
	nodes := sortedNodes(in, out)
	bad := []NodeImbalance{}
	endCount := 0
	for _, n := range nodes {
		d := out[n] - in[n]
		switch {
		case d == 0:
		case d == 1 && n == start:
		case d == -1:
			endCount++
		default:
			bad = append(bad, NodeImbalance{Node: n, In: in[n], Out: out[n]})
		}
	}
	if len(bad) > 0 || endCount > 1 {
		// Report every node with a nonzero delta, not only the ones
		// flagged above, so operators see the full imbalance picture.
		if len(bad) == 0 {
			for _, n := range nodes {
				if out[n]-in[n] != 0 {
					bad = append(bad, NodeImbalance{Node: n, In: in[n], Out: out[n]})
				}
			}
		}
		return &Failure{Kind: FailureDegree, Nodes: bad}
	}
	return nil
}

// checkConnectivity verifies that every edge lies in the weakly connected
// component of the start node.
func checkConnectivity(start string, edges []Edge) *Failure {
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	seen := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, m := range adj[n] {
			if !seen[m] {
				seen[m] = true
				stack = append(stack, m)
			}
		}
	}
	var missing []string
	for _, e := range edges {
		if !seen[e.From] {
			missing = append(missing, e.ID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &Failure{Kind: FailureConnectivity, EdgeIDs: missing}
	}
	return nil
}

// greedyRoute walks the graph taking, at each node, the smallest-ID edge
// that does not strand the remaining edges (directed Fleury rule).
func greedyRoute(start string, edges []Edge) ([]string, bool) {
	remaining := make(map[string]Edge, len(edges))
	for _, e := range edges {
		remaining[e.ID] = e
	}
	route := make([]string, 0, len(edges))
	cur := start
	for len(remaining) > 0 {
		candidates := outEdges(cur, remaining)
		if len(candidates) == 0 {
			return nil, false
		}
		advanced := false
		for _, e := range candidates {
			delete(remaining, e.ID)
			if allTailsReachable(e.To, remaining) {
				route = append(route, e.ID)
				cur = e.To
				advanced = true
				break
			}
			remaining[e.ID] = e
		}
		if !advanced {
			return nil, false
		}
	}
	return route, true
}

// allTailsReachable reports whether the tail of every remaining edge is
// reachable from node `from` following directed remaining edges.
func allTailsReachable(from string, remaining map[string]Edge) bool {
	if len(remaining) == 0 {
		return true
	}
	adj := map[string][]string{}
	for _, e := range remaining {
		adj[e.From] = append(adj[e.From], e.To)
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, m := range adj[n] {
			if !seen[m] {
				seen[m] = true
				stack = append(stack, m)
			}
		}
	}
	for _, e := range remaining {
		if !seen[e.From] {
			return false
		}
	}
	return true
}

// exhaustiveRoute finds the lexicographically smallest route by depth-first
// search over edge permutations, trying candidate edges in ID order. When
// prefix is non-nil it is forced as the immutable head of the route; the
// search then only permutes the remaining edges.
func exhaustiveRoute(start string, edges []Edge, prefix []string) ([]string, bool) {
	sorted := sortByID(edges)
	index := make(map[string]int, len(sorted))
	for i, e := range sorted {
		index[e.ID] = i
	}
	full := uint64(1)<<uint(len(sorted)) - 1
	used := uint64(0)
	cur := start
	route := make([]string, 0, len(sorted))
	for _, id := range prefix {
		i, ok := index[id]
		if !ok || used&(uint64(1)<<uint(i)) != 0 {
			return nil, false
		}
		if sorted[i].From != cur {
			return nil, false
		}
		used |= uint64(1) << uint(i)
		route = append(route, id)
		cur = sorted[i].To
	}
	type state struct {
		cur  string
		used uint64
	}
	dead := map[state]bool{}
	var dfs func(cur string, used uint64) bool
	dfs = func(cur string, used uint64) bool {
		if used == full {
			return true
		}
		s := state{cur: cur, used: used}
		if dead[s] {
			return false
		}
		for i, e := range sorted {
			if used&(uint64(1)<<uint(i)) != 0 || e.From != cur {
				continue
			}
			route = append(route, e.ID)
			if dfs(e.To, used|(uint64(1)<<uint(i))) {
				return true
			}
			route = route[:len(route)-1]
		}
		dead[s] = true
		return false
	}
	if !dfs(cur, used) {
		return nil, false
	}
	return append([]string(nil), route...), true
}

func outEdges(node string, remaining map[string]Edge) []Edge {
	var out []Edge
	for _, e := range remaining {
		if e.From == node {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortByID(edges []Edge) []Edge {
	sorted := append([]Edge(nil), edges...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return sorted
}

func sortedNodes(in, out map[string]int) []string {
	set := map[string]bool{}
	for n := range in {
		set[n] = true
	}
	for n := range out {
		set[n] = true
	}
	nodes := make([]string, 0, len(set))
	for n := range set {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return nodes
}

func equalIDs(a, b []string) bool {
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
