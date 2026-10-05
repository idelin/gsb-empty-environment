// Package sourcemap implements composition and querying of multi-layer
// source mappings as found in compile pipelines (transpilers, bundlers,
// minifiers). Each layer maps half-open output intervals to zero, one, or
// several half-open input intervals. Fragments with no source are
// "synthesized"; fragments with exactly one source are "copied"; fragments
// with several sources are concatenations and are never collapsed into a
// fake contiguous original interval.
package sourcemap

import "fmt"

// Span is a half-open interval [Start, End). A span with Start == End is a
// zero-width insertion point.
type Span struct {
	Start, End int
}

// Len returns the length of the span.
func (s Span) Len() int { return s.End - s.Start }

// Empty reports whether the span is a zero-width insertion point.
func (s Span) Empty() bool { return s.Start == s.End }

// Bias selects which side of a boundary owns the boundary position.
type Bias int

const (
	// Left makes a boundary position belong to the segment ending there.
	Left Bias = iota
	// Right makes a boundary position belong to the segment starting there.
	Right
)

// Segment maps the output interval Out to its source intervals.
//
//   - len(Sources) == 0: synthesized fragment, no source provenance.
//   - len(Sources) == 1: copied fragment.
//   - len(Sources) >= 2: concatenation of several source fragments; the
//     sources are laid out consecutively over Out in order.
//
// The total length of Sources always equals Out.Len().
type Segment struct {
	Out     Span
	Sources []Span
}

// Synthesized reports whether the fragment has no source provenance.
func (s Segment) Synthesized() bool { return len(s.Sources) == 0 }

// Mapping is one layer of a compile chain: a sorted, non-overlapping list
// of segments in output coordinates. Positions not covered by any segment
// are unmapped (treated as synthesized on composition).
type Mapping struct {
	segs []Segment
}

// New validates segs and returns a Mapping. Segments do not need to be
// sorted; they are sorted by output start. Segments must not overlap;
// zero-width segments may coincide with boundaries or with each other.
func New(segs []Segment) (*Mapping, error) {
	cp := make([]Segment, len(segs))
	for i, s := range segs {
		if s.Out.Start < 0 || s.Out.End < s.Out.Start {
			return nil, fmt.Errorf("sourcemap: invalid output span %v", s.Out)
		}
		total := 0
		for _, src := range s.Sources {
			if src.Start < 0 || src.End < src.Start {
				return nil, fmt.Errorf("sourcemap: invalid source span %v", src)
			}
			total += src.Len()
		}
		if len(s.Sources) > 0 && total != s.Out.Len() {
			return nil, fmt.Errorf("sourcemap: segment %v sources total %d, want %d",
				s.Out, total, s.Out.Len())
		}
		srcs := make([]Span, len(s.Sources))
		copy(srcs, s.Sources)
		cp[i] = Segment{Out: s.Out, Sources: srcs}
	}
	sortSegments(cp)
	for i := 1; i < len(cp); i++ {
		if cp[i].Out.Start < cp[i-1].Out.End {
			return nil, fmt.Errorf("sourcemap: overlapping segments %v and %v",
				cp[i-1].Out, cp[i].Out)
		}
	}
	return &Mapping{segs: cp}, nil
}

// Must is New that panics on error; handy for literals in tests and tools.
func Must(segs []Segment) *Mapping {
	m, err := New(segs)
	if err != nil {
		panic(err)
	}
	return m
}

// Segments returns a copy of the mapping's segments.
func (m *Mapping) Segments() []Segment {
	out := make([]Segment, len(m.segs))
	for i, s := range m.segs {
		srcs := make([]Span, len(s.Sources))
		copy(srcs, s.Sources)
		out[i] = Segment{Out: s.Out, Sources: srcs}
	}
	return out
}

// Query returns the segments owning output position pos under the given
// bias. Zero-width segments located exactly at pos are always included,
// regardless of bias. The result is empty for unmapped positions.
func (m *Mapping) Query(pos int, bias Bias) []Segment {
	var out []Segment
	for _, s := range m.segs {
		if s.Out.Empty() {
			if pos == s.Out.Start {
				out = append(out, s)
			}
			continue
		}
		if bias == Right {
			if pos >= s.Out.Start && pos < s.Out.End {
				out = append(out, s)
			}
		} else {
			if pos > s.Out.Start && pos <= s.Out.End {
				out = append(out, s)
			}
		}
	}
	return out
}

// SourcesAt flattens the sources of every segment owning pos.
func (m *Mapping) SourcesAt(pos int, bias Bias) []Span {
	var out []Span
	for _, s := range m.Query(pos, bias) {
		out = append(out, s.Sources...)
	}
	return out
}

// SourcePointsAt maps a single output position precisely to zero-width
// source spans: each owning copied segment contributes the exact source
// point for pos, and each zero-width segment located at pos contributes
// its (zero-width) sources. Synthesized owners contribute nothing.
func (m *Mapping) SourcePointsAt(pos int, bias Bias) []Span {
	var out []Span
	for _, s := range m.Query(pos, bias) {
		if s.Out.Empty() {
			out = append(out, s.Sources...)
		} else if !s.Synthesized() {
			out = append(out, s.mapPointWithin(pos))
		}
	}
	return out
}

// mapOffset returns the source subspans corresponding to the output
// sub-interval [lo, hi) of s.Out.
func (s Segment) mapOffset(lo, hi int) []Span {
	var out []Span
	d0, d1 := lo-s.Out.Start, hi-s.Out.Start
	rel := 0
	for _, src := range s.Sources {
		r0, r1 := rel, rel+src.Len()
		if r1 > d0 && r0 < d1 {
			a := maxInt(d0, r0) - r0
			b := minInt(d1, r1) - r0
			out = append(out, Span{src.Start + a, src.Start + b})
		}
		rel = r1
	}
	return out
}

// mapPointWithin maps a single output position to a zero-width source span.
func (s Segment) mapPointWithin(p int) Span {
	d := p - s.Out.Start
	rel := 0
	for _, src := range s.Sources {
		if d >= rel && d < rel+src.Len() {
			return Span{src.Start + (d - rel), src.Start + (d - rel)}
		}
		rel += src.Len()
	}
	// p sits on the segment's end boundary: map to the end of the last source.
	last := s.Sources[len(s.Sources)-1]
	return Span{last.End, last.End}
}

// piece is one contiguous run of the composed mapping, measured in units of
// the inner mapping's coordinates.
type piece struct {
	bLen    int
	sources []Span // nil => synthesized
}

// mapThrough maps the interval src of this mapping's output coordinates
// back to source spans, splitting at segment boundaries and gaps.
func (m *Mapping) mapThrough(src Span) []piece {
	if src.Empty() {
		return []piece{m.mapPoint(src.Start)}
	}
	var pieces []piece
	cursor := src.Start
	emitGap := func(end int) {
		if end > cursor {
			pieces = append(pieces, piece{bLen: end - cursor})
			cursor = end
		}
	}
	for _, s := range m.segs {
		if s.Out.Empty() || s.Out.End <= src.Start || s.Out.Start >= src.End {
			continue
		}
		lo := maxInt(cursor, s.Out.Start)
		hi := minInt(src.End, s.Out.End)
		if lo >= hi {
			continue
		}
		emitGap(lo)
		pieces = append(pieces, piece{bLen: hi - lo, sources: s.mapOffset(lo, hi)})
		cursor = hi
	}
	emitGap(src.End)
	return pieces
}

// mapPoint maps a zero-width insertion point back to zero-width source
// spans, preferring the segment starting at p (Right bias) and falling back
// to the segment ending at p (Left bias). Zero-width segments located at p
// always contribute their sources.
func (m *Mapping) mapPoint(p int) piece {
	owners := m.Query(p, Right)
	if len(owners) == 0 {
		owners = m.Query(p, Left)
	}
	var sources []Span
	for _, o := range owners {
		if o.Out.Empty() {
			sources = append(sources, o.Sources...)
			continue
		}
		if o.Synthesized() {
			continue
		}
		sources = append(sources, o.mapPointWithin(p))
	}
	return piece{bLen: 0, sources: sources}
}

// Compose returns the composition of outer (mapping final output to
// intermediate coordinates) with inner (mapping intermediate coordinates to
// original source). The result maps outer's output coordinates directly to
// inner's source coordinates.
//
// Copied fragments stay copied, synthesized fragments stay synthesized, and
// fragments drawn from several inner segments keep every source span: they
// are never merged into a fake contiguous original interval. The only merge
// performed is of adjacent pieces that provably came from one contiguous
// original interval (same single source, contiguous in both coordinates).
func Compose(outer, inner *Mapping) *Mapping {
	var res []Segment
	for _, o := range outer.segs {
		if o.Synthesized() {
			res = appendSeg(res, Segment{Out: o.Out})
			continue
		}
		cursor := o.Out.Start
		for _, src := range o.Sources {
			pieces := inner.mapThrough(src)
			combined := true
			for _, p := range pieces {
				if p.sources == nil {
					combined = false
				}
			}
			if combined {
				// The whole outer source span maps to copied fragments:
				// keep every source span on one multi-source segment.
				var all []Span
				n := 0
				for _, p := range pieces {
					all = append(all, p.sources...)
					n += p.bLen
				}
				res = appendSeg(res, Segment{Out: Span{cursor, cursor + n}, Sources: all})
				cursor += n
			} else {
				// Mixed copied/synthesized run: emit pieces separately so
				// synthesized fragments stay sourceless.
				for _, p := range pieces {
					res = appendSeg(res, Segment{Out: Span{cursor, cursor + p.bLen}, Sources: p.sources})
					cursor += p.bLen
				}
			}
		}
	}
	return &Mapping{segs: res}
}

// appendSeg appends s, merging it into the previous segment only when both
// are non-empty copied fragments of the same contiguous original interval.
// Synthesized and multi-source fragments are never merged.
func appendSeg(res []Segment, s Segment) []Segment {
	if len(res) > 0 {
		p := &res[len(res)-1]
		if !p.Out.Empty() && !s.Out.Empty() &&
			p.Out.End == s.Out.Start &&
			len(p.Sources) == 1 && len(s.Sources) == 1 &&
			p.Sources[0].End == s.Sources[0].Start {
			p.Out.End = s.Out.End
			p.Sources[0].End = s.Sources[0].End
			return res
		}
	}
	return append(res, s)
}

func sortSegments(segs []Segment) {
	// insertion sort; mappings are typically small and nearly sorted
	for i := 1; i < len(segs); i++ {
		for j := i; j > 0; j-- {
			a, b := segs[j-1], segs[j]
			if a.Out.Start < b.Out.Start ||
				(a.Out.Start == b.Out.Start && a.Out.End <= b.Out.End) {
				break
			}
			segs[j-1], segs[j] = b, a
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
