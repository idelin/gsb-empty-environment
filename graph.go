package testsel

// reverseUnion builds a callee -> callers adjacency over the union of two
// edge sets. Propagating impact over the union of the old and new call
// graphs is what makes selection safe against changed call edges: a test
// stays connected to a changed function whether the connecting path existed
// before the change, after it, or both.
func reverseUnion(a, b []Edge) map[FuncID][]FuncID {
	rev := map[FuncID]map[FuncID]bool{}
	add := func(e Edge) {
		if rev[e.Callee] == nil {
			rev[e.Callee] = map[FuncID]bool{}
		}
		rev[e.Callee][e.Caller] = true
	}
	for _, e := range a {
		add(e)
	}
	for _, e := range b {
		add(e)
	}
	out := make(map[FuncID][]FuncID, len(rev))
	for callee, callers := range rev {
		list := make([]FuncID, 0, len(callers))
		for c := range callers {
			list = append(list, c)
		}
		out[callee] = list
	}
	return out
}

// closure returns the full reverse-dependency closure of roots: the roots
// themselves plus every function that can reach them by following rev
// (callers, callers of callers, ...).
func closure(roots []FuncID, rev map[FuncID][]FuncID) map[FuncID]bool {
	seen := make(map[FuncID]bool, len(roots))
	stack := make([]FuncID, 0, len(roots))
	for _, r := range roots {
		if !seen[r] {
			seen[r] = true
			stack = append(stack, r)
		}
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, caller := range rev[f] {
			if !seen[caller] {
				seen[caller] = true
				stack = append(stack, caller)
			}
		}
	}
	return seen
}

// dynamicCallers returns every function that dynamically invokes any of the
// given method names, according to either snapshot.
func dynamicCallers(old, new *Snapshot, names []string) map[FuncID]bool {
	out := map[FuncID]bool{}
	if len(names) == 0 {
		return out
	}
	changed := make(map[string]bool, len(names))
	for _, n := range names {
		changed[n] = true
	}
	collect := func(dc map[FuncID][]string) {
		for caller, invoked := range dc {
			for _, n := range invoked {
				if changed[n] {
					out[caller] = true
				}
			}
		}
	}
	collect(old.DynCalls)
	collect(new.DynCalls)
	return out
}
