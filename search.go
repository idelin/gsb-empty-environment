package ambiguity

// Result is the outcome of FindAmbiguous.
type Result struct {
	// Found reports whether an ambiguous string was located.
	Found bool
	// Exhausted reports that the state budget ran out before the search
	// space (all strings of length <= MaxLen) was covered; the absence of
	// a result is then inconclusive.
	Exhausted bool
	// String is the shortest ambiguous string; among strings of that
	// length it is first in the grammar's terminal order.
	String []Symbol
	// Trees holds two distinct parse trees of String, for independent
	// verification.
	Trees []*Tree
	// States is the number of expanded states (production x split
	// attempts) consumed by the search.
	States int
}

// FindAmbiguous searches all terminal strings of length 1..maxLen, shortest
// first and, within one length, in the order induced by g.Terminals. It
// returns the first string having two distinct parse trees.
//
// budget caps the total number of expanded states across all candidate
// strings; a negative budget means unlimited.
func FindAmbiguous(g *Grammar, maxLen int, budget int) Result {
	b := &Budget{Limit: budget}
	nTerm := len(g.Terminals)
	for length := 1; length <= maxLen; length++ {
		if nTerm == 0 {
			break
		}
		idx := make([]int, length)
		for {
			s := make([]Symbol, length)
			for m, ti := range idx {
				s[m] = g.Terminals[ti]
			}
			root, ok := buildForest(g, s, b)
			if !ok {
				return Result{Exhausted: true, States: b.Used}
			}
			if root != nil && countTrees(root, 2, map[*Node]int{}) >= 2 {
				return Result{
					Found:  true,
					String: s,
					Trees:  extractTrees(root, 2),
					States: b.Used,
				}
			}
			if !nextIndices(idx, nTerm) {
				break
			}
		}
	}
	return Result{States: b.Used}
}

// nextIndices advances idx to the next tuple in lexicographic order over
// the given base. It reports false when idx wrapped past the last tuple.
func nextIndices(idx []int, base int) bool {
	if base <= 0 {
		return false
	}
	for p := len(idx) - 1; p >= 0; p-- {
		if idx[p]+1 < base {
			idx[p]++
			for q := p + 1; q < len(idx); q++ {
				idx[q] = 0
			}
			return true
		}
	}
	return false
}
