package tcheck

import (
	"fmt"
	"sort"
)

// LivenessResult is the outcome of the request/response check.
type LivenessResult struct {
	OK bool
	// Prefix leads from the initial state to the request that may stay
	// unanswered forever. Cycle is a reachable loop, free of any response
	// state, that can be repeated indefinitely. Together they form a
	// lasso-shaped counterexample to "request is always eventually
	// followed by response".
	Prefix Path
	Cycle  Path
}

// CheckLiveness verifies that every reachable request is eventually
// answered by a response on every infinite continuation.
//
// Method: restrict the reachable graph to states WITHOUT the response
// proposition. A request state in that subgraph that can still reach a
// cycle (a non-trivial strongly connected component) yields an infinite
// execution on which the request is never answered.
//
// Finite dead-end paths are not failures: only genuine cycles count.
// Unreachable states are excluded from the analysis.
func (m *Machine) CheckLiveness(requestProp, responseProp string) LivenessResult {
	reach := m.Reachable()

	// noResp restricts to reachable states not labelled with response.
	noResp := func(s string) bool {
		return reach[s] && !m.Has(s, responseProp)
	}

	sccs := m.sccs(noResp)

	// Map each state to a non-trivial SCC id, if any.
	cycleOf := make(map[string]int)
	for i, comp := range sccs {
		if len(comp) > 1 || m.hasSelfLoop(comp[0]) {
			for _, s := range comp {
				cycleOf[s] = i
			}
		}
	}
	if len(cycleOf) == 0 {
		return LivenessResult{OK: true}
	}

	// Find a request state (in deterministic order) that can reach a
	// cycle while staying in the response-free subgraph.
	for _, r := range m.States() {
		if !noResp(r) || !m.Has(r, requestProp) {
			continue
		}
		entry, mid := m.reachCycle(r, noResp, cycleOf)
		if entry == "" {
			continue
		}
		cycle := m.extractCycle(entry, sccs[cycleOf[entry]])
		prefix := m.shortestPath(m.Initial, r, func(string) bool { return true })
		// Append the response-free segment from the request to the cycle.
		full := append(prefix, mid[1:]...)
		return LivenessResult{OK: false, Prefix: full, Cycle: cycle}
	}
	return LivenessResult{OK: true}
}

func (m *Machine) hasSelfLoop(s string) bool {
	for _, t := range m.Successors(s) {
		if t == s {
			return true
		}
	}
	return false
}

// reachCycle reports whether src can reach a state belonging to a
// non-trivial SCC, moving only through allowed states. It returns the
// reached cycle state and the path to it.
func (m *Machine) reachCycle(src string, allow func(string) bool, cycleOf map[string]int) (string, Path) {
	prev := map[string]string{src: ""}
	visited := map[string]bool{src: true}
	queue := []string{src}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if _, ok := cycleOf[s]; ok {
			rev := []string{s}
			for prev[rev[len(rev)-1]] != "" {
				rev = append(rev, prev[rev[len(rev)-1]])
			}
			path := make(Path, len(rev))
			for i, v := range rev {
				path[len(rev)-1-i] = v
			}
			return s, path
		}
		succ := append([]string(nil), m.Successors(s)...)
		sort.Strings(succ)
		for _, t := range succ {
			if !visited[t] && allow(t) {
				visited[t] = true
				prev[t] = s
				queue = append(queue, t)
			}
		}
	}
	return "", nil
}

// extractCycle builds a concrete cycle within one SCC, starting and
// ending at entry.
func (m *Machine) extractCycle(entry string, comp []string) Path {
	inComp := make(map[string]bool, len(comp))
	for _, s := range comp {
		inComp[s] = true
	}
	// Self loop: the cycle is trivially entry -> entry.
	for _, t := range m.Successors(entry) {
		if t == entry {
			return Path{entry, entry}
		}
	}
	// Pick the lexicographically smallest successor inside the SCC and
	// find a path back to entry; the loop is entry -> next ... -> entry.
	succ := append([]string(nil), m.Successors(entry)...)
	sort.Strings(succ)
	for _, next := range succ {
		if !inComp[next] {
			continue
		}
		back := m.shortestPath(next, entry, func(s string) bool { return inComp[s] })
		if back != nil {
			return append(Path{entry}, back...)
		}
	}
	return Path{entry} // unreachable in a well-formed non-trivial SCC
}

// sccs computes the strongly connected components of the subgraph
// induced by allow, using Tarjan's algorithm.
func (m *Machine) sccs(allow func(string) bool) [][]string {
	index := make(map[string]int)
	low := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var comps [][]string
	counter := 0

	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range m.Successors(v) {
			if !allow(w) {
				continue
			}
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if index[w] < low[v] {
					low[v] = index[w]
				}
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			comps = append(comps, comp)
		}
	}

	for _, s := range m.States() {
		if allow(s) {
			if _, seen := index[s]; !seen {
				strongconnect(s)
			}
		}
	}
	return comps
}

func (r LivenessResult) String() string {
	if r.OK {
		return "LIVENESS OK: every reachable request is eventually answered"
	}
	return fmt.Sprintf("LIVENESS VIOLATED: request may remain unanswered forever\n  prefix: %s\n  cycle:  %s  (repeat forever, no response occurs)",
		r.Prefix, r.Cycle)
}
