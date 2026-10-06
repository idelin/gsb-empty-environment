package primeinterp

import (
	"math/rand"
	"testing"
)

var testPrimes = []int64{2, 3, 7, 97, 1009, 104729, 2147483647}

// randomPoly builds a random polynomial over GF(p) with coefficients in
// canonical (trimmed) form.
func randomPoly(rng *rand.Rand, p int64, maxDegree int) []int64 {
	deg := rng.Intn(maxDegree + 1)
	coeffs := make([]int64, deg+1)
	for i := range coeffs {
		coeffs[i] = rng.Int63n(p)
	}
	coeffs[deg] = 1 + rng.Int63n(p-1) // keep leading coefficient non-zero
	return trim(coeffs)
}

// samplePoints evaluates coeffs at n distinct random x-coordinates.
func samplePoints(rng *rand.Rand, p int64, coeffs []int64, n int) []Point {
	used := make(map[int64]bool)
	pts := make([]Point, 0, n)
	for len(pts) < n {
		x := rng.Int63n(p)
		if used[x] {
			continue
		}
		used[x] = true
		pts = append(pts, Point{X: x, Y: Evaluate(p, coeffs, x)})
	}
	return pts
}

func equalCoeffs(a, b []int64) bool {
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

func TestInterpolateRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, p := range testPrimes {
		for trial := 0; trial < 40; trial++ {
			maxDeg := 8
			if p < int64(maxDeg+2) {
				maxDeg = int(p) - 2
				if maxDeg < 0 {
					continue
				}
			}
			coeffs := randomPoly(rng, p, maxDeg)
			pts := samplePoints(rng, p, coeffs, len(coeffs))
			got, err := Interpolate(p, pts)
			if err != nil {
				t.Fatalf("p=%d trial=%d: unexpected error: %v", p, trial, err)
			}
			if !equalCoeffs(got, coeffs) {
				t.Fatalf("p=%d trial=%d: got %v, want %v", p, trial, got, coeffs)
			}
			for _, pt := range pts {
				if y := Evaluate(p, got, pt.X); y != pt.Y {
					t.Fatalf("p=%d: evaluate mismatch at x=%d: %d != %d", p, pt.X, y, pt.Y)
				}
			}
		}
	}
}

func TestInterpolateRedundantSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p := int64(1009)
	coeffs := randomPoly(rng, p, 5)
	pts := samplePoints(rng, p, coeffs, len(coeffs))
	// Duplicate every sample twice; redundancy must not change the result.
	redundant := append(append([]Point{}, pts...), pts...)
	got, err := Interpolate(p, redundant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalCoeffs(got, coeffs) {
		t.Fatalf("got %v, want %v", got, coeffs)
	}
}

func TestInterpolateConflictingX(t *testing.T) {
	p := int64(97)
	_, err := Interpolate(p, []Point{{X: 3, Y: 1}, {X: 3, Y: 2}})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("expected *ConflictError, got %v", err)
	}
	if ce.X != 3 {
		t.Fatalf("conflict at x=%d, want 3", ce.X)
	}
	// Same y at repeated x is fine.
	if _, err := Interpolate(p, []Point{{X: 3, Y: 1}, {X: 3, Y: 1}}); err != nil {
		t.Fatalf("consistent duplicates rejected: %v", err)
	}
	// Conflict after modular reduction: 3 and 3+p coincide in GF(p).
	if _, err := Interpolate(p, []Point{{X: 3, Y: 1}, {X: 3 + p, Y: 5}}); err == nil {
		t.Fatal("expected conflict for x values equal mod p")
	}
}

func TestNegativeInputs(t *testing.T) {
	p := int64(101)
	// P(x) = 2x^2 - 3x + 5, written with negative coefficients.
	coeffs := []int64{5, -3, 2}
	pts := []Point{{-4, Evaluate(p, coeffs, -4)}, {-1, Evaluate(p, coeffs, -1)}, {2, Evaluate(p, coeffs, 2)}}
	got, err := Interpolate(p, pts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int64{5, mod(-3, p), 2}
	if !equalCoeffs(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, x := range []int64{-1000, -1, 0, 7} {
		if Evaluate(p, got, x) != Evaluate(p, coeffs, x) {
			t.Fatalf("evaluation differs at x=%d", x)
		}
	}
}

func TestTrailingZeroTrim(t *testing.T) {
	p := int64(97)
	// Constant zero polynomial: canonical form is the empty slice.
	got, err := Interpolate(p, []Point{{X: 1, Y: 0}, {X: 2, Y: 0}, {X: 3, Y: 0}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("zero polynomial should trim to empty, got %v", got)
	}
	// Degree drops when extra points stay consistent with a lower degree.
	got, err = Interpolate(p, []Point{{X: 0, Y: 4}, {X: 5, Y: 4}, {X: 9, Y: 4}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalCoeffs(got, []int64{4}) {
		t.Fatalf("got %v, want [4]", got)
	}
}

func TestContradictoryExtraPoint(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	p := int64(104729)
	for trial := 0; trial < 30; trial++ {
		coeffs := randomPoly(rng, p, 6)
		pts := samplePoints(rng, p, coeffs, len(coeffs))

		// Extra point at a fresh x with a deliberately wrong y.
		x := pts[0].X
		used := map[int64]bool{}
		for _, pt := range pts {
			used[pt.X] = true
		}
		for used[x] {
			x = (x + 1) % p
		}
		wrong := Point{X: x, Y: mod(Evaluate(p, coeffs, x)+1, p)}
		got, err := Interpolate(p, append(append([]Point{}, pts...), wrong))
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		if equalCoeffs(got, coeffs) {
			t.Fatalf("trial %d: corrupted point left polynomial unchanged", trial)
		}
		// The corrupted interpolant passes through the wrong point, where
		// the true polynomial takes a different value.
		if y := Evaluate(p, got, wrong.X); y != wrong.Y {
			t.Fatalf("trial %d: interpolant misses its own sample: %d != %d", trial, y, wrong.Y)
		}
		if Evaluate(p, got, wrong.X) == Evaluate(p, coeffs, wrong.X) {
			t.Fatalf("trial %d: corrupted and true polynomials agree at x=%d", trial, wrong.X)
		}

		// Extra point reusing an existing x with a different y is rejected.
		dup := Point{X: pts[0].X, Y: mod(pts[0].Y+1, p)}
		if _, err := Interpolate(p, append(append([]Point{}, pts...), dup)); err == nil {
			t.Fatalf("trial %d: expected conflict for duplicate x", trial)
		}
	}
}

func TestNonPrimeModulus(t *testing.T) {
	for _, p := range []int64{-7, 0, 1, 4, 91, 2147483649} {
		if _, err := Interpolate(p, []Point{{X: 1, Y: 1}}); err != ErrNotPrime {
			t.Fatalf("p=%d: got %v, want ErrNotPrime", p, err)
		}
	}
}
