package netsec

import (
	"math/rand"
	"testing"
)

// bruteFeasible enumerates every offset pair in the intervals and reports
// whether some pair satisfies all constraints plus the extra relation edges
// expressed as (i, j, lo, hi): lo <= global[j]-global[i] <= hi.
type relBound struct {
	i, j   int
	lo, hi int64
}

func bruteFeasible(offsetA, offsetB Interval, events []Event, cons []Constraint, extra []relBound) bool {
	for oA := offsetA.Lo; oA <= offsetA.Hi; oA++ {
		for oB := offsetB.Lo; oB <= offsetB.Hi; oB++ {
			if bruteCheck(oA, oB, events, cons, extra) {
				return true
			}
		}
	}
	return false
}

func bruteCheck(oA, oB int64, events []Event, cons []Constraint, extra []relBound) bool {
	g := make([]int64, len(events))
	for i, ev := range events {
		if ev.Probe == ProbeA {
			g[i] = ev.Local + oA
		} else {
			g[i] = ev.Local + oB
		}
	}
	for _, c := range cons {
		switch c.Kind {
		case Precedence:
			if g[c.J]-g[c.I] < 0 {
				return false
			}
		case MaxGap:
			if g[c.J]-g[c.I] > c.Gap {
				return false
			}
		}
	}
	for _, r := range extra {
		d := g[r.j] - g[r.i]
		if d < r.lo || d > r.hi {
			return false
		}
	}
	return true
}

// bruteClassify mirrors Classify with exhaustive enumeration.
func bruteClassify(offsetA, offsetB Interval, events []Event, cons []Constraint, i, j int, maxGap int64) Relation {
	rel := relBound{i: i, j: j, lo: 0, hi: maxGap}
	possible := bruteFeasible(offsetA, offsetB, events, cons, []relBound{rel})
	if !possible {
		return Impossible
	}
	negLow := relBound{i: i, j: j, lo: -1 << 60, hi: -1}
	negHigh := relBound{i: i, j: j, lo: maxGap + 1, hi: 1 << 60}
	negPossible := bruteFeasible(offsetA, offsetB, events, cons, []relBound{negLow}) ||
		bruteFeasible(offsetA, offsetB, events, cons, []relBound{negHigh})
	if !negPossible {
		return Necessary
	}
	return Possible
}

func checkWitness(t *testing.T, s *System, sol *Solution) {
	t.Helper()
	if sol == nil {
		t.Fatal("expected witness, got nil")
	}
	for p := 0; p < 2; p++ {
		iv := s.OffsetInterval(Probe(p))
		if sol.Offsets[p] < iv.Lo || sol.Offsets[p] > iv.Hi {
			t.Fatalf("witness offset[%d]=%d outside %v", p, sol.Offsets[p], iv)
		}
	}
	events := s.Events()
	for i, ev := range events {
		if sol.Global[i] != ev.Local+sol.Offsets[ev.Probe] {
			t.Fatalf("witness global[%d] inconsistent with offset", i)
		}
	}
	for _, c := range s.Constraints() {
		d := sol.Global[c.J] - sol.Global[c.I]
		if c.Kind == Precedence && d < 0 {
			t.Fatalf("witness violates precedence %+v", c)
		}
		if c.Kind == MaxGap && d > c.Gap {
			t.Fatalf("witness violates max gap %+v", c)
		}
	}
}

func mustSystem(t *testing.T, a, b Interval) *System {
	t.Helper()
	s, err := NewSystem(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustAdd(t *testing.T, s *System, ev Event) int {
	t.Helper()
	i, err := s.AddEvent(ev)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// Two events on the same probe share one offset, so an inverted precedence
// between them is inconsistent no matter what the offset is.
func TestSharedProbeOffset(t *testing.T) {
	s := mustSystem(t, Interval{-50, 50}, Interval{-50, 50})
	e0 := mustAdd(t, s, Event{ProbeA, 100})
	e1 := mustAdd(t, s, Event{ProbeA, 200})
	if err := s.AddConstraint(Constraint{Kind: Precedence, I: e1, J: e0}); err != nil {
		t.Fatal(err)
	}
	sol, cyc := s.Solve()
	if sol != nil || cyc == nil {
		t.Fatalf("expected infeasible with negative cycle, got sol=%v cyc=%v", sol, cyc)
	}
	if cyc.Weight >= 0 {
		t.Fatalf("cycle weight must be negative, got %d", cyc.Weight)
	}
	if bruteFeasible(Interval{-50, 50}, Interval{-50, 50}, s.Events(), s.Constraints(), nil) {
		t.Fatal("brute force disagrees: system is feasible")
	}

	// Same local order, correct precedence: feasible, and the shared offset
	// cancels out so any offset in range works.
	s2 := mustSystem(t, Interval{-50, 50}, Interval{-50, 50})
	a0 := mustAdd(t, s2, Event{ProbeA, 100})
	a1 := mustAdd(t, s2, Event{ProbeA, 200})
	b0 := mustAdd(t, s2, Event{ProbeB, 150})
	if err := s2.AddConstraint(Constraint{Kind: Precedence, I: a0, J: a1}); err != nil {
		t.Fatal(err)
	}
	if err := s2.AddConstraint(Constraint{Kind: Precedence, I: a0, J: b0}); err != nil {
		t.Fatal(err)
	}
	sol2, cyc2 := s2.Solve()
	if cyc2 != nil {
		t.Fatalf("expected feasible, got cycle: %v", cyc2)
	}
	checkWitness(t, s2, sol2)
	if !bruteFeasible(Interval{-50, 50}, Interval{-50, 50}, s2.Events(), s2.Constraints(), nil) {
		t.Fatal("brute force disagrees: system is infeasible")
	}
}

// A gap exactly equal to the bound must be accepted (inclusive boundary).
func TestBoundaryEquality(t *testing.T) {
	s := mustSystem(t, Interval{0, 0}, Interval{5, 5})
	e0 := mustAdd(t, s, Event{ProbeA, 0})
	e1 := mustAdd(t, s, Event{ProbeB, 0})

	v, err := s.Classify(e0, e1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if v.Relation != Necessary {
		t.Fatalf("gap exactly 5 with maxGap 5: got %v, want necessary", v.Relation)
	}
	checkWitness(t, s, v.Witness)
	if v.Negation == nil || v.Negation.OutOfOrder == nil || v.Negation.GapExceeded == nil {
		t.Fatal("necessary verdict must carry negation evidence")
	}

	v, err = s.Classify(e0, e1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if v.Relation != Impossible {
		t.Fatalf("gap 5 with maxGap 4: got %v, want impossible", v.Relation)
	}
	if v.Evidence == nil || v.Evidence.Weight >= 0 {
		t.Fatalf("impossible verdict must carry a negative cycle, got %+v", v.Evidence)
	}

	// Simultaneity is allowed: precedence is non-strict.
	s2 := mustSystem(t, Interval{3, 3}, Interval{3, 3})
	f0 := mustAdd(t, s2, Event{ProbeA, 10})
	f1 := mustAdd(t, s2, Event{ProbeB, 10})
	v2, err := s2.Classify(f0, f1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Relation != Necessary {
		t.Fatalf("equal global times with maxGap 0: got %v, want necessary", v2.Relation)
	}
}

// A calibration update that contradicts offsets implied by the constraints
// must be rejected with negative-cycle evidence, keeping the old interval.
func TestContradictoryCalibrationUpdate(t *testing.T) {
	s := mustSystem(t, Interval{0, 0}, Interval{0, 200})
	e0 := mustAdd(t, s, Event{ProbeA, 100})
	e1 := mustAdd(t, s, Event{ProbeB, 0})
	// global[e0] <= global[e1] forces offset[B] >= 100.
	if err := s.AddConstraint(Constraint{Kind: Precedence, I: e0, J: e1}); err != nil {
		t.Fatal(err)
	}

	cyc, err := s.UpdateCalibration(ProbeB, Interval{0, 50})
	if err != nil {
		t.Fatal(err)
	}
	if cyc == nil {
		t.Fatal("expected contradiction evidence for offset[B] <= 50")
	}
	if cyc.Weight >= 0 {
		t.Fatalf("cycle weight must be negative, got %d", cyc.Weight)
	}
	if got := s.OffsetInterval(ProbeB); got != (Interval{0, 200}) {
		t.Fatalf("rejected update must keep old interval, got %v", got)
	}
	if bruteFeasible(Interval{0, 0}, Interval{0, 50}, s.Events(), s.Constraints(), nil) {
		t.Fatal("brute force disagrees: updated system is feasible")
	}

	// A consistent update is accepted and kept.
	cyc, err = s.UpdateCalibration(ProbeB, Interval{100, 150})
	if err != nil {
		t.Fatal(err)
	}
	if cyc != nil {
		t.Fatalf("consistent update rejected: %v", cyc)
	}
	if got := s.OffsetInterval(ProbeB); got != (Interval{100, 150}) {
		t.Fatalf("accepted update not applied, got %v", got)
	}
}

func TestMaxEvents(t *testing.T) {
	s := mustSystem(t, Interval{0, 1}, Interval{0, 1})
	for i := 0; i < MaxEvents; i++ {
		if _, err := s.AddEvent(Event{Probe(i % 2), int64(i)}); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	if _, err := s.AddEvent(Event{ProbeA, 0}); err == nil {
		t.Fatal("expected error beyond 50 events")
	}
}

// Randomised cross-check of Solve and Classify against exhaustive enumeration
// over small offset intervals.
func TestBruteForceCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	for trial := 0; trial < 3000; trial++ {
		offsetA := Interval{int64(rng.Intn(11) - 5), 0}
		offsetA.Hi = offsetA.Lo + int64(rng.Intn(6))
		offsetB := Interval{int64(rng.Intn(11) - 5), 0}
		offsetB.Hi = offsetB.Lo + int64(rng.Intn(6))

		s := mustSystem(t, offsetA, offsetB)
		n := 2 + rng.Intn(5)
		for k := 0; k < n; k++ {
			mustAdd(t, s, Event{Probe(rng.Intn(2)), int64(rng.Intn(31))})
		}
		for k := 0; k < rng.Intn(5); k++ {
			c := Constraint{I: rng.Intn(n), J: rng.Intn(n)}
			if rng.Intn(2) == 0 {
				c.Kind = Precedence
			} else {
				c.Kind = MaxGap
				c.Gap = int64(rng.Intn(21))
			}
			if err := s.AddConstraint(c); err != nil {
				t.Fatal(err)
			}
		}

		sol, cyc := s.Solve()
		bruteOk := bruteFeasible(offsetA, offsetB, s.Events(), s.Constraints(), nil)
		if (cyc == nil) != bruteOk {
			t.Fatalf("trial %d: feasibility mismatch solver=%v brute=%v\nsystem: %+v offsets %v %v",
				trial, cyc == nil, bruteOk, s.Events(), offsetA, offsetB)
		}
		if cyc == nil {
			checkWitness(t, s, sol)
		} else if cyc.Weight >= 0 {
			t.Fatalf("trial %d: cycle weight %d not negative", trial, cyc.Weight)
		}

		i, j := rng.Intn(n), rng.Intn(n)
		maxGap := int64(rng.Intn(21))
		v, err := s.Classify(i, j, maxGap)
		if err != nil {
			t.Fatal(err)
		}
		want := bruteClassify(offsetA, offsetB, s.Events(), s.Constraints(), i, j, maxGap)
		if v.Relation != want {
			t.Fatalf("trial %d: classify(%d,%d,%d)=%v, brute=%v\nevents=%+v cons=%+v offsets=%v,%v",
				trial, i, j, maxGap, v.Relation, want, s.Events(), s.Constraints(), offsetA, offsetB)
		}
		switch v.Relation {
		case Impossible:
			if v.Evidence == nil || v.Evidence.Weight >= 0 {
				t.Fatalf("trial %d: impossible without negative cycle", trial)
			}
		case Possible, Necessary:
			checkWitness(t, s, v.Witness)
		}
	}
}
