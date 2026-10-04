package foldindex

import (
	"hash/fnv"
	"sort"
)

// unit describes one segment of the folding correspondence: the folded
// runes [FoldStart, FoldEnd) were produced from the original runes
// [OrigStart, OrigEnd). A unit may be zero-width on the folded side when
// a rule deletes characters.
type unit struct {
	FoldStart, FoldEnd int
	OrigStart, OrigEnd int
}

// Hit is a search result mapped back to the original document.
// Start and End are rune offsets into the original text, [Start, End).
// Exact reports whether the folded hit aligned precisely with folding
// unit boundaries; it is false when the hit cuts through the middle of
// an expanded (or collapsed) sequence and the returned interval is the
// minimal original interval covering the hit.
type Hit struct {
	Start int
	End   int
	Exact bool
}

// Index is a folded-text search index over a single document. It is
// bound to the table (and its version) it was built from.
type Index struct {
	table       *Table
	original    []rune
	folded      []rune
	units       []unit
	foldToUnit  []int // folded rune offset -> index into units
	fingerprint uint64
}

// Build folds doc with table t and constructs the index together with
// the folded-to-original interval mapping.
func Build(t *Table, doc string) *Index {
	folded, units := t.fold([]rune(doc))
	foldToUnit := make([]int, len(folded))
	for i, u := range units {
		for p := u.FoldStart; p < u.FoldEnd; p++ {
			foldToUnit[p] = i
		}
	}
	h := fnv.New64a()
	h.Write([]byte(t.version))
	h.Write([]byte{0})
	h.Write([]byte(string(folded)))
	return &Index{
		table:       t,
		original:    []rune(doc),
		folded:      folded,
		units:       units,
		foldToUnit:  foldToUnit,
		fingerprint: h.Sum64(),
	}
}

// Version returns the version of the folding table this index was built
// with. Rebuilding with a new table version yields a new index whose
// cursors are incompatible with the old one.
func (ix *Index) Version() string { return ix.table.version }

// FoldedText returns the folded document the index searches over.
func (ix *Index) FoldedText() string { return string(ix.folded) }

// Search folds query with the index's table, finds all (possibly
// overlapping) occurrences in the folded text, maps every hit back to
// the minimal covering interval of the original document, and merges
// duplicate or overlapping hits.
func (ix *Index) Search(query string) []Hit {
	fq := ix.table.foldString(query)
	if len(fq) == 0 || len(fq) > len(ix.folded) {
		return nil
	}
	var hits []Hit
	for i := 0; i+len(fq) <= len(ix.folded); i++ {
		if runesEqual(ix.folded[i:i+len(fq)], fq) {
			hits = append(hits, ix.mapHit(i, i+len(fq)))
		}
	}
	return mergeHits(hits)
}

// mapHit maps the folded interval [fs, fe) to the minimal covering
// interval of the original document.
func (ix *Index) mapHit(fs, fe int) Hit {
	first := ix.units[ix.foldToUnit[fs]]
	last := ix.units[ix.foldToUnit[fe-1]]
	return Hit{
		Start: first.OrigStart,
		End:   last.OrigEnd,
		Exact: first.FoldStart == fs && last.FoldEnd == fe,
	}
}

// mergeHits sorts hits by original interval and merges duplicates and
// overlaps. A merged hit is Exact only if every constituent was Exact.
func mergeHits(hits []Hit) []Hit {
	if len(hits) == 0 {
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Start != hits[j].Start {
			return hits[i].Start < hits[j].Start
		}
		return hits[i].End < hits[j].End
	})
	out := []Hit{hits[0]}
	for _, h := range hits[1:] {
		last := &out[len(out)-1]
		if h.Start <= last.End {
			if h.End > last.End {
				last.End = h.End
			}
			last.Exact = last.Exact && h.Exact
		} else {
			out = append(out, h)
		}
	}
	return out
}
