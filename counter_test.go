package counter

import (
	"reflect"
	"testing"
)

// bruteForce enumerates every increment d in [0, maxInc] and keeps those
// that map prev to curr modulo 2^width. It is the ground truth: it knows
// nothing about wrap arithmetic and simply tries every possibility,
// including any number of wraparounds.
func bruteForce(prev, curr uint64, width uint, maxInc uint64) []uint64 {
	modulus := uint64(1) << width
	mask := modulus - 1
	var out []uint64
	for d := uint64(0); d <= maxInc; d++ {
		if (prev+d)&mask == curr&mask {
			out = append(out, d)
		}
		if d == ^uint64(0) {
			break
		}
	}
	return out
}

// TestExhaustiveSmallWidths checks Delta against brute force for every pair
// of readings at widths 1..6 and a spread of rate budgets, covering zero,
// single and multiple wraparounds.
func TestExhaustiveSmallWidths(t *testing.T) {
	for width := uint(1); width <= 6; width++ {
		modulus := uint64(1) << width
		// Budgets chosen to allow 0, 1, 2, 3 and many wraparounds.
		budgets := []uint64{0, 1, modulus / 2, modulus - 1, modulus, modulus + 3, 2*modulus + 1, 5*modulus + 2}
		for prev := uint64(0); prev < modulus; prev++ {
			for curr := uint64(0); curr < modulus; curr++ {
				for _, maxInc := range budgets {
					p := Sample{Time: 0, Value: prev, Generation: 7}
					c := Sample{Time: 1, Value: curr, Generation: 7}
					rate := float64(maxInc) // dt == 1s => budget == maxInc
					iv, err := Delta(p, c, width, rate)
					want := bruteForce(prev, curr, width, maxInc)
					if len(want) == 0 {
						if err != ErrUnexplainable {
							t.Fatalf("w=%d %d->%d budget=%d: want ErrUnexplainable, got iv=%+v err=%v", width, prev, curr, maxInc, iv, err)
						}
						continue
					}
					if err != nil {
						t.Fatalf("w=%d %d->%d budget=%d: unexpected err %v", width, prev, curr, maxInc, err)
					}
					if got := iv.Values(); !reflect.DeepEqual(got, want) {
						t.Fatalf("w=%d %d->%d budget=%d: Values()=%v, brute force=%v", width, prev, curr, maxInc, got, want)
					}
					if iv.Min != want[0] || iv.Max != want[len(want)-1] {
						t.Fatalf("w=%d %d->%d budget=%d: interval [%d,%d], brute force [%d,%d]", width, prev, curr, maxInc, iv.Min, iv.Max, want[0], want[len(want)-1])
					}
					if iv.MaxWraps != uint64(len(want)-1) || iv.MinWraps != 0 {
						t.Fatalf("w=%d %d->%d budget=%d: wraps [%d,%d], brute force count=%d", width, prev, curr, maxInc, iv.MinWraps, iv.MaxWraps, len(want))
					}
				}
			}
		}
	}
}

// TestMultipleWraps pins the case the naive "wraps at most once" logic
// misses: a 4-bit counter read as 15 then 2 with a budget of 40 must admit
// 2, 18 and 34 (one, two and three wraparounds).
func TestMultipleWraps(t *testing.T) {
	iv, err := Delta(Sample{Time: 0, Value: 15, Generation: 1}, Sample{Time: 0, Value: 2, Generation: 1}, 4, 0)
	_ = iv
	if err != ErrNonPositiveDuration {
		t.Fatalf("same timestamp: want ErrNonPositiveDuration, got %v", err)
	}

	iv, err = Delta(Sample{Time: 0, Value: 15, Generation: 1}, Sample{Time: 4, Value: 2, Generation: 1}, 4, 10)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []uint64{3, 19, 35} // (2-15) mod 16 = 3, plus 16 and 32
	if got := iv.Values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Values()=%v, want %v", got, want)
	}
	if iv.MinWraps != 0 || iv.MaxWraps != 2 {
		t.Fatalf("wraps=[%d,%d], want [0,2]", iv.MinWraps, iv.MaxWraps)
	}
}

// TestRestart checks that a generation change is reported as a restart and
// never as a numeric increment, even when the readings would otherwise be
// explainable.
func TestRestart(t *testing.T) {
	prev := Sample{Time: 0, Value: 10, Generation: 1}
	curr := Sample{Time: 1, Value: 12, Generation: 2}
	if _, err := Delta(prev, curr, 8, 100); err != ErrRestart {
		t.Fatalf("want ErrRestart, got %v", err)
	}
	// Same generation resumes normal inference after the restart.
	if iv, err := Delta(curr, Sample{Time: 2, Value: 20, Generation: 2}, 8, 100); err != nil || iv.Min != 8 || iv.Max != 8 {
		t.Fatalf("post-restart: iv=%+v err=%v", iv, err)
	}
}

// TestUnexplainable covers jumps that no number of wraparounds can explain:
// the modular difference alone already exceeds the rate budget.
func TestUnexplainable(t *testing.T) {
	// 8-bit counter, 0 -> 200 with budget 10: base 200 > 10, and wraps
	// only add 256 each, so nothing fits.
	_, err := Delta(Sample{Time: 0, Value: 0, Generation: 1}, Sample{Time: 1, Value: 200, Generation: 1}, 8, 10)
	if err != ErrUnexplainable {
		t.Fatalf("want ErrUnexplainable, got %v", err)
	}
	// Backwards-looking jump without enough budget: 100 -> 90 needs 246.
	_, err = Delta(Sample{Time: 0, Value: 100, Generation: 1}, Sample{Time: 1, Value: 90, Generation: 1}, 8, 100)
	if err != ErrUnexplainable {
		t.Fatalf("want ErrUnexplainable, got %v", err)
	}
}

// TestWidth64 ensures the 2^64 modulus edge case (no representable second
// wrap) behaves sanely.
func TestWidth64(t *testing.T) {
	iv, err := Delta(Sample{Time: 0, Value: ^uint64(0) - 3, Generation: 1}, Sample{Time: 1, Value: 5, Generation: 1}, 64, 1000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if iv.Min != 9 || iv.Max != 9 || iv.MaxWraps != 0 {
		t.Fatalf("iv=%+v, want single increment 9", iv)
	}
}

// TestInvalidInputs covers width and ordering validation.
func TestInvalidInputs(t *testing.T) {
	s := Sample{Time: 0, Value: 0, Generation: 1}
	if _, err := Delta(s, Sample{Time: 1, Generation: 1}, 0, 1); err != ErrInvalidWidth {
		t.Fatalf("width 0: want ErrInvalidWidth, got %v", err)
	}
	if _, err := Delta(s, Sample{Time: 1, Generation: 1}, 65, 1); err != ErrInvalidWidth {
		t.Fatalf("width 65: want ErrInvalidWidth, got %v", err)
	}
	if _, err := Delta(s, Sample{Time: -1, Generation: 1}, 8, 1); err != ErrNonPositiveDuration {
		t.Fatalf("backwards time: want ErrNonPositiveDuration, got %v", err)
	}
}
