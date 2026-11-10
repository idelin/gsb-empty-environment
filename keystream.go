package main

import (
	"encoding/hex"
	"sort"
)

func (g *groupAccumulator) buildKeystream() ([]keystreamReport, int) {
	positions := make([]uint64, 0, len(g.states))
	for position, state := range g.states {
		if len(state.candidates) > 0 {
			positions = append(positions, position)
		}
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i] < positions[j] })

	reports := make([]keystreamReport, 0, len(positions))
	conflicts := 0
	for _, position := range positions {
		state := g.states[position]
		candidates := make([]keystreamCandidate, 0, len(state.order))
		for _, value := range state.order {
			candidates = append(candidates, keystreamCandidate{
				KeystreamByte: hex.EncodeToString([]byte{value}),
				Evidence:      state.candidates[value],
			})
		}
		conflict := len(candidates) > 1
		if conflict {
			conflicts++
		}
		reports = append(reports, keystreamReport{
			Position:      position,
			Block:         position / blockSize,
			OffsetInBlock: int(position % blockSize),
			Conflict:      conflict,
			Candidates:    candidates,
		})
	}
	return reports, conflicts
}
