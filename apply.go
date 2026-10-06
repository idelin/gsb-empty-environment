package patchmerge

import "fmt"

// Provenance describes the origin of a single output byte: either a kept
// baseline byte or a byte produced by an insertion edit.
type Provenance struct {
	Insert  bool   // true if this byte came from an insertion
	Source  string // primary source ID of the insertion (Insert only)
	EditIdx int    // index into the merged edit slice (Insert only)
	TextOff int    // offset within the inserted text (Insert only)
	BaseOff int    // baseline byte offset (kept baseline bytes only)
}

// Apply applies merged (non-conflicting) edits to the baseline and returns
// the output text together with a per-byte provenance model: prov[i] tells
// exactly where output byte i came from.
//
// Application walks the baseline left to right. Insertions anchored at
// offset o are emitted when the walk reaches o, before the baseline byte at
// o; deleted bytes are skipped. Insertions at the same offset are emitted in
// the order they appear in edits (Merge orders them by source ID).
func Apply(baseline string, edits []MergedEdit) (string, []Provenance) {
	insAt := map[int][]int{} // baseline offset -> edit indices of insertions
	var dels [][2]int
	for i, e := range edits {
		switch e.Kind {
		case Insert:
			insAt[e.Pos] = append(insAt[e.Pos], i)
		case Delete:
			dels = append(dels, [2]int{e.Pos, e.End})
		}
	}
	deleted := make([]bool, len(baseline))
	for _, d := range dels {
		for o := d[0]; o < d[1] && o < len(baseline); o++ {
			if o >= 0 {
				deleted[o] = true
			}
		}
	}

	var out []byte
	var prov []Provenance
	for o := 0; o <= len(baseline); o++ {
		for _, idx := range insAt[o] {
			e := edits[idx]
			src := ""
			if len(e.Sources) > 0 {
				src = e.Sources[0]
			}
			for t := 0; t < len(e.Text); t++ {
				out = append(out, e.Text[t])
				prov = append(prov, Provenance{Insert: true, Source: src, EditIdx: idx, TextOff: t})
			}
		}
		if o < len(baseline) && !deleted[o] {
			out = append(out, baseline[o])
			prov = append(prov, Provenance{BaseOff: o})
		}
	}
	return string(out), prov
}

// MapPos maps a baseline byte position (0..len(baseline)) to the
// corresponding output position using pure arithmetic on the edit list.
// The mapping is left-leaning: insertions anchored exactly at pos are
// counted as after the mapped position.
func MapPos(pos int, edits []MergedEdit) int {
	out := pos
	for _, e := range edits {
		switch e.Kind {
		case Insert:
			if e.Pos < pos {
				out += len(e.Text)
			}
		case Delete:
			if e.Pos < pos {
				end := e.End
				if end > pos {
					end = pos
				}
				out -= end - e.Pos
			}
		}
	}
	return out
}

// VerifyOffsets cross-checks the arithmetic offset translation (MapPos)
// against the per-byte provenance model produced by Apply. It returns an
// error describing the first inconsistency, or nil if the two models agree
// for every baseline position and every output byte.
func VerifyOffsets(baseline string, edits []MergedEdit, out string, prov []Provenance) error {
	if len(out) != len(prov) {
		return fmt.Errorf("output has %d bytes but provenance has %d entries", len(out), len(prov))
	}

	// 1. Every output byte matches its declared origin, kept baseline bytes
	//    appear in strictly increasing order, and inserted bytes match the
	//    text of their edit.
	lastBase := -1
	for i, p := range prov {
		if p.Insert {
			if p.EditIdx < 0 || p.EditIdx >= len(edits) {
				return fmt.Errorf("output %d: bad edit index %d", i, p.EditIdx)
			}
			e := edits[p.EditIdx]
			if e.Kind != Insert {
				return fmt.Errorf("output %d: edit %d is not an insertion", i, p.EditIdx)
			}
			if p.TextOff < 0 || p.TextOff >= len(e.Text) || out[i] != e.Text[p.TextOff] {
				return fmt.Errorf("output %d: inserted byte does not match edit %d text", i, p.EditIdx)
			}
		} else {
			if p.BaseOff <= lastBase {
				return fmt.Errorf("output %d: baseline offset %d out of order", i, p.BaseOff)
			}
			if p.BaseOff >= len(baseline) || out[i] != baseline[p.BaseOff] {
				return fmt.Errorf("output %d: byte does not match baseline offset %d", i, p.BaseOff)
			}
			lastBase = p.BaseOff
		}
	}

	// 2. Every baseline byte is either deleted (covered by a delete edit) or
	//    present exactly once in the output.
	seen := make([]bool, len(baseline))
	for _, p := range prov {
		if !p.Insert {
			if seen[p.BaseOff] {
				return fmt.Errorf("baseline offset %d appears twice in output", p.BaseOff)
			}
			seen[p.BaseOff] = true
		}
	}
	for o := 0; o < len(baseline); o++ {
		if seen[o] {
			continue
		}
		if !coveredByDelete(o, edits) {
			return fmt.Errorf("baseline offset %d neither deleted nor present in output", o)
		}
	}

	// 3. For every baseline position o, the arithmetic mapping MapPos(o)
	//    must equal the provenance-derived position: the index of the first
	//    output byte whose anchor (insertion point or baseline offset) is
	//    >= o.
	anchor := make([]int, len(prov))
	for i, p := range prov {
		if p.Insert {
			anchor[i] = edits[p.EditIdx].Pos
		} else {
			anchor[i] = p.BaseOff
		}
	}
	for o := 0; o <= len(baseline); o++ {
		idx := len(prov)
		for i := 0; i < len(prov); i++ {
			if anchor[i] >= o {
				idx = i
				break
			}
		}
		if got := MapPos(o, edits); got != idx {
			return fmt.Errorf("baseline position %d: arithmetic mapping %d != provenance mapping %d", o, got, idx)
		}
	}
	return nil
}

func coveredByDelete(off int, edits []MergedEdit) bool {
	for _, e := range edits {
		if e.Kind == Delete && off >= e.Pos && off < e.End {
			return true
		}
	}
	return false
}
