package interp

import (
	"math/rand"
	"testing"
)

const testPrime = 1000003

// randPoly returns random canonical coefficients of degree exactly deg
// (deg < 0 means the zero polynomial), with entries possibly negative.
func randPoly(rng *rand.Rand, f Field, deg int) []int64 {
	if deg < 0 {
		return nil
	}
	coeffs := make([]int64, deg+1)
	for i := range coeffs {
		coeffs[i] = rng.Int63n(2*f.p) - f.p // may be negative
	}
	for f.Norm(coeffs[deg]) == 0 {
		coeffs[deg] = rng.Int63n(2*f.p) - f.p
	}
	return Trim(f, coeffs)
}

func TestFieldNegativeInputs(t *testing.T) {
	f := NewField(7)
	if got := f.Norm(-1); got != 6 {
		t.Fatalf("Norm(-1) = %d, want 6", got)
	}
	if got := f.Add(-3, -4); got != 0 { // -7 mod 7
		t.Fatalf("Add(-3,-4) = %d, want 0", got)
	}
	if got := f.Sub(1, 8); got != 0 {
		t.Fatalf("Sub(1,8) = %d, want 0", got)
	}
	if got := f.Mul(-2, 3); got != 1 { // -6 mod 7
		t.Fatalf("Mul(-2,3) = %d, want 1", got)
	}
	if got := f.Div(1, -2); got != 3 { // 1/(-2) = 1/5 = 3 mod 7
		t.Fatalf("Div(1,-2) = %d, want 3", got)
	}
	if got := f.Eval([]int64{-1, 0, 1}, -1); got != 0 { // x^2 - 1 at x=-1
		t.Fatalf("Eval = %d, want 0", got)
	}
}

func TestInterpolateRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	f := NewField(testPrime)
	for trial := 0; trial < 200; trial++ {
		deg := rng.Intn(8) - 1 // -1..6, includes zero polynomial
		coeffs := randPoly(rng, f, deg)
		n := deg + 2 + rng.Intn(3)
		if n < 1 {
			n = 1
		}
		// Distinct sample x-coordinates, some negative, some >= p.
		used := make(map[int64]bool, n)
		xs := make([]int64, n)
		ys := make([]int64, n)
		for i := range xs {
			v := rng.Int63n(f.p)
			for used[v] {
				v = rng.Int63n(f.p)
			}
			used[v] = true
			x := v
			switch rng.Intn(3) {
			case 0:
				x = -x
			case 1:
				x += f.p * int64(rng.Intn(5))
			}
			xs[i] = x
			ys[i] = f.Eval(coeffs, x)
		}
		got, err := Interpolate(f, xs, ys)
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		if !equalCoeffs(got, coeffs) {
			t.Fatalf("trial %d: got %v, want %v", trial, got, coeffs)
		}
	}
}

func TestInterpolateRedundantSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	f := NewField(testPrime)
	coeffs := randPoly(rng, f, 3)
	xs := []int64{1, 2, 3, 4}
	ys := make([]int64, len(xs))
	for i, x := range xs {
		ys[i] = f.Eval(coeffs, x)
	}
	// Append redundant copies (same x, same y), including non-normalized forms.
	xs = append(xs, 1, 2+f.p, 3-f.p)
	ys = append(ys, ys[0], ys[1], ys[2])
	got, err := Interpolate(f, xs, ys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalCoeffs(got, coeffs) {
		t.Fatalf("got %v, want %v", got, coeffs)
	}
}

func TestInterpolateRejectsConflict(t *testing.T) {
	f := NewField(testPrime)
	_, err := Interpolate(f, []int64{1, 2, 1 + f.p}, []int64{5, 6, 7})
	if err != ErrConflict {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	// Same x, same y after normalization: not a conflict.
	got, err := Interpolate(f, []int64{1, 1 - f.p}, []int64{5, 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalCoeffs(got, []int64{5}) {
		t.Fatalf("got %v, want [5]", got)
	}
}

func TestInterpolateLowestDegreeTrimmed(t *testing.T) {
	f := NewField(101)
	// Four collinear points must yield a degree-1 polynomial, not degree 3.
	xs := []int64{0, 1, 2, 3}
	ys := []int64{5, 8, 11, 14} // y = 3x + 5
	got, err := Interpolate(f, xs, ys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalCoeffs(got, []int64{5, 3}) {
		t.Fatalf("got %v, want [5 3]", got)
	}
	// Constant points collapse to a single coefficient.
	got, err = Interpolate(f, []int64{4, 5, 6}, []int64{9, 9, 9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalCoeffs(got, []int64{9}) {
		t.Fatalf("got %v, want [9]", got)
	}
	// All-zero samples yield the zero polynomial (empty slice).
	got, err = Interpolate(f, []int64{4, 5}, []int64{0, 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want zero polynomial", got)
	}
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
