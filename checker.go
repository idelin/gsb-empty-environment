package main

// ReachableSet returns the set of states reachable from the initial state.
// Only reachable states participate in property checking; unreachable
// states must never influence the verdict.
func (m *Machine) ReachableSet() map[string]bool {
	seen := map[string]bool{m.Initial: true}
	queue := []string{m.Initial}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, t := range m.Adj[s] {
			if !seen[t] {
				seen[t] = true
				queue = append(queue, t)
			}
		}
	}
	return seen
}

// bfsPath returns a shortest path from src to dst, visiting only states
// accepted by allow (src and dst included). Returns nil if unreachable.
func bfsPath(adj map[string][]string, src, dst string, allow func(string) bool) []string {
	if !allow(src) || !allow(dst) {
		return nil
	}
	prev := map[string]string{src: ""}
	queue := []string{src}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if s == dst {
			var path []string
			for cur := dst; ; cur = prev[cur] {
				path = append([]string{cur}, path...)
				if cur == src {
					return path
				}
			}
		}
		for _, t := range adj[s] {
			if allow(t) {
				if _, ok := prev[t]; !ok {
					prev[t] = s
					queue = append(queue, t)
				}
			}
		}
	}
	return nil
}

// tarjanSCC computes the strongly connected components of the subgraph
// induced by inRegion, iterating nodes in the given deterministic order.
func tarjanSCC(order []string, adj map[string][]string, inRegion func(string) bool) [][]string {
	index := make(map[string]int)
	low := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var sccs [][]string
	counter := 0

	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if !inRegion(w) {
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
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for _, v := range order {
		if !inRegion(v) {
			continue
		}
		if _, seen := index[v]; !seen {
			strongconnect(v)
		}
	}
	return sccs
}

// isCyclicSCC reports whether the SCC admits an infinite run: either it
// has more than one state, or its single state has a self-loop.
func isCyclicSCC(scc []string, adj map[string][]string) bool {
	if len(scc) > 1 {
		return true
	}
	for _, t := range adj[scc[0]] {
		if t == scc[0] {
			return true
		}
	}
	return false
}

// cycleThrough returns a closed loop [s, ..., s] contained in the SCC.
func cycleThrough(s string, sccSet map[string]bool, adj map[string][]string) []string {
	for _, t := range adj[s] {
		if t == s {
			return []string{s, s}
		}
	}
	allow := func(x string) bool { return sccSet[x] }
	for _, t := range adj[s] {
		if !sccSet[t] {
			continue
		}
		if back := bfsPath(adj, t, s, allow); back != nil {
			return append([]string{s}, back...)
		}
	}
	return nil
}

// LivenessResult is the outcome of checking G(request -> F response):
// every request is eventually followed by a response.
type LivenessResult struct {
	OK       bool
	ReqState string   // state where the pending request is issued
	Prefix   []string // finite prefix: initial .. cycle entry
	Cycle    []string // closed loop, first element == last element
}

// CheckLiveness verifies that on every infinite run from the initial
// state, whenever request holds, response holds at some later point.
//
// A violation requires a genuine lasso: a reachable state where request
// is pending, from which a cycle is reachable that never visits a
// response state. A finite path without a response is NOT a violation,
// because it may still be extended to one.
func (m *Machine) CheckLiveness(reqProp, respProp string) LivenessResult {
	reachable := m.ReachableSet()
	inRegion := func(s string) bool {
		return reachable[s] && !m.HasLabel(s, respProp)
	}

	sccs := tarjanSCC(m.Order, m.Adj, inRegion)
	for _, scc := range sccs {
		if !isCyclicSCC(scc, m.Adj) {
			continue
		}
		sccSet := make(map[string]bool)
		for _, s := range scc {
			sccSet[s] = true
		}
		// States inside the response-free region that can reach this SCC
		// without leaving the region (reverse BFS from the SCC).
		canReach := make(map[string]bool)
		var queue []string
		for _, s := range scc {
			canReach[s] = true
			queue = append(queue, s)
		}
		for len(queue) > 0 {
			s := queue[0]
			queue = queue[1:]
			for _, cand := range m.Order {
				if !inRegion(cand) || canReach[cand] {
					continue
				}
				for _, t := range m.Adj[cand] {
					if t == s {
						canReach[cand] = true
						queue = append(queue, cand)
						break
					}
				}
			}
		}
		// Pick the request state closest to the initial state (BFS order)
		// for a concise witness.
		reqState := ""
		bfsOrder := []string{m.Initial}
		seen := map[string]bool{m.Initial: true}
		for len(bfsOrder) > 0 {
			s := bfsOrder[0]
			bfsOrder = bfsOrder[1:]
			if m.HasLabel(s, reqProp) && inRegion(s) && canReach[s] {
				reqState = s
				break
			}
			for _, t := range m.Adj[s] {
				if reachable[t] && !seen[t] {
					seen[t] = true
					bfsOrder = append(bfsOrder, t)
				}
			}
		}
		if reqState == "" {
			continue
		}
		// Assemble the lasso: initial -> reqState -> SCC entry -> cycle.
		entry := ""
		for _, s := range scc {
			entry = s
			break
		}
		allowReachable := func(s string) bool { return reachable[s] }
		prefixA := bfsPath(m.Adj, m.Initial, reqState, allowReachable)
		prefixB := bfsPath(m.Adj, reqState, entry, inRegion)
		if prefixA == nil || prefixB == nil {
			continue
		}
		prefix := append(append([]string{}, prefixA...), prefixB[1:]...)
		cycle := cycleThrough(entry, sccSet, m.Adj)
		if cycle == nil {
			continue
		}
		return LivenessResult{
			OK:       false,
			ReqState: reqState,
			Prefix:   prefix,
			Cycle:    cycle,
		}
	}
	return LivenessResult{OK: true}
}

// SafetyResult is the outcome of checking that danger is never entered
// without a strictly earlier authorization on the same run.
type SafetyResult struct {
	OK   bool
	Path []string // run to a danger state with no prior authorized state
}

// CheckSafety verifies that every reachable danger state is preceded by
// an authorized state on the same run. Equivalently: no run from the
// initial state reaches danger while staying outside authorized states.
// Authorization on the danger state itself does not count; it must hold
// strictly before entering danger.
func (m *Machine) CheckSafety(authProp, dangerProp string) SafetyResult {
	if m.HasLabel(m.Initial, dangerProp) {
		return SafetyResult{OK: false, Path: []string{m.Initial}}
	}
	if m.HasLabel(m.Initial, authProp) {
		// Every run starts authorized, so danger is always preceded
		// by an authorization.
		return SafetyResult{OK: true}
	}
	// BFS restricted to states that are neither authorized nor danger.
	// Any danger state adjacent to this region is reachable without a
	// prior authorization.
	prev := map[string]string{m.Initial: ""}
	queue := []string{m.Initial}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, t := range m.Adj[s] {
			if m.HasLabel(t, dangerProp) {
				path := []string{t}
				for cur := s; ; cur = prev[cur] {
					path = append([]string{cur}, path...)
					if cur == m.Initial {
						break
					}
				}
				return SafetyResult{OK: false, Path: path}
			}
			if m.HasLabel(t, authProp) {
				continue
			}
			if _, ok := prev[t]; !ok {
				prev[t] = s
				queue = append(queue, t)
			}
		}
	}
	return SafetyResult{OK: true}
}
