// Package sourcemap composes layered interval mappings produced by
// multi-step compilation pipelines.
//
// Each layer maps half-open output intervals [Start, End) to zero, one,
// or several half-open input intervals. Copied segments are verbatim 1:1
// copies of exactly one source interval; Synthesized segments are
// generated and may derive from any number of source intervals without a
// positional correspondence. A zero-width segment (Start == End) models
// an insertion point; queries at such points are resolved with a
// left/right Bias.
//
// Composition never collapses a multi-source synthesized segment into a
// fake continuous original interval: a segment only remains Copied when
// its sources map to a single, fully contiguous copied run in the next
// layer.
package sourcemap

import "fmt"

// Kind describes how an output segment relates to its source intervals.
type Kind int

const (
	// Copied segments are verbatim 1:1 copies of exactly one source
	// interval of identical length.
	Copied Kind = iota
	// Synthesized segments are generated; they may derive from zero,
	// one, or several source intervals without positional correspondence.
	Synthesized
)

func (k Kind) String() string {
	switch k {
	case Copied:
		return "Copied"
	case Synthesized:
		return "Synthesized"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// SourceRef is a half-open interval [Start, End) in a layer's input.
type SourceRef struct {
	Start, End int
}

// Len returns the length of the interval; zero for an insertion point.
func (r SourceRef) Len() int { return r.End - r.Start }

func (r SourceRef) String() string { return fmt.Sprintf("[%d,%d)", r.Start, r.End) }

// Segment maps the half-open output interval [Start, End) to its sources.
// A zero-width segment (Start == End) models an insertion point.
type Segment struct {
	Start, End int
	Kind       Kind
	Sources    []SourceRef
}

// Len returns the output length covered by the segment.
func (s Segment) Len() int { return s.End - s.Start }

// ZeroWidth reports whether the segment is an insertion point.
func (s Segment) ZeroWidth() bool { return s.Start == s.End }

func (s Segment) String() string {
	return fmt.Sprintf("%s[%d,%d)->%v", s.Kind, s.Start, s.End, s.Sources)
}

// Layer is one step of a compilation chain: an ordered, non-overlapping
// list of segments mapping this layer's output to its input. Gaps between
// segments are unmapped (generated with no source information).
type Layer struct {
	segs []Segment
}

// NewLayer validates and builds a Layer. Segments must be sorted by Start
// and non-overlapping; Copied segments must have exactly one source of
// matching length.
func NewLayer(segs []Segment) (*Layer, error) {
	cp := make([]Segment, len(segs))
	for i, s := range segs {
		if s.Start < 0 || s.Start > s.End {
			return nil, fmt.Errorf("sourcemap: segment %d has invalid interval [%d,%d)", i, s.Start, s.End)
		}
		for _, r := range s.Sources {
			if r.Start < 0 || r.Start > r.End {
				return nil, fmt.Errorf("sourcemap: segment %d has invalid source %v", i, r)
			}
		}
		switch s.Kind {
		case Copied:
			if len(s.Sources) != 1 {
				return nil, fmt.Errorf("sourcemap: copied segment %d must have exactly one source, got %d", i, len(s.Sources))
			}
			if s.Sources[0].Len() != s.Len() {
				return nil, fmt.Errorf("sourcemap: copied segment %d length %d != source length %d", i, s.Len(), s.Sources[0].Len())
			}
		case Synthesized:
		default:
			return nil, fmt.Errorf("sourcemap: segment %d has unknown kind %d", i, int(s.Kind))
		}
		if i > 0 && s.Start < segs[i-1].End {
			return nil, fmt.Errorf("sourcemap: segments %d and %d overlap", i-1, i)
		}
		srcs := make([]SourceRef, len(s.Sources))
		copy(srcs, s.Sources)
		s.Sources = srcs
		cp[i] = s
	}
	return &Layer{segs: cp}, nil
}

// Segments returns a copy of the layer's segments.
func (l *Layer) Segments() []Segment {
	out := make([]Segment, len(l.segs))
	copy(out, l.segs)
	return out
}

// Bias resolves queries at segment boundaries and insertion points.
type Bias int

const (
	// BiasLeft associates a boundary position with the segment ending
	// there, then with insertion points, then with the segment starting
	// there.
	BiasLeft Bias = iota
	// BiasRight associates a boundary position with the segment starting
	// there, then with insertion points, then with the segment ending
	// there.
	BiasRight
)

// Lookup returns the segment associated with output position p, or nil if
// p falls in an unmapped gap. A position strictly inside a segment always
// resolves to that segment; bias only breaks ties at boundaries and
// insertion points.
func (l *Layer) Lookup(p int, bias Bias) *Segment {
	var left, right, zero *Segment
	for i := range l.segs {
		s := &l.segs[i]
		if s.End < p {
			continue
		}
		if s.Start > p {
			break
		}
		switch {
		case s.Start < p && p < s.End:
			return s // strict interior always wins
		case s.ZeroWidth():
			if zero == nil {
				zero = s
			}
		case p == s.End:
			left = s
		default: // p == s.Start
			right = s
		}
	}
	if bias == BiasLeft {
		if left != nil {
			return left
		}
		if zero != nil {
			return zero
		}
		return right
	}
	if right != nil {
		return right
	}
	if zero != nil {
		return zero
	}
	return left
}

// InsertionsAt returns all zero-width insertion segments at position p.
func (l *Layer) InsertionsAt(p int) []Segment {
	var out []Segment
	for _, s := range l.segs {
		if s.Start > p {
			break
		}
		if s.ZeroWidth() && s.Start == p {
			out = append(out, s)
		}
	}
	return out
}

// Position is the result of mapping a single output position through a
// layer. Pos is exact only when Kind is Copied; Sources always lists every
// contributing source interval.
type Position struct {
	Kind    Kind
	Pos     int
	Sources []SourceRef
}

// Mapped reports whether the position has any source information.
func (p Position) Mapped() bool { return len(p.Sources) > 0 }

// MapPosition maps output position p through the layer.
func (l *Layer) MapPosition(p int, bias Bias) Position {
	s := l.Lookup(p, bias)
	if s == nil {
		return Position{Kind: Synthesized}
	}
	if s.Kind == Copied {
		sp := s.Sources[0].Start + (p - s.Start)
		end := sp + 1
		if s.ZeroWidth() {
			end = sp
		}
		return Position{Kind: Copied, Pos: sp, Sources: []SourceRef{{Start: sp, End: end}}}
	}
	srcs := make([]SourceRef, len(s.Sources))
	copy(srcs, s.Sources)
	return Position{Kind: Synthesized, Sources: srcs}
}

// mappedRef is a source interval plus whether it came from a Copied
// segment (and therefore preserves positional correspondence).
type mappedRef struct {
	ref    SourceRef
	copied bool
}

func segRefs(s *Segment, offLo, offHi int) []mappedRef {
	if s.Kind == Copied {
		src := s.Sources[0]
		return []mappedRef{{ref: SourceRef{Start: src.Start + offLo, End: src.Start + offHi}, copied: true}}
	}
	out := make([]mappedRef, 0, len(s.Sources))
	for _, r := range s.Sources {
		out = append(out, mappedRef{ref: r})
	}
	return out
}

// mapInterval maps the half-open input interval [a, b) back to source
// intervals. Insertion points strictly inside (a, b) contribute their
// sources; insertions exactly at a or b do not.
func (l *Layer) mapInterval(a, b int) []mappedRef {
	if a == b {
		return l.refsAtPoint(a)
	}
	var out []mappedRef
	for i := range l.segs {
		s := &l.segs[i]
		if s.End <= a {
			continue
		}
		if s.Start >= b {
			break
		}
		if s.ZeroWidth() {
			out = append(out, segRefs(s, 0, 0)...)
			continue
		}
		lo := max(a, s.Start)
		hi := min(b, s.End)
		out = append(out, segRefs(s, lo-s.Start, hi-s.Start)...)
	}
	return mergeRefs(out)
}

// refsAtPoint collects the sources of every segment touching point p:
// the segment ending at p, insertion points at p, and the segment
// starting at p. Copied segments contribute the exact source point.
func (l *Layer) refsAtPoint(p int) []mappedRef {
	var out []mappedRef
	for i := range l.segs {
		s := &l.segs[i]
		if s.End < p {
			continue
		}
		if s.Start > p {
			break
		}
		if s.Kind == Copied {
			sp := s.Sources[0].Start + (p - s.Start)
			out = append(out, mappedRef{ref: SourceRef{Start: sp, End: sp}, copied: true})
		} else {
			for _, r := range s.Sources {
				out = append(out, mappedRef{ref: r})
			}
		}
	}
	return mergeRefs(out)
}

// mergeRefs merges adjacent refs that are contiguous and both copied
// (a genuinely continuous run), and drops exact duplicates. Synthesized
// refs are never merged into neighbours: multi-source provenance must not
// be faked into a continuous interval.
func mergeRefs(refs []mappedRef) []mappedRef {
	out := make([]mappedRef, 0, len(refs))
	for _, r := range refs {
		if n := len(out); n > 0 {
			prev := &out[n-1]
			if prev.copied && r.copied && prev.ref.End == r.ref.Start {
				prev.ref.End = r.ref.End
				continue
			}
			if *prev == r {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// Compose returns the composition of out (mapping final output to an
// intermediate representation) with in (mapping that intermediate
// representation to its own input), yielding a layer from out's output
// directly to in's input.
//
// Copied out-segments are split at every segment boundary of in so each
// composed piece keeps maximal precision: pieces over copied in-segments
// stay Copied, pieces over synthesized in-segments or unmapped gaps
// become Synthesized, and insertion points inside the source interval are
// re-emitted as zero-width segments at the corresponding output position.
// Adjacent copied pieces merge only when their sources are genuinely
// contiguous; a multi-source synthesized piece is never faked into a
// continuous original interval.
func Compose(out, in *Layer) *Layer {
	var segs []Segment
	emit := func(seg Segment) { segs = appendMerged(segs, seg) }
	for _, s := range out.segs {
		switch {
		case s.ZeroWidth():
			refs := mergeRefs(mapSourcesThrough(in, s.Sources))
			seg := Segment{Start: s.Start, End: s.End, Kind: Synthesized, Sources: refsOnly(refs)}
			if s.Kind == Copied && len(refs) == 1 && refs[0].copied {
				seg.Kind = Copied
			}
			emit(seg)
		case s.Kind == Copied:
			in.composeCopied(s, emit)
		default:
			refs := mergeRefs(mapSourcesThrough(in, s.Sources))
			emit(Segment{Start: s.Start, End: s.End, Kind: Synthesized, Sources: refsOnly(refs)})
		}
	}
	return &Layer{segs: segs}
}

// composeCopied emits the composition of one non-zero-width copied
// out-segment, splitting it at every segment boundary of l (the next
// layer towards the source).
func (l *Layer) composeCopied(s Segment, emit func(Segment)) {
	src := s.Sources[0]
	a, b := src.Start, src.End
	at := func(t int) int { return s.Start + (t - a) } // output position of input position t
	cursor := a
	for i := range l.segs {
		g := &l.segs[i]
		if g.End <= a {
			continue
		}
		if g.Start >= b {
			break
		}
		if cursor < g.Start { // unmapped gap before g
			hi := min(g.Start, b)
			emit(Segment{Start: at(cursor), End: at(hi), Kind: Synthesized})
			cursor = hi
		}
		if g.ZeroWidth() { // insertion point strictly inside (a, b)
			emit(Segment{Start: at(g.Start), End: at(g.Start), Kind: g.Kind, Sources: cloneRefs(g.Sources)})
			continue
		}
		lo := max(a, g.Start)
		hi := min(b, g.End)
		if g.Kind == Copied {
			gs := g.Sources[0].Start + (lo - g.Start)
			emit(Segment{Start: at(lo), End: at(hi), Kind: Copied,
				Sources: []SourceRef{{Start: gs, End: gs + hi - lo}}})
		} else {
			emit(Segment{Start: at(lo), End: at(hi), Kind: Synthesized, Sources: cloneRefs(g.Sources)})
		}
		cursor = hi
	}
	if cursor < b { // trailing unmapped gap
		emit(Segment{Start: at(cursor), End: at(b), Kind: Synthesized})
	}
}

// mapSourcesThrough maps every source interval through layer l.
func mapSourcesThrough(l *Layer, srcs []SourceRef) []mappedRef {
	var out []mappedRef
	for _, src := range srcs {
		if src.Start == src.End {
			out = append(out, l.refsAtPoint(src.Start)...)
		} else {
			out = append(out, l.mapInterval(src.Start, src.End)...)
		}
	}
	return out
}

// appendMerged appends seg, merging it into the previous segment when
// both are copied with genuinely contiguous sources, or both are
// synthesized with identical provenance.
func appendMerged(segs []Segment, seg Segment) []Segment {
	if n := len(segs); n > 0 {
		last := &segs[n-1]
		if last.End == seg.Start && last.ZeroWidth() == seg.ZeroWidth() {
			if last.Kind == Copied && seg.Kind == Copied &&
				last.Sources[0].End == seg.Sources[0].Start {
				last.End = seg.End
				last.Sources[0].End = seg.Sources[0].End
				return segs
			}
			if last.Kind == Synthesized && seg.Kind == Synthesized &&
				sourcesEqual(last.Sources, seg.Sources) {
				last.End = seg.End
				return segs
			}
		}
	}
	return append(segs, seg)
}

func refsOnly(refs []mappedRef) []SourceRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]SourceRef, len(refs))
	for i, r := range refs {
		out[i] = r.ref
	}
	return out
}

func cloneRefs(srcs []SourceRef) []SourceRef {
	if len(srcs) == 0 {
		return nil
	}
	out := make([]SourceRef, len(srcs))
	copy(out, srcs)
	return out
}

func sourcesEqual(a, b []SourceRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ComposeChain folds Compose over a compilation chain. layers[0] maps the
// final output, layers[len-1] maps to the original source.
func ComposeChain(layers ...*Layer) *Layer {
	if len(layers) == 0 {
		return &Layer{}
	}
	acc := layers[0]
	for _, l := range layers[1:] {
		acc = Compose(acc, l)
	}
	return acc
}

// CharSources returns, for every output position in [0, length), the set
// of source positions contributing to the character there. It is intended
// for verifying compositions character by character, including interleaved
// mappings: a copied character contributes exactly one source position,
// a synthesized character contributes every position of every source
// interval, and unmapped positions yield an empty set.
func (l *Layer) CharSources(length int) []map[int]struct{} {
	sets := make([]map[int]struct{}, length)
	for i := range sets {
		sets[i] = make(map[int]struct{})
	}
	for _, s := range l.segs {
		for p := s.Start; p < s.End && p < length; p++ {
			if s.Kind == Copied {
				sets[p][s.Sources[0].Start+(p-s.Start)] = struct{}{}
				continue
			}
			for _, r := range s.Sources {
				for q := r.Start; q < r.End; q++ {
					sets[p][q] = struct{}{}
				}
			}
		}
	}
	return sets
}
