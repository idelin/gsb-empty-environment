// Package patchmerge merges two text patch sets that are both expressed in
// baseline byte coordinates.
//
// Merge rules:
//   - Identical insertions (same offset, same text) from both sets are
//     deduplicated into a single edit.
//   - Different insertions at the same offset are ordered by source ID.
//   - A deletion conflicts with an insertion from the other set whose point
//     lies strictly inside the deleted range. Insertions exactly at either
//     endpoint of a deletion do not conflict.
//   - Identical deletions are deduplicated; overlapping deletions are merged
//     into their union.
//
// Merge reports the conflicting baseline ranges separately and keeps the
// mergeable edits sorted and ready to apply.
package patchmerge

import "sort"

// Kind identifies the type of an Edit.
type Kind int

const (
	Insert Kind = iota
	Delete
)

// Edit is a single text patch in baseline byte coordinates.
type Edit struct {
	Kind Kind
	Pos  int    // Insert: insertion point. Delete: start of deleted range.
	End  int    // Delete: end of deleted range (exclusive). Unused for Insert.
	Text string // Insert: text to insert.
}

// PatchSet is a set of edits from one source, all in baseline byte
// coordinates. A set must be internally consistent: deletions do not
// overlap and no insertion point lies strictly inside one of its own
// deletions.
type PatchSet struct {
	ID    string
	Edits []Edit
}

// MergedEdit is an edit in the merged output. Sources lists the IDs of the
// patch sets it came from (more than one when deduplicated).
type MergedEdit struct {
	Edit
	Sources []string
}

// Conflict describes a deletion that overlaps an insertion from the other
// set at a point strictly inside the deletion. Start and End are the
// baseline byte range of the deletion.
type Conflict struct {
	Deletion  MergedEdit
	Insertion MergedEdit
	Start     int
	End       int
}

// Result is the outcome of Merge: Merged holds the non-conflicting edits,
// sorted and ready to apply; Conflicts holds the excluded edit pairs.
type Result struct {
	Merged    []MergedEdit
	Conflicts []Conflict
}

type insKey struct {
	pos  int
	text string
}

type delKey struct {
	pos int
	end int
}

// Merge combines patch sets a and b into a single edit list plus a list of
// conflicts, following the rules documented on the package.
func Merge(a, b PatchSet) *Result {
	sets := []PatchSet{a, b}

	insSrc := map[insKey][]string{}
	delSrc := map[delKey][]string{}
	for _, s := range sets {
		seenIns := map[insKey]bool{}
		seenDel := map[delKey]bool{}
		for _, e := range s.Edits {
			switch e.Kind {
			case Insert:
				k := insKey{e.Pos, e.Text}
				if !seenIns[k] {
					seenIns[k] = true
					insSrc[k] = append(insSrc[k], s.ID)
				}
			case Delete:
				k := delKey{e.Pos, e.End}
				if !seenDel[k] {
					seenDel[k] = true
					delSrc[k] = append(delSrc[k], s.ID)
				}
			}
		}
	}

	dels := make([]delKey, 0, len(delSrc))
	for k := range delSrc {
		dels = append(dels, k)
	}
	sort.Slice(dels, func(i, j int) bool {
		if dels[i].pos != dels[j].pos {
			return dels[i].pos < dels[j].pos
		}
		return dels[i].end < dels[j].end
	})
	inss := make([]insKey, 0, len(insSrc))
	for k := range insSrc {
		inss = append(inss, k)
	}
	sort.Slice(inss, func(i, j int) bool {
		if inss[i].pos != inss[j].pos {
			return inss[i].pos < inss[j].pos
		}
		return inss[i].text < inss[j].text
	})

	// A deletion conflicts with an insertion from a *different* source whose
	// point lies strictly inside the deleted range. Endpoint insertions
	// (pos == del.Pos or pos == del.End) never conflict.
	conflictDel := map[delKey]bool{}
	conflictIns := map[insKey]bool{}
	var conflicts []Conflict
	for _, d := range dels {
		for _, in := range inss {
			if in.pos <= d.pos || in.pos >= d.end {
				continue
			}
			if !crossSource(delSrc[d], insSrc[in]) {
				continue
			}
			conflictDel[d] = true
			conflictIns[in] = true
			conflicts = append(conflicts, Conflict{
				Deletion:  MergedEdit{Edit: Edit{Kind: Delete, Pos: d.pos, End: d.end}, Sources: sortedSources(delSrc[d])},
				Insertion: MergedEdit{Edit: Edit{Kind: Insert, Pos: in.pos, End: in.pos, Text: in.text}, Sources: sortedSources(insSrc[in])},
				Start:     d.pos,
				End:       d.end,
			})
		}
	}

	// Mergeable deletions: drop conflicting ones, then union overlapping or
	// adjacent intervals, combining their sources.
	var merged []MergedEdit
	for _, d := range dels {
		if conflictDel[d] {
			continue
		}
		srcs := sortedSources(delSrc[d])
		if n := len(merged); n > 0 && merged[n-1].Kind == Delete && d.pos <= merged[n-1].End {
			if d.end > merged[n-1].End {
				merged[n-1].End = d.end
			}
			merged[n-1].Sources = unionSources(merged[n-1].Sources, srcs)
			continue
		}
		merged = append(merged, MergedEdit{Edit: Edit{Kind: Delete, Pos: d.pos, End: d.end}, Sources: srcs})
	}

	// Mergeable insertions, ordered at the same point by source ID then text.
	var inserts []MergedEdit
	for _, in := range inss {
		if conflictIns[in] {
			continue
		}
		inserts = append(inserts, MergedEdit{
			Edit:    Edit{Kind: Insert, Pos: in.pos, End: in.pos, Text: in.text},
			Sources: sortedSources(insSrc[in]),
		})
	}
	sort.Slice(inserts, func(i, j int) bool {
		if inserts[i].Pos != inserts[j].Pos {
			return inserts[i].Pos < inserts[j].Pos
		}
		if inserts[i].Sources[0] != inserts[j].Sources[0] {
			return inserts[i].Sources[0] < inserts[j].Sources[0]
		}
		return inserts[i].Text < inserts[j].Text
	})

	merged = append(merged, inserts...)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Pos != merged[j].Pos {
			return merged[i].Pos < merged[j].Pos
		}
		return merged[i].Kind == Delete && merged[j].Kind == Insert
	})

	return &Result{Merged: merged, Conflicts: conflicts}
}

func crossSource(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x != y {
				return true
			}
		}
	}
	return false
}

func sortedSources(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func unionSources(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string(nil), a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
