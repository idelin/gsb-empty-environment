// Package patchmerge merges two sets of text patches that are all expressed
// in baseline byte coordinates.
//
// Merge rules:
//   - Identical inserts (same position and text) are deduplicated.
//   - Different inserts at the same point are both kept and ordered by
//     source id.
//   - A delete conflicts with any insert from the other set whose position
//     lies strictly inside the deleted range.
//   - Inserts at either endpoint of a delete do not conflict with it.
//   - Overlapping deletes are compatible and both are kept (identical
//     deletes are deduplicated).
//
// The merge result separates the mergeable patches from the conflicted
// ranges. Offset conversion between baseline and merged coordinates is
// provided by MapOffset and is verified against a byte-by-byte source
// model in the package tests.
package patchmerge

import (
	"fmt"
	"sort"
)

// Kind is the kind of a Patch.
type Kind int

const (
	// Insert inserts Text at byte offset Start in the baseline.
	Insert Kind = iota
	// Delete removes the baseline byte range [Start, End).
	Delete
)

// Patch is a single edit expressed in baseline byte coordinates.
type Patch struct {
	Kind  Kind
	Start int    // baseline byte offset; insertion point for Insert
	End   int    // exclusive end for Delete; equal to Start for Insert
	Text  string // payload for Insert; empty for Delete
}

// InsertAt builds an Insert patch at byte offset pos.
func InsertAt(pos int, text string) Patch {
	return Patch{Kind: Insert, Start: pos, End: pos, Text: text}
}

// DeleteRange builds a Delete patch for the byte range [start, end).
func DeleteRange(start, end int) Patch {
	return Patch{Kind: Delete, Start: start, End: end}
}

// PatchSet is a named set of patches from one source.
type PatchSet struct {
	ID      string
	Patches []Patch
}

// MergedPatch is a patch in the merge output, tagged with its origin.
type MergedPatch struct {
	Patch
	// Source is the id of the originating patch set. For inserts
	// deduplicated across both sets it is the lexicographically smaller id.
	Source string
}

// Conflict describes a delete from one set that overlaps an insert from
// the other set at a position strictly inside the deleted range.
type Conflict struct {
	Start     int // baseline byte offset of the conflicted region
	End       int // exclusive end of the conflicted region
	Delete    Patch
	DeleteSrc string
	Insert    Patch
	InsertSrc string
}

// Result is the outcome of Merge: the mergeable patches and the
// conflicted ranges.
type Result struct {
	// Merged contains all non-conflicted patches, deduplicated and sorted
	// by (Start, Delete-before-Insert, Source, Text).
	Merged []MergedPatch
	// Conflicts contains one entry per conflicting (delete, insert) pair,
	// sorted by (Start, End, InsertSrc, Insert.Start).
	Conflicts []Conflict
}

type identity struct {
	kind       Kind
	start, end int
	text       string
}

func identOf(p Patch) identity {
	return identity{kind: p.Kind, start: p.Start, end: p.End, text: p.Text}
}

type tagged struct {
	p   Patch
	src string
}

func normalize(p Patch) (Patch, error) {
	if p.Start < 0 || p.End < p.Start {
		return p, fmt.Errorf("patchmerge: invalid range [%d, %d)", p.Start, p.End)
	}
	if p.Kind == Insert {
		p.End = p.Start
	} else {
		p.Text = ""
	}
	return p, nil
}

// Merge combines the patch sets a and b into a Result. All patches must
// use baseline byte coordinates.
func Merge(a, b PatchSet) (Result, error) {
	var all []tagged
	for _, set := range []PatchSet{a, b} {
		for _, p := range set.Patches {
			np, err := normalize(p)
			if err != nil {
				return Result{}, fmt.Errorf("patchmerge: set %q: %v", set.ID, err)
			}
			all = append(all, tagged{p: np, src: set.ID})
		}
	}

	// Conflict detection runs before deduplication so that an insert
	// present in both sets still conflicts with a delete from either set.
	conflicted := make(map[identity]bool)
	var conflicts []Conflict
	for _, d := range all {
		if d.p.Kind != Delete {
			continue
		}
		for _, ins := range all {
			if ins.p.Kind != Insert || ins.src == d.src {
				continue
			}
			// Strictly internal inserts conflict; endpoint inserts do not.
			if d.p.Start < ins.p.Start && ins.p.Start < d.p.End {
				conflicts = append(conflicts, Conflict{
					Start:     d.p.Start,
					End:       d.p.End,
					Delete:    d.p,
					DeleteSrc: d.src,
					Insert:    ins.p,
					InsertSrc: ins.src,
				})
				conflicted[identOf(d.p)] = true
				conflicted[identOf(ins.p)] = true
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		ci, cj := conflicts[i], conflicts[j]
		if ci.Start != cj.Start {
			return ci.Start < cj.Start
		}
		if ci.End != cj.End {
			return ci.End < cj.End
		}
		if ci.InsertSrc != cj.InsertSrc {
			return ci.InsertSrc < cj.InsertSrc
		}
		return ci.Insert.Start < cj.Insert.Start
	})

	// Collect mergeable patches, deduplicating identical ones and keeping
	// the lexicographically smaller source id for duplicates.
	index := make(map[identity]int)
	var merged []MergedPatch
	for _, t := range all {
		id := identOf(t.p)
		if conflicted[id] {
			continue
		}
		if i, ok := index[id]; ok {
			if t.src < merged[i].Source {
				merged[i].Source = t.src
			}
			continue
		}
		index[id] = len(merged)
		merged = append(merged, MergedPatch{Patch: t.p, Source: t.src})
	}
	sortMerged(merged)

	return Result{Merged: merged, Conflicts: conflicts}, nil
}

func sortMerged(m []MergedPatch) {
	sort.Slice(m, func(i, j int) bool {
		a, b := m[i], m[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.Kind != b.Kind {
			// Deletes sort before inserts at the same offset.
			return a.Kind == Delete
		}
		if a.Kind == Delete && a.End != b.End {
			return a.End < b.End
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Text < b.Text
	})
}

// unionDeletes merges overlapping delete intervals so that offset math
// never double-counts deleted bytes.
func unionDeletes(patches []MergedPatch) [][2]int {
	var ivs [][2]int
	for _, p := range patches {
		if p.Kind == Delete {
			ivs = append(ivs, [2]int{p.Start, p.End})
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i][0] < ivs[j][0] })
	out := ivs[:0]
	for _, iv := range ivs {
		if len(out) > 0 && iv[0] <= out[len(out)-1][1] {
			if iv[1] > out[len(out)-1][1] {
				out[len(out)-1][1] = iv[1]
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// Apply applies mergeable patches to baseline and returns the merged
// document. Patches are applied in deterministic order: at each baseline
// position, inserts at that position are emitted (ordered by source id),
// then the baseline byte is copied unless it is covered by a delete.
func Apply(baseline []byte, patches []MergedPatch) ([]byte, error) {
	n := len(baseline)
	insAt := make(map[int][]MergedPatch)
	covered := make([]bool, n)
	for _, p := range patches {
		if p.Start < 0 || p.Start > n || p.End > n {
			return nil, fmt.Errorf("patchmerge: patch %+v out of bounds for baseline of %d bytes", p.Patch, n)
		}
		switch p.Kind {
		case Insert:
			insAt[p.Start] = append(insAt[p.Start], p)
		case Delete:
			for i := p.Start; i < p.End; i++ {
				covered[i] = true
			}
		}
	}
	for _, list := range insAt {
		sortMerged(list)
	}

	var out []byte
	for pos := 0; pos <= n; pos++ {
		for _, ip := range insAt[pos] {
			out = append(out, ip.Text...)
		}
		if pos == n {
			break
		}
		if !covered[pos] {
			out = append(out, baseline[pos])
		}
	}
	return out, nil
}

// MapOffset converts a baseline byte offset into the corresponding offset
// in the merged document produced by Apply. Inserts at position p are
// emitted before baseline byte p, so they shift byte p and everything
// after it. Offsets inside a deleted range map to the position where the
// deletion happened.
func MapOffset(patches []MergedPatch, off int) int {
	delta := 0
	for _, iv := range unionDeletes(patches) {
		if iv[0] < off {
			end := iv[1]
			if end > off {
				end = off
			}
			delta -= end - iv[0]
		}
	}
	for _, p := range patches {
		if p.Kind == Insert && p.Start <= off {
			delta += len(p.Text)
		}
	}
	return off + delta
}
