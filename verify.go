package tasksched

// LexMinOrder independently computes the lexicographically smallest
// topological order by exhaustively enumerating every valid order.
// It is the oracle used to cross-check Sort. ok is false when the
// constraints contain a cycle. Only practical for small n.
func LexMinOrder(n int, edges []Edge) (best []int, ok bool) {
	preds := make([][]int, n)
	seen := make(map[Edge]bool)
	for _, e := range edges {
		if e.From == e.To {
			return nil, false
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		preds[e.To] = append(preds[e.To], e.From)
	}

	used := make([]bool, n)
	cur := make([]int, 0, n)
	var backtrack func()
	backtrack = func() {
		if len(cur) == n {
			if best == nil || lexLess(cur, best) {
				best = append([]int(nil), cur...)
			}
			return
		}
		for v := 0; v < n; v++ {
			if used[v] || !allUsed(preds[v], used) {
				continue
			}
			if best != nil && !prefixCanBeat(cur, v, best) {
				continue
			}
			used[v] = true
			cur = append(cur, v)
			backtrack()
			cur = cur[:len(cur)-1]
			used[v] = false
		}
	}
	backtrack()
	if best == nil {
		return nil, false
	}
	return best, true
}

func allUsed(preds []int, used []bool) bool {
	for _, p := range preds {
		if !used[p] {
			return false
		}
	}
	return true
}

func lexLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// prefixCanBeat reports whether appending v to cur can still lead to an
// order lexicographically smaller than best.
func prefixCanBeat(cur []int, v int, best []int) bool {
	for i := range cur {
		if cur[i] != best[i] {
			return cur[i] < best[i]
		}
	}
	return v <= best[len(cur)]
}
