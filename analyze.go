package main

import (
	"fmt"
	"sort"
)

type groupAccumulator struct {
	keyHex    string
	nonceHex  string
	fragments []*decodedFragment
	coverage  map[uint64][]*decodedFragment
	states    map[uint64]*positionState
}

func analyze(input []fragment) (*report, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("input contains no fragments")
	}

	groups := make(map[string]*groupAccumulator)
	groupOrder := make([]string, 0)
	seenIDs := make(map[string]bool)
	decoded := make([]*decodedFragment, 0, len(input))

	for idx := range input {
		df, err := decodeFragment(input[idx])
		if err != nil {
			return nil, err
		}
		if seenIDs[df.frag.ID] {
			return nil, fmt.Errorf("duplicate fragment id %q", df.frag.ID)
		}
		seenIDs[df.frag.ID] = true

		key := df.keyHex + "|" + df.nonceHex
		acc, ok := groups[key]
		if !ok {
			acc = &groupAccumulator{
				keyHex:   df.keyHex,
				nonceHex: df.nonceHex,
				coverage: make(map[uint64][]*decodedFragment),
				states:   make(map[uint64]*positionState),
			}
			groups[key] = acc
			groupOrder = append(groupOrder, key)
		}
		acc.fragments = append(acc.fragments, df)
		decoded = append(decoded, df)
	}

	sort.Strings(groupOrder)
	out := &report{
		GroupCount:    len(groupOrder),
		FragmentCount: len(decoded),
		Groups:        make([]groupReport, 0, len(groupOrder)),
	}
	for _, key := range groupOrder {
		out.Groups = append(out.Groups, groups[key].build())
	}
	return out, nil
}

func (g *groupAccumulator) build() groupReport {
	for _, df := range g.fragments {
		for offset := range df.ks {
			position := df.absStart + uint64(offset)
			g.coverage[position] = append(g.coverage[position], df)
			if g.states[position] == nil {
				g.states[position] = &positionState{}
			}
		}
	}

	for _, df := range g.fragments {
		for _, kp := range df.frag.KnownPlaintext {
			for i := 0; i < len(kp.decoded); i++ {
				offset := kp.Offset + i
				position := df.absStart + uint64(offset)
				keystreamByte := df.ks[offset] ^ kp.decoded[i]
				g.states[position].add(keystreamByte, evidence{FragmentID: df.frag.ID, Offset: offset})
			}
		}
	}

	gr := groupReport{KeyID: g.keyHex, Nonce: g.nonceHex}
	gr.Overlaps = g.buildOverlaps()
	gr.Keystream, gr.ConflictCount = g.buildKeystream()
	gr.Fragments, gr.RecoveredByteCount = g.buildFragments()
	return gr
}
