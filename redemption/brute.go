package redemption

// BruteForce enumerates every one of the 2^N candidate subsets (N <= 22) and
// returns the feasible one with the greatest number of completed service
// entitlements; ties are broken by the lexicographic order of the ascending
// transaction-ID sequence. It is the independent oracle used by the test
// suite against the branch-and-bound solver.
func (l *Ledger) BruteForce() (bestMask uint32, bestValue int64) {
	n := l.N()
	total := uint32(1) << uint(n)
	for mask := uint32(0); mask < total; mask++ {
		ok, _ := l.Feasible(mask)
		if !ok {
			continue
		}
		value := l.Entitlements(mask)
		if value > bestValue || (value == bestValue && l.lexLess(mask, bestMask)) {
			bestValue = value
			bestMask = mask
		}
	}
	return bestMask, bestValue
}

// lexLess compares two subsets under the declared tie-break: lexicographic
// order on the ascending transaction-ID sequence, with a strict prefix
// considered smaller. Candidates are indexed in ascending ID order, so this
// is exactly the bitmask one-wins comparison at the lowest differing bit.
func (l *Ledger) lexLess(a, b uint32) bool {
	diff := a ^ b
	if diff == 0 {
		return false
	}
	first := diff & -diff
	return a&first != 0
}
