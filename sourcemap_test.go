package sourcemap

import (
	"reflect"
	"testing"
)

func mustLayer(t *testing.T, segs ...Segment) *Layer {
	t.Helper()
	l, err := NewLayer(segs)
	if err != nil {
		t.Fatalf("NewLayer: %v", err)
	}
	return l
}

func copied(start, end, srcStart int) Segment {
	return Segment{Start: start, End: end, Kind: Copied,
		Sources: []SourceRef{{Start: srcStart, End: srcStart + end - start}}}
}

func synth(start, end int, srcs ...SourceRef) Segment {
	return Segment{Start: start, End: end, Kind: Synthesized, Sources: srcs}
}

// assertCharSources verifies the per-character source sets of a layer.
func assertCharSources(t *testing.T, l *Layer, want ...[]int) {
	t.Helper()
	got := l.CharSources(len(want))
	for i, w := range want {
		wantSet := make(map[int]struct{}, len(w))
		for _, p := range w {
			wantSet[p] = struct{}{}
		}
		if !reflect.DeepEqual(got[i], wantSet) {
			t.Errorf("char %d: source set = %v, want %v", i, got[i], wantSet)
		}
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		segs []Segment
	}{
		{"reversed interval", []Segment{{Start: 5, End: 3, Kind: Synthesized}}},
		{"copied needs one source", []Segment{{Start: 0, End: 3, Kind: Copied,
			Sources: []SourceRef{{0, 1}, {2, 3}}}}},
		{"copied length mismatch", []Segment{{Start: 0, End: 3, Kind: Copied,
			Sources: []SourceRef{{0, 2}}}}},
		{"overlap", []Segment{copied(0, 5, 0), copied(4, 8, 5)}},
		{"unsorted", []Segment{copied(5, 8, 0), copied(0, 3, 10)}},
		{"bad source", []Segment{synth(0, 1, SourceRef{4, 2})}},
	}
	for _, c := range cases {
		if _, err := NewLayer(c.segs); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
	// Zero-width insertion points are legal, also stacked at one position.
	if _, err := NewLayer([]Segment{
		copied(0, 5, 10),
		synth(5, 5, SourceRef{100, 102}),
		synth(5, 5),
		copied(5, 9, 15),
	}); err != nil {
		t.Errorf("zero-width insertions rejected: %v", err)
	}
}

func TestLookupBiasAndInsertions(t *testing.T) {
	l := mustLayer(t,
		copied(0, 5, 100),                  // [0,5)  <- [100,105)
		synth(5, 5, SourceRef{200, 202}),   // insertion at 5
		copied(5, 10, 105),                 // [5,10) <- [105,110)
		synth(20, 20, SourceRef{300, 301}), // lone insertion at 20
	)

	if s := l.Lookup(3, BiasLeft); s == nil || s.Start != 0 {
		t.Fatalf("interior lookup = %v", s)
	}
	// Boundary 5: left bias -> segment ending at 5; right bias -> starting at 5.
	if s := l.Lookup(5, BiasLeft); s == nil || s.Kind != Copied || s.Sources[0].Start != 100 {
		t.Fatalf("left bias at 5 = %v", s)
	}
	if s := l.Lookup(5, BiasRight); s == nil || s.Kind != Copied || s.Sources[0].Start != 105 {
		t.Fatalf("right bias at 5 = %v", s)
	}
	// The insertion itself is reachable via InsertionsAt.
	ins := l.InsertionsAt(5)
	if len(ins) != 1 || !reflect.DeepEqual(ins[0].Sources, []SourceRef{{200, 202}}) {
		t.Fatalf("insertions at 5 = %v", ins)
	}
	// Lone insertion: both biases fall back to it.
	if s := l.Lookup(20, BiasLeft); s == nil || !s.ZeroWidth() {
		t.Fatalf("left bias at lone insertion = %v", s)
	}
	if s := l.Lookup(20, BiasRight); s == nil || !s.ZeroWidth() {
		t.Fatalf("right bias at lone insertion = %v", s)
	}
	// Boundary 10: only the left segment abuts, both biases fall back to it.
	if s := l.Lookup(10, BiasLeft); s == nil || s.End != 10 {
		t.Fatalf("left bias at 10 = %v", s)
	}
	if s := l.Lookup(10, BiasRight); s == nil || s.End != 10 {
		t.Fatalf("right bias fallback at 10 = %v", s)
	}
	// Interior gap and out-of-range positions are unmapped.
	for _, p := range []int{11, 15, 19, 21, 100} {
		if s := l.Lookup(p, BiasLeft); s != nil {
			t.Fatalf("gap lookup at %d = %v", p, s)
		}
	}
}

func TestMapPosition(t *testing.T) {
	l := mustLayer(t,
		copied(0, 5, 100),
		synth(5, 8, SourceRef{200, 202}, SourceRef{300, 301}),
	)
	pos := l.MapPosition(2, BiasLeft)
	if pos.Kind != Copied || pos.Pos != 102 || !pos.Mapped() {
		t.Fatalf("copied position = %+v", pos)
	}
	pos = l.MapPosition(6, BiasLeft)
	if pos.Kind != Synthesized || !reflect.DeepEqual(pos.Sources, []SourceRef{{200, 202}, {300, 301}}) {
		t.Fatalf("synthesized position = %+v", pos)
	}
	if pos = l.MapPosition(50, BiasLeft); pos.Mapped() {
		t.Fatalf("unmapped position = %+v", pos)
	}
}

func TestCopiedCompositionChain(t *testing.T) {
	// out -> mid -> orig -> orig0, three copied hops with offsets.
	l1 := mustLayer(t, copied(0, 2, 3))     // out[0,2)  <- mid[3,5)
	l2 := mustLayer(t, copied(0, 10, 10))   // mid[0,10) <- orig[10,20)
	l3 := mustLayer(t, copied(10, 20, 100)) // orig[10,20) <- orig0[100,110)
	composed := ComposeChain(l1, l2, l3)

	segs := composed.Segments()
	if len(segs) != 1 || segs[0].Kind != Copied {
		t.Fatalf("composed segments = %v", segs)
	}
	if segs[0].Sources[0] != (SourceRef{103, 105}) {
		t.Fatalf("composed source = %v", segs[0].Sources[0])
	}
	if pos := composed.MapPosition(1, BiasLeft); pos.Kind != Copied || pos.Pos != 104 {
		t.Fatalf("composed position = %+v", pos)
	}
	assertCharSources(t, composed, []int{103}, []int{104})
}

// TestInterleavedComposition is the core "do not fake continuity" check:
// a single copied output interval whose source interleaves two original
// regions must compose to alternating per-character copied segments —
// never to one continuous original interval.
func TestInterleavedComposition(t *testing.T) {
	// mid interleaves orig "abc" ([0,3)) and orig "XYZ" ([10,13)):
	// mid = a X b Y c Z
	mid := mustLayer(t,
		copied(0, 1, 0),  // a <- orig[0]
		copied(1, 2, 10), // X <- orig[10]
		copied(2, 3, 1),  // b <- orig[1]
		copied(3, 4, 11), // Y <- orig[11]
		copied(4, 5, 2),  // c <- orig[2]
		copied(5, 6, 12), // Z <- orig[12]
	)
	out := mustLayer(t, copied(0, 6, 0)) // out[0,6) <- mid[0,6)

	composed := Compose(out, mid)
	segs := composed.Segments()
	if len(segs) != 6 {
		t.Fatalf("composed segments = %v", segs)
	}
	wantSrcs := []SourceRef{{0, 1}, {10, 11}, {1, 2}, {11, 12}, {2, 3}, {12, 13}}
	for i, seg := range segs {
		if seg.Kind != Copied || seg.Start != i || seg.End != i+1 {
			t.Fatalf("segment %d faked or misplaced: %v", i, seg)
		}
		if !reflect.DeepEqual(seg.Sources, wantSrcs[i:i+1]) {
			t.Fatalf("segment %d sources = %v, want %v", i, seg.Sources, wantSrcs[i:i+1])
		}
	}
	// Per-character source sets: exact singletons, alternating regions.
	assertCharSources(t, composed,
		[]int{0}, []int{10}, []int{1}, []int{11}, []int{2}, []int{12})
	// Positional queries are exact per character.
	wantPos := []int{0, 10, 1, 11, 2, 12}
	for i, w := range wantPos {
		if pos := composed.MapPosition(i, BiasRight); pos.Kind != Copied || pos.Pos != w {
			t.Fatalf("position %d = %+v, want copied %d", i, pos, w)
		}
	}
}

// TestCopiedOverSynthesizedNotFaked: a copied output interval over a
// multi-source synthesized region must degrade to a synthesized segment
// listing every contributing source — never a fake continuous copy.
func TestCopiedOverSynthesizedNotFaked(t *testing.T) {
	mid := mustLayer(t,
		synth(0, 4, SourceRef{10, 12}, SourceRef{20, 22}), // generated from two regions
	)
	out := mustLayer(t, copied(0, 4, 0)) // out[0,4) <- mid[0,4)
	composed := Compose(out, mid)
	segs := composed.Segments()
	if len(segs) != 1 {
		t.Fatalf("composed = %v", segs)
	}
	if segs[0].Kind != Synthesized {
		t.Fatalf("multi-source segment faked as %v: %v", segs[0].Kind, segs[0])
	}
	if !reflect.DeepEqual(segs[0].Sources, []SourceRef{{10, 12}, {20, 22}}) {
		t.Fatalf("sources = %v", segs[0].Sources)
	}
	assertCharSources(t, composed,
		[]int{10, 11, 20, 21}, []int{10, 11, 20, 21}, []int{10, 11, 20, 21}, []int{10, 11, 20, 21})
	if pos := composed.MapPosition(1, BiasRight); pos.Kind == Copied {
		t.Fatalf("synthesized position faked as copied: %+v", pos)
	}
}

// TestContiguousCopiedMerge: adjacent copied segments that are contiguous
// in both layers legitimately compose to one copied segment.
func TestContiguousCopiedMerge(t *testing.T) {
	mid := mustLayer(t,
		copied(0, 3, 10), // mid[0,3) <- orig[10,13)
		copied(3, 6, 13), // mid[3,6) <- orig[13,16)
	)
	out := mustLayer(t, copied(0, 6, 0))
	composed := Compose(out, mid)
	segs := composed.Segments()
	if len(segs) != 1 || segs[0].Kind != Copied || segs[0].Sources[0] != (SourceRef{10, 16}) {
		t.Fatalf("contiguous compose = %v", segs)
	}
}

func TestZeroWidthInsertionComposition(t *testing.T) {
	mid := mustLayer(t,
		copied(0, 10, 50),  // mid[0,10)  <- orig[50,60)
		copied(10, 13, 70), // mid[10,13) <- orig[70,73)
	)
	out := mustLayer(t,
		copied(0, 5, 0),                // out[0,5) <- mid[0,5)
		copied(5, 5, 8),                // insertion at 5 <- mid point 8
		synth(6, 6, SourceRef{12, 13}), // synthesized insertion at 6 <- mid[12,13)
		copied(6, 9, 10),               // out[6,9) <- mid[10,13)
	)
	composed := Compose(out, mid)
	segs := composed.Segments()
	if len(segs) != 4 {
		t.Fatalf("composed segments = %v", segs)
	}
	if segs[0].Kind != Copied || segs[0].Sources[0] != (SourceRef{50, 55}) {
		t.Fatalf("seg0 = %v", segs[0])
	}
	// Zero-width copied insertion lands on the exact original point 58.
	if segs[1].Kind != Copied || !segs[1].ZeroWidth() || segs[1].Sources[0] != (SourceRef{58, 58}) {
		t.Fatalf("insertion seg = %v", segs[1])
	}
	// Synthesized insertion maps its source interval through mid.
	if segs[2].Kind != Synthesized || !reflect.DeepEqual(segs[2].Sources, []SourceRef{{72, 73}}) {
		t.Fatalf("synth insertion seg = %v", segs[2])
	}
	if segs[3].Kind != Copied || segs[3].Sources[0] != (SourceRef{70, 73}) {
		t.Fatalf("seg3 = %v", segs[3])
	}
	// Insertions consume no output characters.
	assertCharSources(t, composed,
		[]int{50}, []int{51}, []int{52}, []int{53}, []int{54},
		[]int{}, // position 5: only zero-width segments live here
		[]int{70}, []int{71}, []int{72},
	)
	ins := composed.InsertionsAt(5)
	if len(ins) != 1 || ins[0].Kind != Copied || ins[0].Sources[0] != (SourceRef{58, 58}) {
		t.Fatalf("composed insertions at 5 = %v", ins)
	}
}

func TestSynthesizedSourcesComposeThrough(t *testing.T) {
	mid := mustLayer(t,
		copied(0, 3, 10), // mid[0,3) <- orig[10,13)
		copied(5, 8, 20), // mid[5,8) <- orig[20,23)
	)
	out := mustLayer(t,
		synth(0, 4, SourceRef{0, 2}, SourceRef{5, 6}), // generated from two mid intervals
	)
	composed := Compose(out, mid)
	segs := composed.Segments()
	if len(segs) != 1 || segs[0].Kind != Synthesized {
		t.Fatalf("composed = %v", segs)
	}
	if !reflect.DeepEqual(segs[0].Sources, []SourceRef{{10, 12}, {20, 21}}) {
		t.Fatalf("sources = %v", segs[0].Sources)
	}
	// Every generated character traces to the union of both sources.
	assertCharSources(t, composed,
		[]int{10, 11, 20}, []int{10, 11, 20}, []int{10, 11, 20}, []int{10, 11, 20})
}

func TestUnmappedAndGaps(t *testing.T) {
	// mid only maps [0,2); the rest of out's source interval is a gap.
	mid := mustLayer(t, copied(0, 2, 40))
	out := mustLayer(t,
		copied(0, 5, 0), // out[0,5) <- mid[0,5), partially unmapped
		synth(5, 7),     // generated, no sources at all
	)
	composed := Compose(out, mid)
	segs := composed.Segments()
	if len(segs) != 2 {
		t.Fatalf("composed = %v", segs)
	}
	// The mapped prefix stays an exact copied segment.
	if segs[0].Kind != Copied || segs[0].Start != 0 || segs[0].End != 2 ||
		!reflect.DeepEqual(segs[0].Sources, []SourceRef{{40, 42}}) {
		t.Fatalf("mapped prefix = %v", segs[0])
	}
	// The unmapped remainder and the source-less generated segment merge
	// into one synthesized segment with no sources.
	if segs[1].Kind != Synthesized || segs[1].Start != 2 || segs[1].End != 7 ||
		len(segs[1].Sources) != 0 {
		t.Fatalf("unmapped seg = %v", segs[1])
	}
	assertCharSources(t, composed,
		[]int{40}, []int{41}, []int{}, []int{}, []int{},
		[]int{}, []int{})
	if pos := composed.MapPosition(6, BiasLeft); pos.Mapped() {
		t.Fatalf("unmapped position = %+v", pos)
	}
}

// TestInsertionInsideMappedInterval: an insertion point strictly inside a
// copied source interval is re-emitted as a zero-width segment at the
// corresponding output position, splitting the enclosing copies.
func TestInsertionInsideMappedInterval(t *testing.T) {
	mid := mustLayer(t,
		copied(0, 4, 10),               // mid[0,4) <- orig[10,14)
		synth(4, 4, SourceRef{90, 92}), // insertion at mid point 4
		copied(4, 8, 20),               // mid[4,8) <- orig[20,24)
	)
	out := mustLayer(t, copied(0, 8, 0)) // out[0,8) <- mid[0,8)
	composed := Compose(out, mid)
	segs := composed.Segments()
	if len(segs) != 3 {
		t.Fatalf("composed = %v", segs)
	}
	if segs[0].Kind != Copied || !reflect.DeepEqual(segs[0].Sources, []SourceRef{{10, 14}}) {
		t.Fatalf("seg0 = %v", segs[0])
	}
	if !segs[1].ZeroWidth() || segs[1].Start != 4 || segs[1].Kind != Synthesized ||
		!reflect.DeepEqual(segs[1].Sources, []SourceRef{{90, 92}}) {
		t.Fatalf("insertion = %v", segs[1])
	}
	if segs[2].Kind != Copied || !reflect.DeepEqual(segs[2].Sources, []SourceRef{{20, 24}}) {
		t.Fatalf("seg2 = %v", segs[2])
	}
	assertCharSources(t, composed,
		[]int{10}, []int{11}, []int{12}, []int{13},
		[]int{20}, []int{21}, []int{22}, []int{23})
}
