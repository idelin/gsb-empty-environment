package capmig

import "math/bits"

// event is one causally ordered state-machine input for one token.
type event struct {
	order        uint64 // (epoch << 1) | kind, kind 0 = revocation, 1 = handoff
	handoffIdx   int    // -1 for revocations
	isRevoc      bool
	receiptEpoch uint64
}

// tokenState is the per-token result of one simulated history.
type tokenState struct {
	revoked bool
	holder  int // subject index; -1 while the token is unheld-but-live
}

// world holds one full enumeration pass: per-token states plus per-handoff
// success flags packed by handoff ordinal.
type world struct {
	states []tokenState
	// hopSuccess word k holds handoff ordinals [64k, 64k+63).
	hopSuccess []uint64
}

type engine struct {
	in  *Input
	idx *index

	numAmb      int
	numHist     uint64
	numSubjects int
	numTokens   int
	numHandoffs int

	// handoffOrdinal maps handoff slice index -> dense ordinal.
	handoffOrdinal []int
	// eventOrder groups ordered events per token.
	eventOrder map[string][]event
	// rejected[handoff ordinal] is true when the receipt is below the barrier.
	rejected []bool
	// ambiguous[handoff ordinal] is the variable index or -1.
	ambiguousOfHop []int

	// confirmedOK / admissible use one bit per history mask.
	confirmedOK []uint64
	admissible  []uint64

	// Vectorised per-token states and per-handoff success over all masks.
	tokenBits       []tokenBits
	hopSuccessBits  [][]uint64
	varOneWords     [][]uint64
	exclusivityHeld bool

	// worldCache memoises full simulations keyed by mask.
	worldCache map[uint64]*world
	hopWords   int
}

func newEngine(in *Input, idx *index) *engine {
	numAmb := len(idx.ambiguousHops)
	numHist := uint64(1) << numAmb
	words := int((numHist + 63) / 64)

	e := &engine{
		in:             in,
		idx:            idx,
		numAmb:         numAmb,
		numHist:        numHist,
		numSubjects:    len(idx.subjects),
		numTokens:      len(in.Grants),
		numHandoffs:    len(in.Handoffs),
		handoffOrdinal: make([]int, len(in.Handoffs)),
		eventOrder:     map[string][]event{},
		rejected:       make([]bool, len(in.Handoffs)),
		ambiguousOfHop: make([]int, len(in.Handoffs)),
		confirmedOK:    make([]uint64, words),
		admissible:     make([]uint64, words),
		varOneWords:    make([][]uint64, numAmb),
		worldCache:     map[uint64]*world{},
		hopWords:       (len(in.Handoffs) + 63) / 64,
	}
	for varIdx := 0; varIdx < numAmb; varIdx++ {
		row := make([]uint64, words)
		bit := uint(varIdx)
		for w := 0; w < words; w++ {
			base := uint64(w) * 64
			var v uint64
			for b := uint(0); b < 64; b++ {
				if (base+uint64(b))&(1<<bit) != 0 {
					v |= 1 << b
				}
			}
			row[w] = v
		}
		if rem := e.numHist % 64; rem != 0 {
			row[words-1] &= (uint64(1) << rem) - 1
		}
		e.varOneWords[varIdx] = row
	}
	for i := range e.ambiguousOfHop {
		e.ambiguousOfHop[i] = -1
	}
	for ord, hi := range idx.handoffs {
		e.handoffOrdinal[hi] = ord
	}
	for varIdx, hi := range idx.ambiguousHops {
		e.ambiguousOfHop[hi] = varIdx
	}

	// Revocation events sort before same-epoch handoff events (fail-closed).
	for ri, r := range in.Revocations {
		_ = ri
		e.eventOrder[r.Token] = append(e.eventOrder[r.Token], event{
			order:      r.Epoch << 1,
			handoffIdx: -1,
			isRevoc:    true,
		})
	}
	for hi, h := range in.Handoffs {
		receiptEpoch := h.ReceiptEpoch
		if receiptEpoch == 0 {
			receiptEpoch = h.Epoch
		}
		rejected := receiptEpoch < in.BarrierEpoch
		e.rejected[hi] = rejected
		if rejected {
			// The late receipt is inadmissible: it creates no state change.
			continue
		}
		e.eventOrder[h.Token] = append(e.eventOrder[h.Token], event{
			order:        h.Epoch<<1 | 1,
			handoffIdx:   hi,
			receiptEpoch: receiptEpoch,
		})
	}
	for tok := range e.eventOrder {
		evs := e.eventOrder[tok]
		// Insertion order is already causal; stable-sort defensively.
		for i := 1; i < len(evs); i++ {
			for j := i; j > 0 && evs[j-1].order > evs[j].order; j-- {
				evs[j-1], evs[j] = evs[j], evs[j-1]
			}
		}
		e.eventOrder[tok] = evs
	}
	return e
}

func (e *engine) enumerate() {
	e.precompute()
}

// simulate replays every token's ordered events under one global assignment.
func (e *engine) simulate(mask uint64) *world {
	if w, ok := e.worldCache[mask]; ok {
		return w
	}
	w := &world{
		states:     make([]tokenState, e.numTokens),
		hopSuccess: make([]uint64, e.hopWords),
	}
	for gi := range w.states {
		w.states[gi] = tokenState{holder: e.subjectIndex(e.in.Grants[gi].Subject)}
	}

	for gi, g := range e.in.Grants {
		st := &w.states[gi]
		for _, ev := range e.eventOrder[g.Token] {
			if ev.isRevoc {
				st.revoked = true
				st.holder = -1
				continue
			}
			h := e.in.Handoffs[ev.handoffIdx]
			if st.revoked {
				continue
			}
			if st.holder != e.subjectIndex(h.From) {
				// Causal break: sender does not currently own the handle.
				continue
			}
			if varIdx := e.ambiguousOfHop[ev.handoffIdx]; varIdx >= 0 {
				if mask&(1<<uint(varIdx)) == 0 {
					// The lost confirmation means the transfer did not land.
					continue
				}
			}
			ord := e.handoffOrdinal[ev.handoffIdx]
			w.hopSuccess[ord/64] |= 1 << uint(ord%64)
			st.holder = e.subjectIndex(h.To)
		}
	}
	e.worldCache[mask] = w
	return w
}

func (e *engine) subjectIndex(subject string) int {
	return e.idx.subjects[subject]
}

func (e *engine) hopSucceeded(w *world, handoffIdx int) bool {
	ord := e.handoffOrdinal[handoffIdx]
	return w.hopSuccess[ord/64]&(1<<uint(ord%64)) != 0
}

func (e *engine) admissibleCount() uint64 {
	var n uint64
	for _, word := range e.admissible {
		n += uint64(bits.OnesCount64(word))
	}
	return n
}

func (e *engine) isAdmissible(mask uint64) bool {
	return e.admissible[mask/64]&(1<<uint(mask%64)) != 0
}
