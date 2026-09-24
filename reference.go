package cacheaudit

// referenceLinearizable is an independent, deliberately naive checker used
// to cross-validate the auditor on small histories: it enumerates every
// permutation of the ops, keeps those respecting real-time precedence, and
// tests whether any is a valid versioned-read sequence. It shares no code
// with the memoized search in linearizable.
func referenceLinearizable(ops []op) bool {
	n := len(ops)
	used := make([]bool, n)
	order := make([]int, 0, n)
	found := false
	var rec func()
	rec = func() {
		if found {
			return
		}
		if len(order) == n {
			if validSequence(ops, order) {
				found = true
			}
			return
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			order = append(order, i)
			rec()
			order = order[:len(order)-1]
			used[i] = false
		}
	}
	rec()
	return found
}

// validSequence reports whether order is a precedence-respecting
// linearization in which every get reads the latest published version.
func validSequence(ops []op, order []int) bool {
	pos := make([]int, len(ops))
	for p, idx := range order {
		pos[idx] = p
	}
	for i := range ops {
		for j := range ops {
			// j ends before i starts => j must be ordered before i.
			if i != j && ops[j].end.Before(ops[i].start) && pos[j] > pos[i] {
				return false
			}
		}
	}
	var ver uint64
	for _, idx := range order {
		if ops[idx].get {
			if ops[idx].version != ver {
				return false
			}
		} else {
			ver = ops[idx].version
		}
	}
	return true
}

// bruteLinearizable is the no-memo fallback for histories too large for the
// bitmask memo. It is exponential and only a safety net.
func bruteLinearizable(ops []op) bool {
	n := len(ops)
	placed := make([]bool, n)
	var dfs func(ver uint64) bool
	dfs = func(ver uint64) bool {
		done := true
		for i := 0; i < n; i++ {
			if placed[i] {
				continue
			}
			done = false
			ready := true
			for j := 0; j < n; j++ {
				if j != i && !placed[j] && ops[j].end.Before(ops[i].start) {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if ops[i].get && ops[i].version != ver {
				continue
			}
			placed[i] = true
			next := ver
			if !ops[i].get {
				next = ops[i].version
			}
			if dfs(next) {
				return true
			}
			placed[i] = false
		}
		return done
	}
	return dfs(0)
}
