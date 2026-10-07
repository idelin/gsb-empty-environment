// Package intervalidx implements a versioned interval index.
//
// The index stores a set of pairwise-disjoint half-open intervals [Start, End),
// each carrying a value. Every update (Set/Delete) produces a new immutable
// version; older versions remain queryable as snapshots. Updates split
// existing segments at range boundaries, and adjacent segments holding equal
// values are merged. Empty ranges (Start >= End) are no-ops and produce no
// new version. Endpoints are arbitrary rationals (*math/big.Rat).
package intervalidx

import (
	"fmt"
	"math/big"
	"reflect"
	"sort"
)

// Segment is a half-open interval [Start, End) with an associated value.
// Segments within one version are sorted, non-overlapping, and no two
// adjacent segments hold equal values.
type Segment struct {
	Start *big.Rat
	End   *big.Rat
	Value interface{}
}

// Index is a versioned interval map. The zero value is not ready for use;
// create one with New.
type Index struct {
	// versions[i] is the immutable segment list of version i.
	// versions[0] is always the empty map.
	versions [][]Segment
}

// New returns an empty index whose only version is 0 (the empty map).
func New() *Index {
	return &Index{versions: [][]Segment{{}}}
}

// NumVersions returns the number of versions, i.e. valid version ids are
// 0 .. NumVersions()-1.
func (ix *Index) NumVersions() int { return len(ix.versions) }

// Latest returns the id of the most recent version.
func (ix *Index) Latest() int { return len(ix.versions) - 1 }

// Set assigns value to the half-open range [start, end) and returns the new
// version id. Existing segments overlapping the range are split and the
// overlapped parts are replaced; adjacent segments with equal values are
// merged afterwards. If start >= end nothing changes and the current latest
// version id is returned without creating a new version.
func (ix *Index) Set(start, end *big.Rat, value interface{}) int {
	if start.Cmp(end) >= 0 {
		return ix.Latest()
	}
	segs := cut(ix.versions[ix.Latest()], start, end)
	segs = append(segs, Segment{Start: start, End: end, Value: value})
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start.Cmp(segs[j].Start) < 0 })
	ix.versions = append(ix.versions, merge(segs))
	return ix.Latest()
}

// Delete removes coverage of the half-open range [start, end) and returns the
// new version id. Overlapping segments are split and the overlapped parts
// discarded. If start >= end nothing changes and the current latest version
// id is returned without creating a new version.
func (ix *Index) Delete(start, end *big.Rat) int {
	if start.Cmp(end) >= 0 {
		return ix.Latest()
	}
	ix.versions = append(ix.versions, merge(cut(ix.versions[ix.Latest()], start, end)))
	return ix.Latest()
}

// Get returns the value covering point in the given version, and whether any
// segment covers it.
func (ix *Index) Get(version int, point *big.Rat) (interface{}, bool) {
	segs := ix.versions[version]
	i := sort.Search(len(segs), func(i int) bool { return segs[i].End.Cmp(point) > 0 })
	if i < len(segs) && segs[i].Start.Cmp(point) <= 0 {
		return segs[i].Value, true
	}
	return nil, false
}

// Segments returns a copy of the segment list of the given version.
func (ix *Index) Segments(version int) []Segment {
	src := ix.versions[version]
	out := make([]Segment, len(src))
	copy(out, src)
	return out
}

// cut returns the segments of segs with every part overlapping the half-open
// range [start, end) removed. Segments crossing a boundary are split and
// only their non-overlapping parts are kept. The result stays sorted.
func cut(segs []Segment, start, end *big.Rat) []Segment {
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		if s.End.Cmp(start) <= 0 || s.Start.Cmp(end) >= 0 {
			out = append(out, s) // disjoint
			continue
		}
		if s.Start.Cmp(start) < 0 { // left remainder
			out = append(out, Segment{Start: s.Start, End: start, Value: s.Value})
		}
		if s.End.Cmp(end) > 0 { // right remainder
			out = append(out, Segment{Start: end, End: s.End, Value: s.Value})
		}
	}
	return out
}

// merge coalesces adjacent segments (prev.End == cur.Start) holding equal
// values. segs must be sorted and non-overlapping.
func merge(segs []Segment) []Segment {
	out := segs[:0]
	for _, s := range segs {
		if n := len(out); n > 0 && out[n-1].End.Cmp(s.Start) == 0 && reflect.DeepEqual(out[n-1].Value, s.Value) {
			out[n-1].End = s.End
			continue
		}
		out = append(out, s)
	}
	return out
}

// String renders a version's segments for debugging.
func (ix *Index) String(version int) string {
	return fmt.Sprintf("%v", ix.versions[version])
}
