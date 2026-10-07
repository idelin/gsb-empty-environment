package occlusion

import (
	"math/big"
	"math/rand"
	"testing"
)

func mustUpsert(t *testing.T, m *Map, o Occluder) {
	t.Helper()
	if err := m.Upsert(o); err != nil {
		t.Fatalf("Upsert(%+v): %v", o, err)
	}
}

func TestEmptyMap(t *testing.T) {
	m := New()
	segs := m.Segments()
	if len(segs) != 1 || !segs[0].Full() || segs[0].Winner != NoObject {
		t.Fatalf("empty map segments: %v", segs)
	}
	if got := m.WinnerAt(Rat(123, 7)); got != NoObject {
		t.Fatalf("empty map winner: %d", got)
	}
	if err := m.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestZeroSpanIsEmpty(t *testing.T) {
	m := New()
	mustUpsert(t, m, Occluder{ID: 1, Priority: 5, Start: Rat(45, 1), Span: Rat(0, 1)})
	segs := m.Segments()
	if len(segs) != 1 || !segs[0].Full() || segs[0].Winner != NoObject {
		t.Fatalf("zero-span occluder must be empty: %v", segs)
	}
	if got := m.WinnerAt(Rat(45, 1)); got != NoObject {
		t.Fatalf("zero-span occluder must not cover its start: %d", got)
	}
}

func TestFullCircleNotEmpty(t *testing.T) {
	m := New()
	// Start and end coincide (0 == 360 mod 360); must still be the full circle.
	mustUpsert(t, m, Occluder{ID: 7, Priority: 1, Start: Rat(0, 1), Span: Rat(360, 1)})
	segs := m.Segments()
	if len(segs) != 1 || !segs[0].Full() || segs[0].Winner != 7 {
		t.Fatalf("full-circle occluder mistaken for empty: %v", segs)
	}
	for _, a := range []int64{0, 1, 90, 180, 359} {
		if got := m.WinnerAt(Rat(a, 1)); got != 7 {
			t.Fatalf("WinnerAt(%d) = %d, want 7", a, got)
		}
	}
	// Full circle starting at a non-zero angle is still the full circle.
	m2 := New()
	mustUpsert(t, m2, Occluder{ID: 3, Priority: 1, Start: Rat(200, 1), Span: Rat(360, 1)})
	if segs := m2.Segments(); len(segs) != 1 || !segs[0].Full() || segs[0].Winner != 3 {
		t.Fatalf("offset full-circle: %v", segs)
	}
	if err := m2.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestLeftClosedRightOpen(t *testing.T) {
	m := New()
	mustUpsert(t, m, Occluder{ID: 1, Priority: 1, Start: Rat(0, 1), Span: Rat(180, 1)})
	mustUpsert(t, m, Occluder{ID: 2, Priority: 1, Start: Rat(180, 1), Span: Rat(180, 1)})
	if got := m.WinnerAt(Rat(0, 1)); got != 1 {
		t.Fatalf("left endpoint belongs to its arc: %d", got)
	}
	if got := m.WinnerAt(Rat(180, 1)); got != 2 {
		t.Fatalf("shared endpoint belongs to the right arc: %d", got)
	}
	if got := m.WinnerAt(Rat(359999, 1000)); got != 2 {
		t.Fatalf("just before 360: %d", got)
	}
	if got := m.WinnerAt(Rat(179999, 1000)); got != 1 {
		t.Fatalf("just before 180: %d", got)
	}
	if err := m.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestCrossZeroInterval(t *testing.T) {
	m := New()
	mustUpsert(t, m, Occluder{ID: 9, Priority: 2, Start: Rat(270, 1), Span: Rat(180, 1)})
	for _, tc := range []struct {
		angle *big.Rat
		want  int
	}{
		{Rat(270, 1), 9},
		{Rat(0, 1), 9},
		{Rat(89, 1), 9},
		{Rat(90, 1), NoObject},
		{Rat(180, 1), NoObject},
		{Rat(269, 1), NoObject},
		{Rat(359, 1), 9},
	} {
		if got := m.WinnerAt(tc.angle); got != tc.want {
			t.Fatalf("WinnerAt(%s) = %d, want %d", tc.angle, got, tc.want)
		}
	}
	segs := m.Segments()
	if len(segs) != 2 {
		t.Fatalf("want 2 segments, got %v", segs)
	}
	if err := m.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestPriorityAndTieBreak(t *testing.T) {
	m := New()
	mustUpsert(t, m, Occluder{ID: 5, Priority: 1, Start: Rat(0, 1), Span: Rat(200, 1)})
	mustUpsert(t, m, Occluder{ID: 6, Priority: 2, Start: Rat(100, 1), Span: Rat(200, 1)})
	mustUpsert(t, m, Occluder{ID: 2, Priority: 2, Start: Rat(150, 1), Span: Rat(100, 1)}) // same layer, smaller ID
	for _, tc := range []struct {
		angle *big.Rat
		want  int
	}{
		{Rat(50, 1), 5},  // only occluder 5
		{Rat(120, 1), 6}, // 6 outranks 5
		{Rat(160, 1), 2}, // tie between 2 and 6 -> smaller ID
		{Rat(260, 1), 6}, // only occluder 6 covers 260
		{Rat(320, 1), NoObject},
	} {
		if got := m.WinnerAt(tc.angle); got != tc.want {
			t.Fatalf("WinnerAt(%s) = %d, want %d", tc.angle, got, tc.want)
		}
	}
	if err := m.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveAndModify(t *testing.T) {
	m := New()
	mustUpsert(t, m, Occluder{ID: 1, Priority: 1, Start: Rat(0, 1), Span: Rat(90, 1)})
	mustUpsert(t, m, Occluder{ID: 2, Priority: 2, Start: Rat(45, 1), Span: Rat(90, 1)})
	if got := m.WinnerAt(Rat(60, 1)); got != 2 {
		t.Fatalf("before remove: %d", got)
	}
	if !m.Remove(2) {
		t.Fatal("Remove(2) should report existence")
	}
	if m.Remove(2) {
		t.Fatal("second Remove(2) should report absence")
	}
	if got := m.WinnerAt(Rat(60, 1)); got != 1 {
		t.Fatalf("after remove: %d", got)
	}
	// Modify: same ID, new geometry and priority.
	mustUpsert(t, m, Occluder{ID: 1, Priority: 1, Start: Rat(180, 1), Span: Rat(45, 1)})
	if got := m.WinnerAt(Rat(10, 1)); got != NoObject {
		t.Fatalf("old geometry must be gone: %d", got)
	}
	if got := m.WinnerAt(Rat(200, 1)); got != 1 {
		t.Fatalf("new geometry must apply: %d", got)
	}
	if m.Len() != 1 {
		t.Fatalf("Len = %d, want 1", m.Len())
	}
	if err := m.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestMinimalMergeAcrossZero(t *testing.T) {
	m := New()
	// A low-priority background fills the circle; a high-priority occluder
	// and a below-background occluder add breakpoints that must not split
	// the background into several arcs: the background winner remains one
	// single segment crossing the 0 meridian.
	mustUpsert(t, m, Occluder{ID: 1, Priority: 0, Start: Rat(0, 1), Span: Rat(360, 1)})
	mustUpsert(t, m, Occluder{ID: 5, Priority: 1, Start: Rat(90, 1), Span: Rat(90, 1)})
	mustUpsert(t, m, Occluder{ID: 6, Priority: -1, Start: Rat(45, 1), Span: Rat(15, 1)})
	segs := m.Segments()
	if len(segs) != 2 {
		t.Fatalf("want 2 segments, got %v", segs)
	}
	var occ, bg Segment
	for _, s := range segs {
		if s.Winner == 5 {
			occ = s
		} else {
			bg = s
		}
	}
	if occ.Start.Cmp(Rat(90, 1)) != 0 || occ.Span.Cmp(Rat(90, 1)) != 0 {
		t.Fatalf("occluder arc = %v, want [90, +90)", occ)
	}
	if bg.Winner != 1 || bg.Start.Cmp(Rat(180, 1)) != 0 || bg.Span.Cmp(Rat(270, 1)) != 0 {
		t.Fatalf("background arc = %v, want [180, +270) crossing 0", bg)
	}
	if err := m.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestRationalEndpoints(t *testing.T) {
	m := New()
	// Thirds: endpoints that binary floating point cannot represent exactly.
	mustUpsert(t, m, Occluder{ID: 1, Priority: 1, Start: Rat(1, 3), Span: Rat(1, 3)})
	mustUpsert(t, m, Occluder{ID: 2, Priority: 1, Start: Rat(2, 3), Span: Rat(1, 3)})
	if got := m.WinnerAt(Rat(2, 3)); got != 2 {
		t.Fatalf("exact rational endpoint: %d", got)
	}
	if got := m.WinnerAt(new(big.Rat).Sub(Rat(2, 3), Rat(1, 10))); got != 1 {
		t.Fatalf("just below 2/3: %d", got)
	}
	if err := m.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidSpan(t *testing.T) {
	m := New()
	if err := m.Upsert(Occluder{ID: 1, Start: Rat(0, 1), Span: Rat(361, 1)}); err == nil {
		t.Fatal("span > 360 must be rejected")
	}
	if err := m.Upsert(Occluder{ID: 1, Start: Rat(0, 1), Span: Rat(-1, 1)}); err == nil {
		t.Fatal("negative span must be rejected")
	}
}

// TestRandomConsistency fuzzes random occluder sets (including zero spans,
// full circles, crossing intervals, negative starts, shared layers and IDs)
// and checks the segment model against the naive sampling model.
func TestRandomConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for trial := 0; trial < 300; trial++ {
		m := New()
		ops := 1 + rng.Intn(12)
		for op := 0; op < ops; op++ {
			id := rng.Intn(6)
			switch rng.Intn(8) {
			case 0:
				m.Remove(id)
			default:
				den := int64(1 + rng.Intn(12))
				start := big.NewRat(int64(rng.Intn(1440))-720, den) // may be negative or > 360
				var span *big.Rat
				switch rng.Intn(6) {
				case 0:
					span = Rat(0, 1)
				case 1:
					span = Rat(360, 1)
				default:
					span = big.NewRat(int64(rng.Intn(360)), int64(1+rng.Intn(4)))
				}
				mustUpsert(t, m, Occluder{ID: id, Priority: rng.Intn(4), Start: start, Span: span})
			}
		}
		if err := m.CheckConsistency(); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}
