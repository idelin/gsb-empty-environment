package capmig

// tokenBits is the vectorised state of one token across every history mask.
// Every field has one bit per history (mask == bit position).
type tokenBits struct {
	// revoked[w] bit b is set when the token is revoked in mask 64w+b.
	revoked []uint64
	// holder[subject][w] bit b is set when the subject holds the live token.
	holder [][]uint64
}

// precompute builds vectorised per-token states and per-handoff success bits
// in O(tokens * hops * historyWords), processing 64 histories in parallel.
func (e *engine) precompute() {
	words := len(e.admissible)
	e.tokenBits = make([]tokenBits, e.numTokens)
	e.hopSuccessBits = make([][]uint64, e.numHandoffs)

	for gi, g := range e.in.Grants {
		tb := tokenBits{
			revoked: make([]uint64, words),
			holder:  make([][]uint64, e.numSubjects),
		}
		for s := range tb.holder {
			tb.holder[s] = make([]uint64, words)
		}
		genesis := e.subjectIndex(g.Subject)
		for w := range tb.holder[genesis] {
			tb.holder[genesis][w] = ^uint64(0)
		}
		if rem := e.numHist % 64; rem != 0 {
			tb.holder[genesis][words-1] &= (uint64(1) << rem) - 1
		}

		for _, ev := range e.eventOrder[g.Token] {
			if ev.isRevoc {
				for w := 0; w < words; w++ {
					tb.revoked[w] = ^uint64(0)
					for s := range tb.holder {
						tb.holder[s][w] = 0
					}
				}
				if rem := e.numHist % 64; rem != 0 {
					mask := (uint64(1) << rem) - 1
					tb.revoked[words-1] = mask
				}
				continue
			}

			hi := ev.handoffIdx
			h := e.in.Handoffs[hi]
			fromIdx := e.subjectIndex(h.From)
			toIdx := e.subjectIndex(h.To)
			success := make([]uint64, words)

			varIdx := e.ambiguousOfHop[hi]
			fromHold := tb.holder[fromIdx]
			toHold := tb.holder[toIdx]
			for w := 0; w < words; w++ {
				can := fromHold[w] &^ tb.revoked[w]
				if varIdx >= 0 {
					can &= e.varOneWords[varIdx][w]
				}
				success[w] = can
				fromHold[w] &^= can
				toHold[w] |= can
			}
			e.hopSuccessBits[hi] = success
		}

		// Handoffs rejected by the barrier never enter eventOrder; give them
		// an explicit all-zero success vector for deterministic access.
		e.tokenBits[gi] = tb
	}
	for hi := range e.in.Handoffs {
		if e.hopSuccessBits[hi] == nil {
			e.hopSuccessBits[hi] = make([]uint64, words)
		}
	}

	e.exclusivityHeld = true
	for gi := range e.in.Grants {
		var occupied []uint64
		for _, row := range e.tokenBits[gi].holder {
			if occupied == nil {
				occupied = append([]uint64(nil), row...)
				continue
			}
			for w := range occupied {
				if occupied[w]&row[w] != 0 {
					e.exclusivityHeld = false
				}
				occupied[w] |= row[w]
			}
		}
	}

	// Admissibility: every non-ambiguous, non-rejected handoff must succeed.
	for i := range e.confirmedOK {
		e.confirmedOK[i] = ^uint64(0)
	}
	if rem := e.numHist % 64; rem != 0 {
		e.confirmedOK[words-1] &= (uint64(1) << rem) - 1
	}
	for hi := range e.in.Handoffs {
		if e.rejected[hi] || e.ambiguousOfHop[hi] >= 0 {
			continue
		}
		for w := range e.confirmedOK {
			e.confirmedOK[w] &= e.hopSuccessBits[hi][w]
		}
	}
	for i := range e.admissible {
		e.admissible[i] = e.confirmedOK[i]
	}
}

func (e *engine) coverageMaskFast(gi int, subject int) []uint64 {
	out := make([]uint64, len(e.admissible))
	hold := e.tokenBits[gi].holder[subject]
	for w := range out {
		out[w] = hold[w] &^ e.tokenBits[gi].revoked[w] & e.admissible[w]
	}
	return out
}

func (e *engine) hopSucceededMask(mask uint64, hi int) bool {
	return e.hopSuccessBits[hi][mask/64]&(1<<uint(mask%64)) != 0
}
