package main

import (
	"encoding/hex"
	"sort"
)

func (g *groupAccumulator) buildFragments() ([]fragmentReport, int) {
	ordered := append([]*decodedFragment(nil), g.fragments...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].absStart != ordered[j].absStart {
			return ordered[i].absStart < ordered[j].absStart
		}
		return ordered[i].frag.ID < ordered[j].frag.ID
	})

	reports := make([]fragmentReport, 0, len(ordered))
	propagated := 0
	for _, df := range ordered {
		fr := fragmentReport{
			FragmentID: df.frag.ID,
			AbsStart:   df.absStart,
			AbsEnd:     df.absEnd,
			KnownCount: len(df.known),
			Recovered:  make([]recoveredByte, 0),
		}
		for offset := range df.ks {
			position := df.absStart + uint64(offset)
			state := g.states[position]
			if state == nil || len(state.candidates) == 0 {
				continue
			}
			conflict := len(state.candidates) > 1
			if conflict {
				fr.ConflictCount++
			}
			for _, value := range state.order {
				ev := state.candidates[value]
				fromSelf := ev.FragmentID == df.frag.ID
				if !fromSelf {
					propagated++
				}
				fr.Recovered = append(fr.Recovered, recoveredByte{
					Position:           position,
					Offset:             offset,
					PlaintextByte:      hex.EncodeToString([]byte{df.ks[offset] ^ value}),
					Source:             ev,
					FromKnownPlaintext: fromSelf,
					Conflict:           conflict,
				})
			}
		}
		fr.RecoveredCount = len(fr.Recovered) - fr.KnownCount
		reports = append(reports, fr)
	}
	return reports, propagated
}
