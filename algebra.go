package temporal

import "sort"

// Intersect returns the set intersection a ∩ b. The returned range is empty
// when the operands do not overlap. On domain mismatch an empty range of a's
// kind is returned; use IntersectE to detect the mismatch.
func Intersect(a, b Range) Range {
	r, _ := IntersectE(a, b)
	return r
}

// IntersectE is Intersect with explicit domain-mismatch reporting.
func IntersectE(a, b Range) (Range, error) {
	a = a.Normalize()
	b = b.Normalize()
	if a.Kind != b.Kind {
		return Empty(a.Kind), kindMismatch(a, b)
	}
	if a.isEmptyCanonical() || b.isEmptyCanonical() {
		return Empty(a.Kind), nil
	}

	res := Range{Kind: a.Kind}

	// Lower bound = greatest lower bound.
	switch {
	case a.LowerUnbounded:
		res.Lower = b.Lower
		res.LowerUnbounded = b.LowerUnbounded
		res.LowerInclusive = b.LowerInclusive
	case b.LowerUnbounded:
		res.Lower = a.Lower
		res.LowerInclusive = a.LowerInclusive
	case a.Lower > b.Lower:
		res.Lower = a.Lower
		res.LowerInclusive = a.LowerInclusive
	case b.Lower > a.Lower:
		res.Lower = b.Lower
		res.LowerInclusive = b.LowerInclusive
	default:
		res.Lower = a.Lower
		// At the same boundary point, membership requires both sides.
		res.LowerInclusive = a.LowerInclusive && b.LowerInclusive
	}

	// Upper bound = least upper bound.
	switch {
	case a.UpperUnbounded:
		res.Upper = b.Upper
		res.UpperUnbounded = b.UpperUnbounded
		res.UpperInclusive = b.UpperInclusive
	case b.UpperUnbounded:
		res.Upper = a.Upper
		res.UpperInclusive = a.UpperInclusive
	case a.Upper < b.Upper:
		res.Upper = a.Upper
		res.UpperInclusive = a.UpperInclusive
	case b.Upper < a.Upper:
		res.Upper = b.Upper
		res.UpperInclusive = b.UpperInclusive
	default:
		res.Upper = a.Upper
		// At the same boundary point, membership requires both sides.
		res.UpperInclusive = a.UpperInclusive && b.UpperInclusive
	}

	return res.Normalize(), nil
}

// Intersection is an alias for Intersect.
func Intersection(a, b Range) Range { return Intersect(a, b) }

// IntersectionE is an alias for IntersectE.
func IntersectionE(a, b Range) (Range, error) { return IntersectE(a, b) }

// Intersect is the method form of the package-level Intersect.
func (r Range) Intersect(other Range) Range { return Intersect(r, other) }

// Intersection is the method form of Intersection.
func (r Range) Intersection(other Range) Range { return Intersection(r, other) }

// Overlaps reports whether a and b share at least one value. Empty ranges
// never overlap anything.
func Overlaps(a, b Range) bool {
	return !Intersect(a, b).IsEmpty()
}

// Overlap is an alias for Overlaps.
func Overlap(a, b Range) bool { return Overlaps(a, b) }

// Overlaps is the method form of Overlaps.
func (r Range) Overlaps(other Range) bool { return Overlaps(r, other) }

// Adjacent reports whether two non-empty ranges do not overlap but their
// union is a single interval with no uncovered point between them.
//
// For normalized discrete [lo, hi) ranges this is exactly a.Upper == b.Lower
// in either orientation. For continuous ranges the shared boundary value
// must be included by at least one side (e.g. [a,b) adjacent [b,c], and
// [a,b] adjacent (b,c]; [a,b) and (b,c] are NOT adjacent because b is
// uncovered).
func Adjacent(a, b Range) bool {
	a = a.Normalize()
	b = b.Normalize()
	if a.Kind != b.Kind || a.isEmptyCanonical() || b.isEmptyCanonical() {
		return false
	}
	if Overlaps(a, b) {
		return false
	}
	return meetsLeftRight(a, b) || meetsLeftRight(b, a)
}

// Adjacent is the method form of Adjacent.
func (r Range) Adjacent(other Range) bool { return Adjacent(r, other) }

// Abuts is an alias for Adjacent.
func Abuts(a, b Range) bool { return Adjacent(a, b) }

// IsAdjacent is an alias for Adjacent.
func IsAdjacent(a, b Range) bool { return Adjacent(a, b) }

func meetsLeftRight(left, right Range) bool {
	if left.UpperUnbounded || right.LowerUnbounded {
		return false
	}
	if left.Upper != right.Lower {
		return false
	}
	return left.UpperInclusive || right.LowerInclusive
}

// Union returns the set union a ∪ b as one or two disjoint ranges.
// Overlapping or adjacent inputs coalesce into a single hull; otherwise the
// two ranges are returned in left-to-right order. Empty operands are
// swallowed (the other range is returned). The result never contains empty
// ranges.
func Union(a, b Range) []Range {
	a = a.Normalize()
	b = b.Normalize()
	if a.Kind != b.Kind {
		return nil
	}
	if a.isEmptyCanonical() {
		if b.isEmptyCanonical() {
			return nil
		}
		return []Range{b}
	}
	if b.isEmptyCanonical() {
		return []Range{a}
	}

	if Overlaps(a, b) || Adjacent(a, b) {
		return []Range{hull(a, b).Normalize()}
	}
	if lowerBoundaryLess(b, a) {
		a, b = b, a
	}
	return []Range{a, b}
}

// UnionRange returns the coalesced hull and true when a and b overlap or are
// adjacent; otherwise it returns the zero range and false.
func UnionRange(a, b Range) (Range, bool) {
	u := Union(a, b)
	if len(u) == 1 && (Overlaps(a, b) || Adjacent(a, b)) {
		return u[0], true
	}
	return Range{}, false
}

// Merge is an alias for Union.
func Merge(a, b Range) []Range { return Union(a, b) }

// Union is the method form of Union.
func (r Range) Union(other Range) []Range { return Union(r, other) }

// hull builds the smallest range containing both operands. Callers ensure
// equal kind and non-emptiness.
func hull(a, b Range) Range {
	h := Range{Kind: a.Kind}

	switch {
	case a.LowerUnbounded || b.LowerUnbounded:
		h.LowerUnbounded = true
	case a.Lower < b.Lower:
		h.Lower = a.Lower
		h.LowerInclusive = a.LowerInclusive
	case b.Lower < a.Lower:
		h.Lower = b.Lower
		h.LowerInclusive = b.LowerInclusive
	default:
		h.Lower = a.Lower
		// Union includes the boundary if either side includes it.
		h.LowerInclusive = a.LowerInclusive || b.LowerInclusive
	}

	switch {
	case a.UpperUnbounded || b.UpperUnbounded:
		h.UpperUnbounded = true
	case a.Upper > b.Upper:
		h.Upper = a.Upper
		h.UpperInclusive = a.UpperInclusive
	case b.Upper > a.Upper:
		h.Upper = b.Upper
		h.UpperInclusive = b.UpperInclusive
	default:
		h.Upper = a.Upper
		h.UpperInclusive = a.UpperInclusive || b.UpperInclusive
	}
	return h
}

// Difference returns the set difference a − b as zero, one or two non-empty
// ranges. Two pieces occur when b punches an interior hole through a.
//
// The boundary inclusion of the cut is flipped relative to b: if b includes
// its lower boundary the left remnant ends exclusively there, while if b
// excludes it the remnant may include it; symmetrically for the right side.
func Difference(a, b Range) []Range {
	a = a.Normalize()
	b = b.Normalize()
	if a.Kind != b.Kind {
		return nil
	}
	if a.isEmptyCanonical() {
		return nil
	}
	if b.isEmptyCanonical() || !Overlaps(a, b) {
		return []Range{a}
	}

	var out []Range

	if !b.LowerUnbounded {
		cut := Range{
			Kind:           a.Kind,
			LowerUnbounded: true,
			Upper:          b.Lower,
			UpperInclusive: !b.LowerInclusive,
		}
		if left := Intersect(a, cut); !left.IsEmpty() {
			out = append(out, left)
		}
	}

	if !b.UpperUnbounded {
		cut := Range{
			Kind:           a.Kind,
			Lower:          b.Upper,
			LowerInclusive: !b.UpperInclusive,
			UpperUnbounded: true,
		}
		if right := Intersect(a, cut); !right.IsEmpty() {
			out = append(out, right)
		}
	}

	return out
}

// Diff is an alias for Difference.
func Diff(a, b Range) []Range { return Difference(a, b) }

// Sub is an alias for Difference.
func Sub(a, b Range) []Range { return Difference(a, b) }

// Difference is the method form of Difference.
func (r Range) Difference(other Range) []Range { return Difference(r, other) }

// Diff is the method alias of Difference.
func (r Range) Diff(other Range) []Range { return Difference(r, other) }

// lowerBoundaryLess reports whether a's lower boundary sorts before b's.
// -inf sorts first; at equal values an inclusive "[" sorts before exclusive
// "(".
func lowerBoundaryLess(a, b Range) bool {
	if a.LowerUnbounded {
		return !b.LowerUnbounded
	}
	if b.LowerUnbounded {
		return false
	}
	if a.Lower != b.Lower {
		return a.Lower < b.Lower
	}
	if a.LowerInclusive == b.LowerInclusive {
		return false
	}
	return a.LowerInclusive
}

// SortRanges returns a sorted copy ordered by lower boundary.
func SortRanges(ranges []Range) []Range {
	out := make([]Range, 0, len(ranges))
	for _, r := range ranges {
		c := r.Normalize()
		if c.IsEmpty() {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return lowerBoundaryLess(out[i], out[j])
	})
	return out
}

// UnionAll normalizes, drops empties and coalesces every overlapping or
// adjacent pair, returning the sorted disjoint connected components of the
// union. This is the "union of adjacent or overlapping rows" used by the
// foreign-key coverage rule.
func UnionAll(ranges []Range) []Range {
	sorted := SortRanges(ranges)
	if len(sorted) == 0 {
		return nil
	}

	var components []Range
	cur := sorted[0]
	for i := 1; i < len(sorted); i++ {
		next := sorted[i]
		if Overlaps(cur, next) || Adjacent(cur, next) {
			cur = hull(cur, next).Normalize()
			continue
		}
		components = append(components, cur)
		cur = next
	}
	components = append(components, cur)
	return components
}

// SubtractAll subtracts every cutter from r and returns the remaining
// disjoint fragments.
func SubtractAll(r Range, cutters []Range) []Range {
	r = r.Normalize()
	if r.IsEmpty() {
		return nil
	}
	fragments := []Range{r}
	for _, c := range cutters {
		c = c.Normalize()
		if c.Kind != r.Kind || c.IsEmpty() {
			continue
		}
		var next []Range
		for _, f := range fragments {
			next = append(next, Difference(f, c)...)
		}
		fragments = UnionAll(next)
		if len(fragments) == 0 {
			return nil
		}
	}
	return fragments
}

// CoveredBy reports whether child is fully contained in the union of covers.
// Adjacent and overlapping covers cooperate: parents [0,5) and [5,10)
// completely cover [0,10). An empty child is trivially covered. Domains are
// expected to match; mismatched covers are ignored.
func CoveredBy(child Range, covers []Range) bool {
	child = child.Normalize()
	if child.IsEmpty() {
		return true
	}
	return len(SubtractAll(child, covers)) == 0
}

// IsCoveredBy is an alias for CoveredBy.
func IsCoveredBy(child Range, covers []Range) bool { return CoveredBy(child, covers) }

// MissingCoverage returns the fragments of child not covered by covers. A
// nil/empty result means full coverage.
func MissingCoverage(child Range, covers []Range) []Range {
	return SubtractAll(child, covers)
}
