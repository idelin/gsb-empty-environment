package mtusim

const maxInt = int(^uint(0) >> 1)

// Plan is the result of jointly choosing header length and alignment unit
// for a given MTU: the combination needing the fewest fragments.
type Plan struct {
	Params FragParams
	Count  int
}

// Optimize searches headerLen in [hMin,hMax] and align in [1,aMax] for the
// fragmentation of totalLen bytes under mtu that needs the fewest fragments.
// Ties break toward the smaller header, then the smaller alignment unit.
// Returns ErrKindInfeasible if no (header, align) pair can carry the datagram.
func Optimize(totalLen, mtu, hMin, hMax, aMax int) (Plan, error) {
	best := Plan{Count: maxInt}
	found := false
	for h := hMin; h <= hMax && h <= mtu; h++ {
		for a := 1; a <= aMax; a++ {
			p := FragParams{HeaderLen: h, MTU: mtu, Align: a}
			n, err := p.FragmentCount(totalLen)
			if err != nil {
				continue
			}
			if n < best.Count ||
				(n == best.Count && found &&
					(h < best.Params.HeaderLen ||
						(h == best.Params.HeaderLen && a < best.Params.Align))) {
				best = Plan{Params: p, Count: n}
				found = true
			}
		}
	}
	if !found {
		return Plan{}, errf(ErrKindInfeasible,
			"no (header, align) pair can carry %d bytes under mtu %d", totalLen, mtu)
	}
	return best, nil
}

// ExhaustiveMinFragments enumerates every legal contiguous layout of a
// totalLen-byte datagram under p and returns the minimum fragment count.
// It is the independent ground truth used to verify FragmentCount and
// Optimize on small inputs; cost grows exponentially with the length.
func ExhaustiveMinFragments(p FragParams, totalLen int) (int, bool) {
	if totalLen < 0 || p.Validate() != nil {
		return 0, false
	}
	if totalLen == 0 {
		return 1, true
	}
	maxP := p.maxPayload()
	unit := maxP / p.Align * p.Align // largest aligned non-final payload
	best := maxInt
	var rec func(off, k int)
	rec = func(off, k int) {
		rem := totalLen - off
		if rem <= maxP {
			if k+1 < best {
				best = k + 1 // final fragment: any size up to maxPayload
			}
			return
		}
		if unit <= 0 {
			return // no aligned payload fits: dead end
		}
		// Lower bound: remaining bytes need at least 1+ceil((rem-maxP)/unit)
		// more fragments; prune anything that cannot beat the best plan.
		lb := 1 + (rem-maxP+unit-1)/unit
		if k+lb >= best {
			return
		}
		// Non-final fragments: positive multiples of Align, at most maxP,
		// leaving at least one byte for the rest of the datagram. Largest
		// first so a near-optimal plan bounds the search immediately.
		limit := maxP
		if limit > rem-1 {
			limit = rem - 1
		}
		for l := limit / p.Align * p.Align; l >= p.Align; l -= p.Align {
			rec(off+l, k+1)
		}
	}
	rec(0, 0)
	if best == maxInt {
		return 0, false
	}
	return best, true
}

// ExhaustiveOptimalCount jointly minimizes the fragment count over header
// lengths [hMin,hMax] and alignment units [1,aMax] by pure enumeration of
// layouts. It verifies Optimize without sharing any of its logic.
func ExhaustiveOptimalCount(totalLen, mtu, hMin, hMax, aMax int) (int, bool) {
	best := maxInt
	for h := hMin; h <= hMax && h <= mtu; h++ {
		for a := 1; a <= aMax; a++ {
			n, ok := ExhaustiveMinFragments(FragParams{HeaderLen: h, MTU: mtu, Align: a}, totalLen)
			if ok && n < best {
				best = n
			}
		}
	}
	if best == maxInt {
		return 0, false
	}
	return best, true
}
