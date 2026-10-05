package sourcemap

import (
	"reflect"
	"sort"
	"testing"
)

// traceSet maps a set of positions one layer back, returning the set of
// exact source points (per-character provenance).
func traceSet(m *Mapping, pts map[int]bool) map[int]bool {
	out := map[int]bool{}
	for p := range pts {
		for _, s := range m.SourcePointsAt(p, Right) {
			out[s.Start] = true
		}
	}
	return out
}

// groundTruth computes, for every character position of the last layer's
// output, the set of original source positions by chaining per-point
// queries through each layer independently (never touching any composed
// mapping).
func groundTruth(outLen int, layers ...*Mapping) []map[int]bool {
	sets := make([]map[int]bool, outLen)
	for pos := 0; pos < outLen; pos++ {
		pts := map[int]bool{pos: true}
		for _, l := range layers {
			pts = traceSet(l, pts)
		}
		sets[pos] = pts
	}
	return sets
}

// checkPerChar verifies the composed mapping against independently computed
// per-character source sets for every output position.
func checkPerChar(t *testing.T, composed *Mapping, want []map[int]bool) {
	t.Helper()
	if _, err := New(composed.Segments()); err != nil {
		t.Fatalf("composed mapping violates invariants: %v", err)
	}
	for pos, w := range want {
		got := map[int]bool{}
		for _, s := range composed.SourcePointsAt(pos, Right) {
			got[s.Start] = true
		}
		if !reflect.DeepEqual(got, w) {
			t.Fatalf("pos %d: composed source set %v, want %v",
				pos, sortedKeys(got), sortedKeys(w))
		}
	}
}

func sortedKeys(m map[int]bool) []int {
	var ks []int
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}

func outLen(m *Mapping) int {
	max := 0
	for _, s := range m.Segments() {
		if s.Out.End > max {
			max = s.Out.End
		}
	}
	return max
}

// TestComposeInterleaved builds two layers that interleave copied and
// synthesized fragments, composes them, and checks every output character's
// source set against the independently computed ground truth.
func TestComposeInterleaved(t *testing.T) {
	// Layer 1 (A -> B): B interleaves two copied runs of A with a
	// synthesized gap.
	//   B[0,5)   <- A[10,15)   copied
	//   B[5,10)                synthesized
	//   B[10,20) <- A[0,10)    copied
	inner := Must([]Segment{
		{Out: Span{0, 5}, Sources: []Span{{10, 15}}},
		{Out: Span{5, 10}},
		{Out: Span{10, 20}, Sources: []Span{{0, 10}}},
	})
	// Layer 2 (B -> C): C shuffles B, slicing across segment boundaries.
	//   C[0,4)   <- B[6,10)    (synthesized in layer 1)
	//   C[4,14)  <- B[10,20)
	//   C[14,19) <- B[0,5)
	//   C[19,22)               synthesized
	outer := Must([]Segment{
		{Out: Span{0, 4}, Sources: []Span{{6, 10}}},
		{Out: Span{4, 14}, Sources: []Span{{10, 20}}},
		{Out: Span{14, 19}, Sources: []Span{{0, 5}}},
		{Out: Span{19, 22}},
	})

	composed := Compose(outer, inner)
	checkPerChar(t, composed, groundTruth(outLen(outer), outer, inner))

	// C[0,4) <- B[6,10) is synthesized in layer 1.
	for pos := 0; pos < 4; pos++ {
		if srcs := composed.SourcePointsAt(pos, Right); len(srcs) != 0 {
			t.Fatalf("pos %d: expected synthesized, got %v", pos, srcs)
		}
	}
	// Exact per-character points at the shuffle boundaries.
	if got := composed.SourcePointsAt(4, Right); !reflect.DeepEqual(got, []Span{{0, 0}}) {
		t.Fatalf("pos 4: got %v", got)
	}
	if got := composed.SourcePointsAt(14, Right); !reflect.DeepEqual(got, []Span{{10, 10}}) {
		t.Fatalf("pos 14: got %v", got)
	}
}

// TestComposeMultiSourcePreserved ensures a fragment drawn from several
// inner segments keeps every source span instead of being collapsed into a
// fake contiguous original interval.
func TestComposeMultiSourcePreserved(t *testing.T) {
	// Layer 1 (A -> B): two copied runs of A that are NOT contiguous in A.
	inner := Must([]Segment{
		{Out: Span{0, 3}, Sources: []Span{{10, 13}}},
		{Out: Span{3, 6}, Sources: []Span{{0, 3}}},
	})
	// Layer 2 (B -> C): one copied run spanning both inner segments.
	outer := Must([]Segment{
		{Out: Span{0, 6}, Sources: []Span{{0, 6}}},
	})

	composed := Compose(outer, inner)
	checkPerChar(t, composed, groundTruth(outLen(outer), outer, inner))

	// A single segment covers C[0,6) and must carry both sources, never a
	// single fake span such as A[0,6) or A[10,16).
	segs := composed.Segments()
	if len(segs) != 1 || segs[0].Out != (Span{0, 6}) {
		t.Fatalf("expected one segment covering C[0,6), got %v", segs)
	}
	want := []Span{{10, 13}, {0, 3}}
	if !reflect.DeepEqual(segs[0].Sources, want) {
		t.Fatalf("multi-source segment sources %v, want %v", segs[0].Sources, want)
	}
	// Boundary characters map to exact points on each side.
	if got := composed.SourcePointsAt(2, Right); !reflect.DeepEqual(got, []Span{{12, 12}}) {
		t.Fatalf("pos 2: got %v", got)
	}
	if got := composed.SourcePointsAt(3, Right); !reflect.DeepEqual(got, []Span{{0, 0}}) {
		t.Fatalf("pos 3: got %v", got)
	}
}

// TestZeroWidthBoundaries checks insertion points and bias handling at
// empty-interval boundaries.
func TestZeroWidthBoundaries(t *testing.T) {
	// A -> B with an insertion point at B=3 (zero-width, sourced at A=2)
	// and a copied run on each side.
	inner := Must([]Segment{
		{Out: Span{0, 3}, Sources: []Span{{0, 3}}},
		{Out: Span{3, 3}, Sources: []Span{{2, 2}}},
		{Out: Span{3, 6}, Sources: []Span{{5, 8}}},
	})

	// Bias decides ownership of the boundary position 3; the zero-width
	// segment at 3 always contributes.
	if got := inner.SourcePointsAt(3, Right); !reflect.DeepEqual(got, []Span{{2, 2}, {5, 5}}) {
		t.Fatalf("Right bias at 3: got %v", got)
	}
	if got := inner.SourcePointsAt(3, Left); !reflect.DeepEqual(got, []Span{{3, 3}, {2, 2}}) {
		t.Fatalf("Left bias at 3: got %v", got)
	}

	// Compose with an outer layer that also inserts at C=3.
	outer := Must([]Segment{
		{Out: Span{0, 3}, Sources: []Span{{0, 3}}},
		{Out: Span{3, 3}, Sources: []Span{{3, 3}}},
		{Out: Span{3, 6}, Sources: []Span{{3, 6}}},
	})
	composed := Compose(outer, inner)
	checkPerChar(t, composed, groundTruth(outLen(outer), outer, inner))

	// The composed zero-width segment at C=3 traces through the inner
	// insertion point (A=2) and the copied run starting at B=3 (A=5).
	var zw *Segment
	segs := composed.Segments()
	for i := range segs {
		if segs[i].Out == (Span{3, 3}) {
			zw = &segs[i]
		}
	}
	if zw == nil {
		t.Fatal("expected zero-width segment at C=3")
	}
	if !reflect.DeepEqual(zw.Sources, []Span{{2, 2}, {5, 5}}) {
		t.Fatalf("zero-width sources %v, want [{2 2} {5 5}]", zw.Sources)
	}
}

// TestSynthesizedDistinct ensures synthesized fragments never acquire
// sources and never merge with copied neighbours.
func TestSynthesizedDistinct(t *testing.T) {
	inner := Must([]Segment{
		{Out: Span{0, 2}, Sources: []Span{{4, 6}}},
		// B[2,4) uncovered: gap => synthesized on composition.
		{Out: Span{4, 6}, Sources: []Span{{4, 6}}},
	})
	outer := Must([]Segment{
		{Out: Span{0, 6}, Sources: []Span{{0, 6}}},
	})
	composed := Compose(outer, inner)
	checkPerChar(t, composed, groundTruth(outLen(outer), outer, inner))

	segs := composed.Segments()
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments (copied/synthesized/copied), got %v", segs)
	}
	if segs[1].Out != (Span{2, 4}) || !segs[1].Synthesized() {
		t.Fatalf("middle segment = %+v, want synthesized C[2,4)", segs[1])
	}
	if segs[0].Synthesized() || segs[2].Synthesized() {
		t.Fatalf("copied segments lost sources: %v", segs)
	}
}

// TestValidation checks the constructor's invariant enforcement.
func TestValidation(t *testing.T) {
	if _, err := New([]Segment{
		{Out: Span{0, 5}, Sources: []Span{{0, 4}}},
	}); err == nil {
		t.Fatal("expected source-length mismatch error")
	}
	if _, err := New([]Segment{
		{Out: Span{0, 5}, Sources: []Span{{0, 5}}},
		{Out: Span{4, 6}, Sources: []Span{{0, 2}}},
	}); err == nil {
		t.Fatal("expected overlap error")
	}
	if _, err := New([]Segment{
		{Out: Span{3, 2}},
	}); err == nil {
		t.Fatal("expected invalid span error")
	}
	// Zero-width segments coinciding with boundaries are legal.
	if _, err := New([]Segment{
		{Out: Span{0, 3}, Sources: []Span{{0, 3}}},
		{Out: Span{3, 3}},
		{Out: Span{3, 5}, Sources: []Span{{3, 5}}},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestComposeChain composes three layers (both association orders) and
// verifies per-character source sets against the three-layer ground truth.
func TestComposeChain(t *testing.T) {
	l1 := Must([]Segment{ // A -> B
		{Out: Span{0, 4}, Sources: []Span{{8, 12}}},
		{Out: Span{4, 6}},
		{Out: Span{6, 10}, Sources: []Span{{0, 4}}},
	})
	l2 := Must([]Segment{ // B -> C
		{Out: Span{0, 5}, Sources: []Span{{5, 10}}},
		{Out: Span{5, 9}, Sources: []Span{{0, 4}}},
	})
	l3 := Must([]Segment{ // C -> D
		{Out: Span{0, 9}, Sources: []Span{{0, 9}}},
		{Out: Span{9, 9}, Sources: []Span{{4, 4}}},
	})
	sets := groundTruth(outLen(l3), l3, l2, l1)
	checkPerChar(t, Compose(l3, Compose(l2, l1)), sets)
	checkPerChar(t, Compose(Compose(l3, l2), l1), sets)
}
