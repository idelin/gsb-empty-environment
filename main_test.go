package main

import (
	"math/big"
	"testing"
)

func r(s string) *big.Rat {
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return v
}

func obj(id int64, x, y, vx, vy string) Object {
	return Object{ID: id, X: r(x), Y: r(y), Vx: r(vx), Vy: r(vy)}
}

// eventsWithPairs returns the non-boundary-only events (those carrying pairs).
func eventsWithPairs(c *Computation) []*Event {
	var out []*Event
	for _, e := range c.Events {
		if len(e.Pairs) > 0 {
			out = append(out, e)
		}
	}
	return out
}

func mustVerify(t *testing.T, c *Computation) {
	t.Helper()
	_, failures := c.Verify()
	if len(failures) > 0 {
		t.Fatalf("verification failed:\n%v", failures)
	}
}

func idsEqual(got []int64, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCrossingSwapRationalRoot(t *testing.T) {
	objs := []Object{
		obj(1, "3", "0", "-1", "0"), // d^2 = (3-t)^2
		obj(2, "1", "0", "1", "0"),  // d^2 = (1+t)^2
	}
	c := Compute(objs, r("0"), r("0"), r("2"), 1)
	mustVerify(t, c)

	evs := eventsWithPairs(c)
	if len(evs) != 1 || evs[0].T.Cmp(ratAlg(r("1"))) != 0 {
		t.Fatalf("expected single event at t=1, got %v", evs)
	}
	if !idsEqual(evs[0].Ranking, 1, 2) { // tie at t=1 broken by ID
		t.Fatalf("event ranking = %v, want [1 2]", evs[0].Ranking)
	}
	if len(c.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(c.Segments))
	}
	if !idsEqual(c.Segments[0].Ranking, 2, 1) {
		t.Fatalf("first segment ranking = %v, want [2 1]", c.Segments[0].Ranking)
	}
	if !idsEqual(c.Segments[1].Ranking, 1, 2) {
		t.Fatalf("second segment ranking = %v, want [1 2]", c.Segments[1].Ranking)
	}
	if !idsEqual(topK(c.Segments[0].Ranking, c.K), 2) || !idsEqual(topK(c.Segments[1].Ranking, c.K), 1) {
		t.Fatalf("top-1 did not swap")
	}
}

func TestIrrationalEventTime(t *testing.T) {
	objs := []Object{
		obj(1, "0", "0", "1", "0"), // d^2 = t^2
		obj(2, "1", "1", "0", "0"), // d^2 = 2
	}
	c := Compute(objs, r("0"), r("0"), r("3"), 2)
	mustVerify(t, c)

	evs := eventsWithPairs(c)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	want := mkAlg(r("0"), r("1"), big.NewInt(2)) // sqrt(2)
	if !evs[0].T.Equal(want) {
		t.Fatalf("event time = %s, want sqrt(2)", evs[0].T)
	}
	if evs[0].T.String() != "1*sqrt(2)" {
		t.Fatalf("event time string = %q", evs[0].T.String())
	}
	// Isolating interval must bracket sqrt(2) with rational endpoints.
	if ratAlg(evs[0].Lo).Cmp(evs[0].T) > 0 || ratAlg(evs[0].Hi).Cmp(evs[0].T) < 0 {
		t.Fatalf("isolating interval [%s,%s] does not contain %s", evs[0].Lo, evs[0].Hi, evs[0].T)
	}
	if !idsEqual(c.Segments[0].Ranking, 1, 2) || !idsEqual(c.Segments[1].Ranking, 2, 1) {
		t.Fatalf("segment rankings = %v, %v", c.Segments[0].Ranking, c.Segments[1].Ranking)
	}
	if !idsEqual(evs[0].Ranking, 1, 2) { // exact tie at sqrt(2), ID order
		t.Fatalf("event ranking = %v, want [1 2]", evs[0].Ranking)
	}
}

func TestTangencyNoSwap(t *testing.T) {
	objs := []Object{
		obj(1, "0", "-1", "1", "1"), // d^2 = t^2 + (t-1)^2
		obj(2, "0", "0", "1", "0"),  // d^2 = t^2
	}
	// Difference is (t-1)^2: a double root at t=1, order never swaps.
	c := Compute(objs, r("0"), r("0"), r("2"), 2)
	mustVerify(t, c)

	evs := eventsWithPairs(c)
	if len(evs) != 1 || evs[0].T.Cmp(ratAlg(r("1"))) != 0 {
		t.Fatalf("expected tangency event at t=1, got %v", evs)
	}
	// Both open segments keep object 2 strictly closer.
	if !idsEqual(c.Segments[0].Ranking, 2, 1) || !idsEqual(c.Segments[1].Ranking, 2, 1) {
		t.Fatalf("segments should not swap: %v, %v", c.Segments[0].Ranking, c.Segments[1].Ranking)
	}
	// At the exact tangency instant the distances tie and ID wins.
	if !idsEqual(evs[0].Ranking, 1, 2) {
		t.Fatalf("tangency instant ranking = %v, want [1 2]", evs[0].Ranking)
	}
}

func TestCoincidentTrajectories(t *testing.T) {
	objs := []Object{
		obj(5, "1", "2", "1", "-1"),
		obj(3, "1", "2", "1", "-1"), // identical trajectory, smaller ID
	}
	c := Compute(objs, r("0"), r("0"), r("5"), 2)
	mustVerify(t, c)

	if len(eventsWithPairs(c)) != 0 {
		t.Fatalf("coincident trajectories must produce no pair events: %v", eventsWithPairs(c))
	}
	if len(c.Segments) != 1 {
		t.Fatalf("expected a single segment, got %d", len(c.Segments))
	}
	for _, e := range c.Events {
		if !idsEqual(e.Ranking, 3, 5) {
			t.Fatalf("ranking at %s = %v, want [3 5] (permanent ID tiebreak)", e.T, e.Ranking)
		}
	}
	if !idsEqual(c.Segments[0].Ranking, 3, 5) {
		t.Fatalf("segment ranking = %v, want [3 5]", c.Segments[0].Ranking)
	}
}

func TestIdenticalDistanceFunctions(t *testing.T) {
	objs := []Object{
		obj(1, "1", "0", "0", "1"), // d^2 = 1 + t^2
		obj(2, "0", "1", "1", "0"), // d^2 = 1 + t^2, different trajectory
	}
	c := Compute(objs, r("0"), r("0"), r("4"), 2)
	mustVerify(t, c)

	if len(eventsWithPairs(c)) != 0 || len(c.Segments) != 1 {
		t.Fatalf("identical distance functions must yield no events")
	}
	if !idsEqual(c.Segments[0].Ranking, 1, 2) {
		t.Fatalf("segment ranking = %v, want [1 2]", c.Segments[0].Ranking)
	}
}

func TestNegativeVelocitiesTwoRoots(t *testing.T) {
	objs := []Object{
		obj(1, "5", "0", "-2", "0"), // d^2 = (5-2t)^2
		obj(2, "1", "0", "0", "0"),  // d^2 = 1
	}
	// Difference = 4(t-2)(t-3): roots at t=2 and t=3.
	c := Compute(objs, r("0"), r("0"), r("4"), 2)
	mustVerify(t, c)

	evs := eventsWithPairs(c)
	if len(evs) != 2 || evs[0].T.Cmp(ratAlg(r("2"))) != 0 || evs[1].T.Cmp(ratAlg(r("3"))) != 0 {
		t.Fatalf("expected events at t=2 and t=3, got %v", evs)
	}
	wantSeg := [][]int64{{2, 1}, {1, 2}, {2, 1}}
	if len(c.Segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(c.Segments))
	}
	for i, w := range wantSeg {
		if !idsEqual(c.Segments[i].Ranking, w...) {
			t.Fatalf("segment %d ranking = %v, want %v", i, c.Segments[i].Ranking, w)
		}
	}
	for _, e := range evs { // both instants are exact ties -> ID order
		if !idsEqual(e.Ranking, 1, 2) {
			t.Fatalf("event at %s ranking = %v, want [1 2]", e.T, e.Ranking)
		}
	}
}

func TestSimultaneousEvents(t *testing.T) {
	objs := []Object{
		obj(1, "2", "0", "-1", "0"), // d^2 = (2-t)^2
		obj(2, "0", "0", "1", "0"),  // d^2 = t^2
		obj(3, "0", "3", "0", "-1"), // d^2 = (3-t)^2
		obj(4, "0", "1", "0", "1"),  // d^2 = (1+t)^2
	}
	c := Compute(objs, r("0"), r("0"), r("2"), 4)
	mustVerify(t, c)

	var atOne *Event
	for _, e := range c.Events {
		if e.T.Cmp(ratAlg(r("1"))) == 0 {
			atOne = e
		}
	}
	if atOne == nil {
		t.Fatalf("no event at t=1")
	}
	if len(atOne.Pairs) != 2 { // pairs (1,2) and (3,4) tie simultaneously
		t.Fatalf("event at t=1 carries %d pairs, want 2", len(atOne.Pairs))
	}
	if !idsEqual(atOne.Ranking, 1, 2, 3, 4) { // both ties broken by ID
		t.Fatalf("ranking at t=1 = %v, want [1 2 3 4]", atOne.Ranking)
	}
	// Just before t=1 the order is [2 1 4 3].
	for _, s := range c.Segments {
		if s.B.Cmp(ratAlg(r("1"))) == 0 && !idsEqual(s.Ranking, 2, 1, 4, 3) {
			t.Fatalf("segment before t=1 = %v, want [2 1 4 3]", s.Ranking)
		}
	}
}

func TestKZero(t *testing.T) {
	objs := []Object{
		obj(1, "3", "0", "-1", "0"),
		obj(2, "1", "0", "1", "0"),
	}
	c := Compute(objs, r("0"), r("0"), r("2"), 0)
	mustVerify(t, c)
	for _, e := range c.Events {
		if got := topK(e.Ranking, c.K); len(got) != 0 {
			t.Fatalf("K=0 topK = %v, want empty", got)
		}
	}
	for _, s := range c.Segments {
		if got := topK(s.Ranking, c.K); len(got) != 0 {
			t.Fatalf("K=0 topK = %v, want empty", got)
		}
	}
}

func TestKExceedsObjectCount(t *testing.T) {
	objs := []Object{
		obj(1, "3", "0", "-1", "0"),
		obj(2, "1", "0", "1", "0"),
	}
	c := Compute(objs, r("0"), r("0"), r("2"), 10)
	mustVerify(t, c)
	for _, s := range c.Segments {
		if got := topK(s.Ranking, c.K); len(got) != 2 {
			t.Fatalf("K=10 with 2 objects: topK = %v, want full ranking", got)
		}
	}
}

func TestRootAtBoundary(t *testing.T) {
	objs := []Object{
		obj(1, "1", "0", "1", "0"),  // d^2 = (1+t)^2
		obj(2, "1", "0", "-1", "0"), // d^2 = (1-t)^2
	}
	// Difference = 4t: the only root is exactly t=0.
	c := Compute(objs, r("0"), r("0"), r("2"), 2)
	mustVerify(t, c)

	first := c.Events[0]
	if first.T.Sign() != 0 || len(first.Pairs) != 1 {
		t.Fatalf("first event should be the boundary root at t=0: %+v", first)
	}
	if !idsEqual(first.Ranking, 1, 2) { // tie at t=0 -> ID order
		t.Fatalf("ranking at t=0 = %v, want [1 2]", first.Ranking)
	}
	if len(c.Segments) != 1 || !idsEqual(c.Segments[0].Ranking, 2, 1) {
		t.Fatalf("single segment ranking = %v, want [2 1]", c.Segments[0].Ranking)
	}
}

func TestRootsFromDifferentPolynomialsCluster(t *testing.T) {
	// t^2 - 2 and 2t^2 - 4 share the root sqrt(2); it must be one event.
	p1 := mkPoly(r("1"), r("0"), r("-2"))
	p2 := mkPoly(r("2"), r("0"), r("-4"))
	r1, r2 := p1.Roots(), p2.Roots()
	if len(r1) != 2 || len(r2) != 2 {
		t.Fatalf("expected 2 roots each")
	}
	if !r1[1].Equal(r2[1]) {
		t.Fatalf("shared root not recognized as equal: %s vs %s", r1[1], r2[1])
	}
	if r1[0].Cmp(ratAlg(r("-1"))) >= 0 { // -sqrt(2) < -1
		t.Fatalf("negative root compare wrong")
	}
}

func TestCLIEndToEnd(t *testing.T) {
	input := []byte(`{
	  "query": {"x": "0", "y": "0"},
	  "T": "3",
	  "K": 1,
	  "objects": [
	    {"id": 1, "x": "0", "y": "0", "vx": "1", "vy": "0"},
	    {"id": 2, "x": "1", "y": "1", "vx": "0", "vy": "0"}
	  ]
	}`)
	out, ok, err := run(input)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !ok {
		t.Fatalf("verification failed: %s", out)
	}
	if len(out) == 0 {
		t.Fatalf("empty output")
	}
}
