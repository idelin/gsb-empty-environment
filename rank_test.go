package main

import (
	"math/big"
	"testing"
)

func obj(id string, x, y, vx, vy int64) Object {
	return Object{ID: id, X: ratInt(x), Y: ratInt(y), VX: ratInt(vx), VY: ratInt(vy)}
}

func mkInput(objs []Object, T int64, k int) Input {
	return Input{QueryX: ratInt(0), QueryY: ratInt(0), T: ratInt(T), K: k, Objects: objs}
}

func idsOf(e *Engine, ranking []int, k int) []string {
	if k > len(ranking) {
		k = len(ranking)
	}
	out := make([]string, k)
	for i := 0; i < k; i++ {
		out[i] = e.ids[ranking[i]]
	}
	return out
}

func equalStrs(a, b []string) bool {
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

func checkSeg(t *testing.T, e *Engine, tl *Timeline, idx int, want ...string) {
	t.Helper()
	got := idsOf(e, tl.Segments[idx].Ranking, len(want))
	if !equalStrs(got, want) {
		t.Fatalf("segment %d: got %v, want %v", idx, got, want)
	}
}

func checkEvt(t *testing.T, e *Engine, tl *Timeline, idx int, want ...string) {
	t.Helper()
	got := idsOf(e, tl.Events[idx].Ranking, len(want))
	if !equalStrs(got, want) {
		t.Fatalf("event %d: got %v, want %v", idx, got, want)
	}
}

func mustVerify(t *testing.T, e *Engine, tl *Timeline) {
	t.Helper()
	if err := e.Verify(tl); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

// Irrational crossing time sqrt(2): d_A = t^2, d_B = 2.
func TestIrrationalEvent(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 0, 0, 1, 0), // d = t^2
		obj("B", 1, 1, 0, 0), // d = 2
	}, 2, 2)
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	if len(tl.Events) != 3 || len(tl.Segments) != 2 {
		t.Fatalf("got %d events, %d segments", len(tl.Events), len(tl.Segments))
	}
	ev := tl.Events[1]
	if ev.Time.IsRational() {
		t.Fatalf("expected irrational event time, got %s", ev.Time)
	}
	if ev.Time.m.Cmp(big.NewInt(2)) != 0 || ev.Time.u.Sign() != 0 || ev.Time.v.Cmp(ratInt(1)) != 0 {
		t.Fatalf("expected sqrt(2), got %s", ev.Time.ExactString())
	}
	checkSeg(t, e, tl, 0, "A", "B")
	checkEvt(t, e, tl, 1, "A", "B") // tie at sqrt(2), broken by ID
	checkSeg(t, e, tl, 1, "B", "A")
	mustVerify(t, e, tl)
}

// Tangency: d_A - d_B = (t-1)^2 >= 0, touching at t=1 without swapping.
func TestTangencyNoSwap(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 0, -1, 1, 1), // d_A = t^2 + (t-1)^2
		obj("B", 0, 0, 1, 0),  // d_B = t^2
	}, 2, 2)
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	if len(tl.Events) != 3 || len(tl.Segments) != 2 {
		t.Fatalf("got %d events, %d segments", len(tl.Events), len(tl.Segments))
	}
	if got := tl.Events[1].Time.String(); got != "1" {
		t.Fatalf("tangency at %s, want 1", got)
	}
	checkSeg(t, e, tl, 0, "B", "A")
	checkEvt(t, e, tl, 1, "A", "B") // tied at t=1, ID order
	checkSeg(t, e, tl, 1, "B", "A") // no swap after tangency
	mustVerify(t, e, tl)
}

// Identical distance functions plus a four-way simultaneous event.
// d_B = d_C = (2-t)^2 (negative velocities), d_A = 1, d_D = t^2.
// All four distances equal 1 at t=1; A, B, C tie again at t=3.
func TestIdenticalAndSimultaneousEvents(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 1, 0, 0, 0),
		obj("B", 2, 0, -1, 0),
		obj("C", 0, 2, 0, -1),
		obj("D", 0, 0, 1, 0),
	}, 4, 4)
	e := NewEngine(in)
	if !e.identical[1][2] {
		t.Fatalf("B and C should have identical distance functions")
	}
	tl := e.ComputeTimeline()
	if len(tl.Events) != 4 || len(tl.Segments) != 3 {
		t.Fatalf("got %d events, %d segments", len(tl.Events), len(tl.Segments))
	}
	if got := tl.Events[1].Time.String(); got != "1" {
		t.Fatalf("event at %s, want 1", got)
	}
	if len(tl.Events[1].Pairs) != 5 { // A-B, A-C, A-D, B-D, C-D (B-C identical, no root)
		t.Fatalf("event t=1 has %d tied pairs, want 5", len(tl.Events[1].Pairs))
	}
	checkSeg(t, e, tl, 0, "D", "A", "B", "C")
	checkEvt(t, e, tl, 1, "A", "B", "C", "D") // all tied at 1, ID order
	checkSeg(t, e, tl, 1, "B", "C", "A", "D")
	checkEvt(t, e, tl, 2, "A", "B", "C", "D") // A, B, C tied at 3
	checkSeg(t, e, tl, 2, "A", "B", "C", "D")
	mustVerify(t, e, tl)
}

// K = 0 must still produce the full event/segment structure with empty
// top-K lists.
func TestKZero(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 0, 0, 1, 0),
		obj("B", 1, 1, 0, 0),
	}, 2, 0)
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	if len(tl.Segments) != 2 || len(tl.Events) != 3 {
		t.Fatalf("got %d events, %d segments", len(tl.Events), len(tl.Segments))
	}
	if got := topIDs(e, tl.Segments[0].Ranking, in.K); got != "(none)" {
		t.Fatalf("K=0 top list = %q", got)
	}
	mustVerify(t, e, tl)
}

// K larger than the object count is clamped.
func TestKExceedsN(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 3, 0, 0, 0),
		obj("B", 0, 0, 1, 0),
	}, 5, 10)
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	checkSeg(t, e, tl, 0, "B", "A") // t^2 < 9 before t=3
	checkEvt(t, e, tl, 1, "A", "B") // tie at t=3
	checkSeg(t, e, tl, 1, "A", "B")
	if got := idsOf(e, tl.Segments[0].Ranking, in.K); len(got) != 2 {
		t.Fatalf("top-K not clamped: %v", got)
	}
	mustVerify(t, e, tl)
}

// Roots exactly at the boundary t=0 and inside; linear-vs-quadratic diff.
func TestBoundaryRootAtZero(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 1, 0, 0, 0),  // d_A = 1
		obj("B", -1, 0, 1, 0), // d_B = (t-1)^2, diff roots at t=0 and t=2
	}, 3, 2)
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	if len(tl.Events) != 3 || len(tl.Segments) != 2 {
		t.Fatalf("got %d events, %d segments", len(tl.Events), len(tl.Segments))
	}
	checkEvt(t, e, tl, 0, "A", "B") // tied at t=0
	checkSeg(t, e, tl, 0, "B", "A")
	checkEvt(t, e, tl, 1, "A", "B") // tied at t=2
	checkSeg(t, e, tl, 1, "A", "B")
	mustVerify(t, e, tl)
}

// Linear difference polynomial (one static, one moving at same speed class):
// d_A = 9, d_B = t^2 gives a rational root t=3.
func TestRationalCrossing(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 3, 0, 0, 0),
		obj("B", 0, 0, 1, 0),
	}, 5, 1)
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	checkSeg(t, e, tl, 0, "B")
	checkEvt(t, e, tl, 1, "A", "B")
	checkSeg(t, e, tl, 1, "A")
	mustVerify(t, e, tl)
}

// Negative velocity moving away from the query, with rational coefficients
// given as fractions.
func TestNegativeVelocityFractions(t *testing.T) {
	half := big.NewRat(1, 2)
	in := Input{
		QueryX: ratInt(0), QueryY: ratInt(0), T: ratInt(3), K: 2,
		Objects: []Object{
			{ID: "A", X: ratInt(0), Y: ratInt(0), VX: big.NewRat(-1, 2), VY: ratInt(0)}, // d = t^2/4
			{ID: "B", X: half, Y: ratInt(0), VX: ratInt(0), VY: ratInt(0)},              // d = 1/4
		},
	}
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	// t^2/4 = 1/4 at t=1
	if len(tl.Events) != 3 {
		t.Fatalf("got %d events", len(tl.Events))
	}
	checkSeg(t, e, tl, 0, "A", "B")
	checkEvt(t, e, tl, 1, "A", "B")
	checkSeg(t, e, tl, 1, "B", "A")
	mustVerify(t, e, tl)
}

// T = 0 degenerates to a single instant.
func TestZeroHorizon(t *testing.T) {
	in := mkInput([]Object{
		obj("A", 2, 0, 1, 0),
		obj("B", 1, 0, 0, 0),
	}, 0, 2)
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	if len(tl.Events) != 1 || len(tl.Segments) != 0 {
		t.Fatalf("got %d events, %d segments", len(tl.Events), len(tl.Segments))
	}
	checkEvt(t, e, tl, 0, "B", "A")
	mustVerify(t, e, tl)
}
