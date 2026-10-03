package stgrid

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func almostEq(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestSegmentDistance(t *testing.T) {
	cases := []struct {
		p1, q1, p2, q2 Vec3
		want           float64
	}{
		// parallel segments, 1 m apart
		{Vec3{0, 0, 0}, Vec3{1, 0, 0}, Vec3{0, 1, 0}, Vec3{1, 1, 0}, 1},
		// skew segments, closest points (1,0,0) and (1,0,1)
		{Vec3{0, 0, 0}, Vec3{2, 0, 0}, Vec3{1, -1, 1}, Vec3{1, 1, 1}, 1},
		// degenerate point vs segment
		{Vec3{0, 0, 0}, Vec3{0, 0, 0}, Vec3{1, 0, 0}, Vec3{2, 0, 0}, 1},
		// intersecting segments
		{Vec3{-1, 0, 0}, Vec3{1, 0, 0}, Vec3{0, -1, 0}, Vec3{0, 1, 0}, 0},
	}
	for i, c := range cases {
		if got := SegmentDistance(c.p1, c.q1, c.p2, c.q2); !almostEq(got, c.want, 1e-9) {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}

func TestBreachWindow(t *testing.T) {
	// head-on approach: 100 m apart, closing 10 m/s, sep 10 -> breach at t=9
	if tb, ok := BreachWindow(Vec3{100, 0, 0}, Vec3{-10, 0, 0}, 10, 20); !ok || !almostEq(tb, 9, 1e-9) {
		t.Errorf("head-on: got t=%v ok=%v, want t=9 ok=true", tb, ok)
	}
	// same setup, window too short
	if _, ok := BreachWindow(Vec3{100, 0, 0}, Vec3{-10, 0, 0}, 10, 5); ok {
		t.Error("short window: expected no breach")
	}
	// receding
	if _, ok := BreachWindow(Vec3{100, 0, 0}, Vec3{10, 0, 0}, 10, 20); ok {
		t.Error("receding: expected no breach")
	}
	// already inside the safety bubble
	if tb, ok := BreachWindow(Vec3{5, 0, 0}, Vec3{1, 0, 0}, 10, 20); !ok || tb != 0 {
		t.Errorf("inside: got t=%v ok=%v, want t=0 ok=true", tb, ok)
	}
	// stationary outside
	if _, ok := BreachWindow(Vec3{100, 0, 0}, Vec3{0, 0, 0}, 10, 20); ok {
		t.Error("stationary: expected no breach")
	}
}

func TestGridInsertCellCount(t *testing.T) {
	g := NewGrid(170) // maxSpeed 120 * slot 1s + minSep 50
	c := Capsule{P0: Vec3{10, 20, 30}, P1: Vec3{60, 20, 30}, Radius: 25}
	g.Insert(0, c, 0)
	if n := len(g.Cells); n == 0 || n > 8 {
		t.Errorf("capsule touched %d cells, want 1..8", n)
	}
}

// The broad phase must never miss a pair whose swept capsules intersect.
func TestBroadPhaseMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 300
	const minSep, maxSpeed, slot = 50.0, 120.0, 1.0
	ents := make([]Entity, n)
	for i := range ents {
		ents[i] = Entity{
			ID:  fmt.Sprintf("E%03d", i),
			Pos: Vec3{rng.Float64() * 1000, rng.Float64() * 1000, rng.Float64() * 1000},
			Vel: Vec3{(rng.Float64() - 0.5) * 2 * maxSpeed, (rng.Float64() - 0.5) * 2 * maxSpeed, (rng.Float64() - 0.5) * 2 * maxSpeed},
		}
	}
	caps := make([]Capsule, n)
	g := NewGrid(maxSpeed*slot + minSep)
	for i, e := range ents {
		caps[i] = Capsule{P0: e.Pos, P1: e.Pos.Add(e.Vel.Scale(slot)), Radius: minSep / 2}
		g.Insert(i, caps[i], 0)
	}
	cand := make(map[[2]int]bool)
	for _, p := range g.CandidatePairs() {
		cand[p] = true
	}
	missed := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if caps[i].Intersects(caps[j]) && !cand[[2]int{i, j}] {
				missed++
			}
		}
	}
	if missed > 0 {
		t.Fatalf("broad phase missed %d intersecting capsule pairs", missed)
	}
}

// buildAcceptanceScenario creates 500 air traffic units: A and B on a
// head-on course that breaches the 50 m minimum separation exactly 10 s
// after the start, plus 498 units on a well-separated lattice.
func buildAcceptanceScenario() []Entity {
	ents := make([]Entity, 0, 500)
	ents = append(ents,
		Entity{ID: "A", Pos: Vec3{0, 0, 0}, Vel: Vec3{50, 0, 0}},
		Entity{ID: "B", Pos: Vec3{1050, 0, 0}, Vel: Vec3{-50, 0, 0}},
	)
	rng := rand.New(rand.NewSource(42))
	for x := 0; x < 8 && len(ents) < 500; x++ {
		for y := 0; y < 8 && len(ents) < 500; y++ {
			for z := 0; z < 8 && len(ents) < 500; z++ {
				ents = append(ents, Entity{
					ID:  fmt.Sprintf("U%03d", len(ents)),
					Pos: Vec3{float64(x)*2000 - 8000, float64(y)*2000 - 8000, float64(z)*2000 + 5000},
					Vel: Vec3{rng.Float64()*40 - 20, rng.Float64()*40 - 20, rng.Float64()*40 - 20},
				})
			}
		}
	}
	return ents
}

func advance(ents []Entity, dt float64) {
	for i := range ents {
		ents[i].Pos = ents[i].Pos.Add(ents[i].Vel.Scale(dt))
	}
}

// Acceptance: 500 units; the engine predicts the A/B breach of the 50 m
// minimum separation within the next 10 s, and emits the warning beacon
// with the current topological distance exactly 5 s before the breach.
func TestAcceptance500Units(t *testing.T) {
	eng := NewEngine(50 /*minSep m*/, 1 /*slot s*/, 10 /*horizon slots*/, 5 /*warn lead s*/, 120 /*maxSpeed m/s*/)
	ents := buildAcceptanceScenario()
	if len(ents) != 500 {
		t.Fatalf("scenario has %d entities, want 500", len(ents))
	}

	// At t=0 the breach at t=10 must already be visible inside the horizon.
	breaches := eng.Detect(ents)
	if len(breaches) != 1 || breaches[0].A != "A" || breaches[0].B != "B" {
		t.Fatalf("t=0 breaches = %+v, want exactly the A/B pair", breaches)
	}
	if !almostEq(breaches[0].TimeToBreach, 10, 1e-9) {
		t.Errorf("t=0 predicted breach in %v s, want 10 s", breaches[0].TimeToBreach)
	}

	firstWarn := -1.0
	for tick := 0; tick < 10; tick++ {
		now := float64(tick)
		for _, bc := range eng.Beacons(ents, now) {
			if bc.A == "A" && bc.B == "B" {
				if firstWarn < 0 {
					firstWarn = now
					if !almostEq(bc.CurrentDistance, 550, 1e-6) {
						t.Errorf("beacon current distance = %v m, want 550 m", bc.CurrentDistance)
					}
					if !almostEq(bc.TimeToBreach, 5, 1e-9) {
						t.Errorf("beacon time-to-breach = %v s, want 5 s", bc.TimeToBreach)
					}
				}
			} else {
				t.Errorf("unexpected beacon for pair %s/%s at t=%v", bc.A, bc.B, now)
			}
		}
		advance(ents, 1)
	}
	if firstWarn != 5 {
		t.Fatalf("first A/B warning emitted at t=%v s, want exactly t=5 s (5 s before breach)", firstWarn)
	}

	// The grid broad phase must be far cheaper than an all-pairs scan.
	if eng.LastCandidates >= eng.LastBruteForce/10 {
		t.Errorf("broad phase tested %d pairs vs %d brute-force; expected >10x reduction",
			eng.LastCandidates, eng.LastBruteForce)
	}
	t.Logf("broad phase: %d candidate pairs vs %d brute-force pairs (%.1fx reduction), %d cells",
		eng.LastCandidates, eng.LastBruteForce,
		float64(eng.LastBruteForce)/float64(eng.LastCandidates), eng.LastCells)
}

func TestNoFalsePositives(t *testing.T) {
	eng := NewEngine(50, 1, 10, 5, 120)
	ents := buildAcceptanceScenario()[2:] // drop A and B
	if b := eng.Detect(ents); len(b) != 0 {
		t.Fatalf("expected no breaches among separated units, got %+v", b)
	}
}
