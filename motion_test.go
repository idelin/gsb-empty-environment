package motion

import (
	"math"
	"testing"
)

func v(x ...float64) Vec { return Vec(x) }

func seg(t0, t1 float64, p0, vel Vec) Segment {
	return Segment{T0: t0, T1: t1, P0: p0, V: vel}
}

func almostEq(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func checkSpan(t *testing.T, spans []Span, idx int, start, end float64, sc, ec bool) {
	t.Helper()
	if len(spans) <= idx {
		t.Fatalf("expected span %d, got only %d spans: %v", idx, len(spans), spans)
	}
	s := spans[idx]
	if !almostEq(s.Start, start) || !almostEq(s.End, end) ||
		s.StartClosed != sc || s.EndClosed != ec {
		t.Fatalf("span %d = [%v,%v] closed=(%v,%v), want [%v,%v] closed=(%v,%v)",
			idx, s.Start, s.End, s.StartClosed, s.EndClosed, start, end, sc, ec)
	}
}

// The minimum of the squared distance lies strictly inside the overlap, so
// any implementation that only inspects segment endpoints misses this span.
func TestInteriorCrossing(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{seg(0, 10, v(-10, 0), v(3, 0))}
	spans, err := Coverage(a, b, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 5.0/3.0, 5, true, true)
	if spans[0].StartRoot == nil || spans[0].EndRoot == nil {
		t.Fatal("interior roots must carry QuadRoot isolation")
	}
	if got := spans[0].StartRoot.Float64(); !almostEq(got, 5.0/3.0) {
		t.Fatalf("StartRoot.Float64() = %v, want %v", got, 5.0/3.0)
	}
	if got := spans[0].EndRoot.Float64(); !almostEq(got, 5) {
		t.Fatalf("EndRoot.Float64() = %v, want 5", got)
	}
}

// Tangency: the trajectories touch the threshold at exactly one instant.
func TestGrazingPoint(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{seg(0, 10, v(-5, 3), v(1, 0))}
	spans, err := Coverage(a, b, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 5, 5, true, true)
	if !Contains(spans, 5) || Contains(spans, 4.999) || Contains(spans, 5.001) {
		t.Fatal("grazing instant must be covered, neighbours must not")
	}
}

// Stationary trajectories: constant squared distance.
func TestStationary(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{seg(0, 10, v(4, 0), v(0, 0))}

	spans, err := Coverage(a, b, 16) // exactly at threshold
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 0, 10, true, false) // half-open domain end

	spans, err = Coverage(a, b, 15.99)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 0 {
		t.Fatalf("expected no coverage, got %v", spans)
	}
}

// A position jump at a breakpoint moves the pair out of range: the span
// ends open at the breakpoint even though the left limit qualifies.
func TestJumpOutAtBreakpoint(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{
		seg(0, 5, v(1, 0), v(0, 0)),
		seg(5, 10, v(10, 0), v(0, 0)), // jump to (10,0) at t=5
	}
	spans, err := Coverage(a, b, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 0, 5, true, false)
	if Contains(spans, 5) {
		t.Fatal("t=5 belongs to the second segment, which is out of range")
	}
}

// A jump into range starts a span closed at the breakpoint.
func TestJumpInAtBreakpoint(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{
		seg(0, 5, v(10, 0), v(0, 0)),
		seg(5, 10, v(1, 0), v(0, 0)),
	}
	spans, err := Coverage(a, b, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 5, 10, true, false)
	if !Contains(spans, 5) {
		t.Fatal("t=5 belongs to the second segment, which is in range")
	}
}

// Both sides qualify across the breakpoint, so the two half-open pieces
// merge into one continuous span.
func TestMergeAcrossBreakpoint(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{
		seg(0, 5, v(1, 0), v(0, 0)),
		seg(5, 10, v(2, 0), v(0, 0)),
	}
	spans, err := Coverage(a, b, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected one merged span, got %v", spans)
	}
	checkSpan(t, spans, 0, 0, 10, true, false)
}

// A jump landing exactly on the threshold is included (<=), so the pieces
// merge across the breakpoint.
func TestJumpOntoThreshold(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{
		seg(0, 5, v(1, 0), v(0, 0)),
		seg(5, 10, v(5, 0), v(0, 0)), // |5|^2 = 25 = threshold at t=5
	}
	spans, err := Coverage(a, b, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected one merged span, got %v", spans)
	}
	checkSpan(t, spans, 0, 0, 10, true, false)
	if !Contains(spans, 5) {
		t.Fatal("instant on the threshold must be covered")
	}
}

// The grazing instant coincides with a breakpoint. The left pair's tangency
// sits at its excluded end; the right pair owns the instant.
func TestGrazingAtBreakpoint(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{
		seg(0, 5, v(-5, 3), v(1, 0)), // approaches (0,3) as t->5
		seg(5, 10, v(0, 3), v(1, 0)), // departs from (0,3)
	}
	spans, err := Coverage(a, b, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 5, 5, true, true)
}

// A gap in one trajectory produces no coverage, and spans on either side of
// the gap stay separate.
func TestGapInTrajectory(t *testing.T) {
	a := Trajectory{seg(0, 10, v(0, 0), v(0, 0))}
	b := Trajectory{
		seg(0, 5, v(1, 0), v(0, 0)),
		seg(7, 10, v(1, 0), v(0, 0)), // undefined on [5,7)
	}
	spans, err := Coverage(a, b, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 0, 5, true, false)
	checkSpan(t, spans, 1, 7, 10, true, false)
	if Contains(spans, 6) {
		t.Fatal("gap must not be covered")
	}
}

// Same-direction equal velocities give constant distance; crossing works in
// three dimensions too.
func TestThreeDimensional(t *testing.T) {
	a := Trajectory{seg(0, 4, v(0, 0, 0), v(1, 0, 0))}
	b := Trajectory{seg(0, 4, v(0, 3, 0), v(1, 0, 0))}
	spans, err := Coverage(a, b, 9) // constant distance 3
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans: %v", len(spans), spans)
	}
	checkSpan(t, spans, 0, 0, 4, true, false)
}

func TestValidation(t *testing.T) {
	bad := Trajectory{seg(2, 2, v(0), v(0))}
	if _, err := Coverage(bad, bad, 1); err == nil {
		t.Fatal("expected error for empty segment interval")
	}
	overlap := Trajectory{seg(0, 3, v(0), v(0)), seg(2, 4, v(0), v(0))}
	if _, err := Coverage(overlap, overlap, 1); err == nil {
		t.Fatal("expected error for overlapping segments")
	}
	a := Trajectory{seg(0, 1, v(0, 0), v(0, 0))}
	b := Trajectory{seg(0, 1, v(0, 0, 0), v(0, 0, 0))}
	if _, err := Coverage(a, b, 1); err == nil {
		t.Fatal("expected error for mismatched dimensions")
	}
	if _, err := Coverage(a, a, -1); err == nil {
		t.Fatal("expected error for negative threshold")
	}
}
