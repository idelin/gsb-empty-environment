package primeinterp

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func mustStream(t *testing.T, p int64) *Stream {
	t.Helper()
	s, err := NewStream(p)
	if err != nil {
		t.Fatalf("NewStream(%d): %v", p, err)
	}
	return s
}

// checkConsistent verifies a consistent result against a from-scratch
// interpolation of the live points and by evaluating every sample.
func checkConsistent(t *testing.T, p int64, res Result, live []Point) {
	t.Helper()
	if res.Status != StatusConsistent {
		t.Fatalf("status = %v, want consistent", res.Status)
	}
	want, err := Interpolate(p, live)
	if err != nil {
		t.Fatalf("full interpolation of live set failed: %v", err)
	}
	if !reflect.DeepEqual(res.Coeffs, want) && !(len(res.Coeffs) == 0 && len(want) == 0) {
		t.Fatalf("coeffs = %v, full interpolation gives %v", res.Coeffs, want)
	}
	for _, pt := range live {
		if y := Evaluate(p, res.Coeffs, pt.X); y != mod(pt.Y, p) {
			t.Fatalf("interpolant misses live sample (%d,%d): got %d", pt.X, pt.Y, y)
		}
	}
}

func TestStreamInsufficient(t *testing.T) {
	s := mustStream(t, 97)
	res := s.Snapshot().Query()
	if res.Status != StatusInsufficient {
		t.Fatalf("status = %v, want insufficient", res.Status)
	}
}

func TestStreamConsistent(t *testing.T) {
	p := int64(1009)
	s := mustStream(t, p)
	s.Apply(Sample{ID: "a", Revision: 1, X: 0, Y: 7})
	s.Apply(Sample{ID: "b", Revision: 1, X: 1, Y: 10})
	s.Apply(Sample{ID: "c", Revision: 1, X: 2, Y: 15})
	live := []Point{{0, 7}, {1, 10}, {2, 15}}
	checkConsistent(t, p, s.Snapshot().Query(), live)
}

func TestStreamStaleRevisionIgnored(t *testing.T) {
	p := int64(97)
	s := mustStream(t, p)
	if !s.Apply(Sample{ID: "a", Revision: 5, X: 1, Y: 2}) {
		t.Fatal("first sample should apply")
	}
	if s.Apply(Sample{ID: "a", Revision: 4, X: 9, Y: 9}) {
		t.Fatal("older revision must be ignored")
	}
	if s.Apply(Sample{ID: "a", Revision: 5, X: 9, Y: 9}) {
		t.Fatal("equal revision must be ignored")
	}
	checkConsistent(t, p, s.Snapshot().Query(), []Point{{1, 2}})
}

func TestStreamCorrectionRevokesAndLowersDegree(t *testing.T) {
	p := int64(101)
	s := mustStream(t, p)
	// (0,1), (1,2), (2,4): unique interpolant is quadratic.
	s.Apply(Sample{ID: "a", Revision: 1, X: 0, Y: 1})
	s.Apply(Sample{ID: "b", Revision: 1, X: 1, Y: 2})
	s.Apply(Sample{ID: "c", Revision: 1, X: 2, Y: 4})
	res := s.Snapshot().Query()
	if len(res.Coeffs) != 3 {
		t.Fatalf("expected degree 2, got coeffs %v", res.Coeffs)
	}
	// Local correction moves c to (2,3); the three points become collinear
	// and the lowest-degree polynomial drops to degree 1.
	if !s.Apply(Sample{ID: "c", Revision: 2, X: 2, Y: 3}) {
		t.Fatal("newer revision should apply")
	}
	res = s.Snapshot().Query()
	checkConsistent(t, p, res, []Point{{0, 1}, {1, 2}, {2, 3}})
	if !reflect.DeepEqual(res.Coeffs, []int64{1, 1}) {
		t.Fatalf("coeffs = %v, want [1 1]", res.Coeffs)
	}
}

func TestStreamConflictAndRecovery(t *testing.T) {
	p := int64(103)
	s := mustStream(t, p)
	s.Apply(Sample{ID: "a", Revision: 1, X: 4, Y: 10})
	s.Apply(Sample{ID: "b", Revision: 1, X: 5, Y: 20})
	// A second id publishes a different y at an occupied x.
	s.Apply(Sample{ID: "c", Revision: 1, X: 5, Y: 21})
	res := s.Snapshot().Query()
	if res.Status != StatusContradictory {
		t.Fatalf("status = %v, want contradictory", res.Status)
	}
	if !reflect.DeepEqual(res.Conflicts, []int64{5}) {
		t.Fatalf("conflicts = %v, want [5]", res.Conflicts)
	}
	// The author of c corrects the sample; the stream recovers.
	s.Apply(Sample{ID: "c", Revision: 2, X: 6, Y: 30})
	res = s.Snapshot().Query()
	checkConsistent(t, p, res, []Point{{4, 10}, {5, 20}, {6, 30}})

	// Contradiction introduced by a *correction* is also detected, and
	// revoking the offender restores consistency.
	s.Apply(Sample{ID: "a", Revision: 2, X: 5, Y: 99})
	if got := s.Snapshot().Query().Status; got != StatusContradictory {
		t.Fatalf("status = %v, want contradictory", got)
	}
	s.Apply(Sample{ID: "a", Revision: 3, X: 4, Y: 10})
	checkConsistent(t, p, s.Snapshot().Query(), []Point{{4, 10}, {5, 20}, {6, 30}})
}

func TestSnapshotBinding(t *testing.T) {
	p := int64(97)
	s := mustStream(t, p)
	s.Apply(Sample{ID: "a", Revision: 1, X: 1, Y: 3})
	snap1 := s.Snapshot()
	res1 := snap1.Query()
	checkConsistent(t, p, res1, []Point{{1, 3}})

	// Later mutations must not leak into the earlier snapshot.
	s.Apply(Sample{ID: "b", Revision: 1, X: 1, Y: 8}) // contradicts a
	if got := snap1.Query().Status; got != StatusConsistent {
		t.Fatalf("old snapshot changed: status = %v", got)
	}
	if again := snap1.Query(); !reflect.DeepEqual(again, res1) {
		t.Fatalf("old snapshot result mutated: %v != %v", again, res1)
	}
	if got := s.Snapshot().Query().Status; got != StatusContradictory {
		t.Fatalf("new snapshot status = %v, want contradictory", got)
	}
}

// TestStreamRandomized replays a random revisioned sample stream and, after
// every apply, checks the snapshot query against a ground-truth live set
// maintained independently by the test.
func TestStreamRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	p := int64(101)
	s := mustStream(t, p)
	live := make(map[string]Sample) // id -> newest sample

	livePoints := func() []Point {
		pts := make([]Point, 0, len(live))
		for _, smp := range live {
			pts = append(pts, Point{X: smp.X, Y: smp.Y})
		}
		return pts
	}

	for step := 0; step < 500; step++ {
		smp := Sample{
			ID:       fmt.Sprintf("id-%d", rng.Intn(12)),
			Revision: uint64(rng.Intn(8)),
			X:        rng.Int63n(2*p) - p, // exercise negative coordinates
			Y:        rng.Int63n(2*p) - p,
		}
		applied := s.Apply(smp)
		prev, known := live[smp.ID]
		wantApplied := !known || prev.Revision < smp.Revision
		if applied != wantApplied {
			t.Fatalf("step %d: applied = %v, want %v", step, applied, wantApplied)
		}
		if wantApplied {
			live[smp.ID] = smp
		}

		res := s.Snapshot().Query()
		pts := livePoints()
		switch {
		case len(pts) == 0:
			if res.Status != StatusInsufficient {
				t.Fatalf("step %d: status = %v, want insufficient", step, res.Status)
			}
		default:
			_, err := Interpolate(p, pts)
			if _, isConflict := err.(*ConflictError); isConflict {
				if res.Status != StatusContradictory {
					t.Fatalf("step %d: status = %v, want contradictory", step, res.Status)
				}
				want := conflictXs(p, pts)
				if !reflect.DeepEqual(res.Conflicts, want) {
					t.Fatalf("step %d: conflicts = %v, want %v", step, res.Conflicts, want)
				}
			} else if err != nil {
				t.Fatalf("step %d: ground-truth interpolation: %v", step, err)
			} else {
				checkConsistent(t, p, res, pts)
			}
		}
	}
}

func TestStreamRejectsNonPrime(t *testing.T) {
	if _, err := NewStream(100); err != ErrNotPrime {
		t.Fatalf("got %v, want ErrNotPrime", err)
	}
}
