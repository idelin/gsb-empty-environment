package interp

import (
	"fmt"
	"math/rand"
	"testing"
)

// checkConsistent verifies the snapshot is consistent, matches a full
// re-interpolation of the active samples, and evaluates correctly on them.
func checkConsistent(t *testing.T, snap Snapshot, f Field, xs, ys []int64) {
	t.Helper()
	if snap.Status() != Consistent {
		t.Fatalf("status = %v, want consistent", snap.Status())
	}
	want, err := Interpolate(f, xs, ys)
	if err != nil {
		t.Fatalf("full interpolation failed: %v", err)
	}
	if got := snap.Polynomial(); !equalCoeffs(got, want) {
		t.Fatalf("polynomial = %v, want %v", got, want)
	}
	for i, x := range xs {
		if got := snap.Eval(x); got != f.Norm(ys[i]) {
			t.Fatalf("Eval(%d) = %d, want %d", x, got, f.Norm(ys[i]))
		}
	}
}

func TestStreamEmptyIsInsufficient(t *testing.T) {
	f := NewField(testPrime)
	s := NewStream(f)
	snap := s.Snapshot()
	if snap.Status() != Insufficient {
		t.Fatalf("status = %v, want insufficient", snap.Status())
	}
	if snap.Polynomial() != nil {
		t.Fatalf("polynomial = %v, want nil", snap.Polynomial())
	}
}

func TestStreamCorrectionRevokesOldContribution(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	f := NewField(testPrime)
	s := NewStream(f)

	// Target polynomial of degree 2.
	target := randPoly(rng, f, 2)
	xs := []int64{10, 20, 30}
	ids := []string{"a", "b", "c"}
	for i, x := range xs {
		s.Apply(ids[i], 1, x, f.Eval(target, x))
	}
	checkConsistent(t, s.Snapshot(), f, xs, []int64{
		f.Eval(target, 10), f.Eval(target, 20), f.Eval(target, 30),
	})

	// Correct point "b" (rev 2) so all three become collinear: the
	// lowest-degree polynomial must drop from degree 2 to degree 1.
	line := []int64{7, 4} // y = 4x + 7
	s.Apply("a", 2, 10, f.Eval(line, 10))
	s.Apply("b", 2, 20, f.Eval(line, 20))
	s.Apply("c", 2, 30, f.Eval(line, 30))
	snap := s.Snapshot()
	checkConsistent(t, snap, f, xs, []int64{
		f.Eval(line, 10), f.Eval(line, 20), f.Eval(line, 30),
	})
	if got := snap.Polynomial(); !equalCoeffs(got, line) {
		t.Fatalf("polynomial = %v, want %v (degree did not drop)", got, line)
	}

	// Stale revisions are ignored: replaying rev 1 must not change anything.
	s.Apply("b", 1, 20, f.Eval(target, 20))
	checkConsistent(t, s.Snapshot(), f, xs, []int64{
		f.Eval(line, 10), f.Eval(line, 20), f.Eval(line, 30),
	})
}

func TestStreamConflictAndRecovery(t *testing.T) {
	f := NewField(testPrime)
	s := NewStream(f)
	s.Apply("a", 1, 5, 100)
	s.Apply("b", 1, 6, 200)
	if got := s.Snapshot().Status(); got != Consistent {
		t.Fatalf("status = %v, want consistent", got)
	}

	// A new point reuses x=5 with a different y: contradiction.
	s.Apply("c", 1, 5, 999)
	snap := s.Snapshot()
	if snap.Status() != Contradictory {
		t.Fatalf("status = %v, want contradictory", snap.Status())
	}
	if snap.Polynomial() != nil {
		t.Fatalf("polynomial = %v, want nil", snap.Polynomial())
	}

	// Correcting "c" to agree with "a" recovers consistency.
	s.Apply("c", 2, 5, 100)
	checkConsistent(t, s.Snapshot(), f, []int64{5, 6, 5}, []int64{100, 200, 100})

	// Moving "c" to a fresh x keeps consistency and raises the degree.
	s.Apply("c", 3, 7, 301)
	snap = s.Snapshot()
	checkConsistent(t, snap, f, []int64{5, 6, 7}, []int64{100, 200, 301})
	if deg := len(snap.Polynomial()) - 1; deg != 2 {
		t.Fatalf("degree = %d, want 2", deg)
	}
}

func TestStreamSnapshotBinding(t *testing.T) {
	f := NewField(testPrime)
	s := NewStream(f)
	s.Apply("a", 1, 1, 10)
	s.Apply("b", 1, 2, 20)
	before := s.Snapshot()
	beforePoly := before.Polynomial()

	// Mutate the stream after taking the snapshot.
	s.Apply("a", 2, 1, 77)
	s.Apply("c", 1, 3, 30)

	// The old snapshot must be unchanged.
	if before.Status() != Consistent {
		t.Fatalf("before status = %v, want consistent", before.Status())
	}
	if got := before.Polynomial(); !equalCoeffs(got, beforePoly) {
		t.Fatalf("snapshot polynomial changed: %v -> %v", beforePoly, got)
	}
	if got := before.Eval(1); got != 10 {
		t.Fatalf("before.Eval(1) = %d, want 10", got)
	}
	if before.Samples() != 2 {
		t.Fatalf("before.Samples() = %d, want 2", before.Samples())
	}

	// The new snapshot reflects the mutation.
	after := s.Snapshot()
	if after.Samples() != 3 {
		t.Fatalf("after.Samples() = %d, want 3", after.Samples())
	}
	checkConsistent(t, after, f, []int64{1, 2, 3}, []int64{77, 20, 30})

	// Mutating a returned polynomial copy must not corrupt the snapshot.
	p := after.Polynomial()
	for i := range p {
		p[i] = 0
	}
	if got := after.Eval(1); got != 77 {
		t.Fatalf("after.Eval(1) = %d, want 77", got)
	}
}

func TestStreamRandomizedAgainstFullInterpolation(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	f := NewField(101)
	s := NewStream(f)
	active := make(map[string][2]int64) // id -> (x, y)
	rev := make(map[string]uint64)

	for step := 0; step < 500; step++ {
		id := fmt.Sprintf("p%d", rng.Intn(12))
		rev[id]++
		x := rng.Int63n(10) - 5 // small range forces frequent x collisions
		y := rng.Int63n(int64(f.Modulus()))
		s.Apply(id, rev[id], x, y)
		active[id] = [2]int64{x, y}

		xs := make([]int64, 0, len(active))
		ys := make([]int64, 0, len(active))
		for _, pt := range active {
			xs = append(xs, pt[0])
			ys = append(ys, pt[1])
		}
		snap := s.Snapshot()
		_, err := Interpolate(f, xs, ys)
		switch {
		case len(xs) == 0:
			if snap.Status() != Insufficient {
				t.Fatalf("step %d: status = %v, want insufficient", step, snap.Status())
			}
		case err == ErrConflict:
			if snap.Status() != Contradictory {
				t.Fatalf("step %d: status = %v, want contradictory", step, snap.Status())
			}
		case err != nil:
			t.Fatalf("step %d: unexpected error: %v", step, err)
		default:
			checkConsistent(t, snap, f, xs, ys)
		}
	}
}
