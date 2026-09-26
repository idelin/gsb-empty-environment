package wavelet

import (
	"math/rand"
	"sort"
	"testing"
)

// bruteKth sorts a copy of the subarray (test-side oracle only; the tree
// itself never sorts query ranges) and returns the k-th smallest, 1-based.
func bruteKth(vals []int, l, r, k int) int {
	tmp := append([]int(nil), vals[l:r+1]...)
	sort.Ints(tmp)
	return tmp[k-1]
}

func bruteFreq(vals []int, l, r, a, b int) int {
	c := 0
	for i := l; i <= r; i++ {
		if vals[i] >= a && vals[i] <= b {
			c++
		}
	}
	return c
}

func TestAccessRoundTrip(t *testing.T) {
	vals := []int{-5, 3, -5, 7, 3, 3, -2, 7, 7, 7, 0, -5, 1000000, -1000000}
	tr, err := New(vals)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Len() != len(vals) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(vals))
	}
	for i, want := range vals {
		got, err := tr.Access(i)
		if err != nil {
			t.Fatalf("Access(%d): %v", i, err)
		}
		if got != want {
			t.Errorf("Access(%d) = %d, want %d", i, got, want)
		}
	}
	if _, err := tr.Access(-1); err != ErrBounds {
		t.Errorf("Access(-1) err = %v, want ErrBounds", err)
	}
	if _, err := tr.Access(len(vals)); err != ErrBounds {
		t.Errorf("Access(n) err = %v, want ErrBounds", err)
	}
}

func TestQuantileDuplicatesAndNegatives(t *testing.T) {
	vals := []int{-5, 3, -5, 7, 3, 3, -2, 7, 7, 7, 0, -5}
	tr, err := New(vals)
	if err != nil {
		t.Fatal(err)
	}
	for l := 0; l < len(vals); l++ {
		for r := l; r < len(vals); r++ {
			for k := 1; k <= r-l+1; k++ {
				got, err := tr.Quantile(l, r, k)
				if err != nil {
					t.Fatalf("Quantile(%d,%d,%d): %v", l, r, k, err)
				}
				if want := bruteKth(vals, l, r, k); got != want {
					t.Fatalf("Quantile(%d,%d,%d) = %d, want %d", l, r, k, got, want)
				}
			}
		}
	}
	// duplicates must count with multiplicity: whole range has three -5s
	for k := 1; k <= 3; k++ {
		if got, _ := tr.Quantile(0, len(vals)-1, k); got != -5 {
			t.Errorf("Quantile(0,n-1,%d) = %d, want -5", k, got)
		}
	}
}

func TestQuantileRejectsEmptyAndBadK(t *testing.T) {
	tr, err := New([]int{4, 1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Quantile(2, 1, 1); err != ErrEmpty {
		t.Errorf("empty interval err = %v, want ErrEmpty", err)
	}
	if _, err := tr.Quantile(0, 2, 0); err != ErrBounds {
		t.Errorf("k=0 err = %v, want ErrBounds", err)
	}
	if _, err := tr.Quantile(0, 2, 4); err != ErrBounds {
		t.Errorf("k>len err = %v, want ErrBounds", err)
	}
	if _, err := tr.Quantile(0, 3, 1); err != ErrBounds {
		t.Errorf("r out of range err = %v, want ErrBounds", err)
	}
}

func TestRangeFreq(t *testing.T) {
	vals := []int{-5, 3, -5, 7, 3, 3, -2, 7, 7, 7, 0, -5}
	tr, err := New(vals)
	if err != nil {
		t.Fatal(err)
	}
	queries := [][2]int{{-100, 100}, {-5, -5}, {3, 7}, {1, 2}, {8, 10}, {-5, 0}}
	for l := 0; l < len(vals); l++ {
		for r := l; r < len(vals); r++ {
			for _, q := range queries {
				got, err := tr.RangeFreq(l, r, q[0], q[1])
				if err != nil {
					t.Fatalf("RangeFreq(%d,%d,%d,%d): %v", l, r, q[0], q[1], err)
				}
				if want := bruteFreq(vals, l, r, q[0], q[1]); got != want {
					t.Fatalf("RangeFreq(%d,%d,%d,%d) = %d, want %d", l, r, q[0], q[1], got, want)
				}
			}
		}
	}
	if _, err := tr.RangeFreq(3, 2, 0, 5); err != ErrEmpty {
		t.Errorf("empty interval err = %v, want ErrEmpty", err)
	}
	if _, err := tr.RangeFreq(0, 2, 5, 0); err == nil {
		t.Error("a>b should be rejected")
	}
}

func TestRandomizedAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		n := 1 + rng.Intn(200)
		vals := make([]int, n)
		for i := range vals {
			vals[i] = rng.Intn(41) - 20 // negatives + heavy duplicates
		}
		tr, err := New(vals)
		if err != nil {
			t.Fatal(err)
		}
		for q := 0; q < 50; q++ {
			l := rng.Intn(n)
			r := l + rng.Intn(n-l)
			k := 1 + rng.Intn(r-l+1)
			if got, _ := tr.Quantile(l, r, k); got != bruteKth(vals, l, r, k) {
				t.Fatalf("trial %d: Quantile(%d,%d,%d) = %d, want %d",
					trial, l, r, k, got, bruteKth(vals, l, r, k))
			}
			a := rng.Intn(41) - 20
			b := a + rng.Intn(20)
			if got, _ := tr.RangeFreq(l, r, a, b); got != bruteFreq(vals, l, r, a, b) {
				t.Fatalf("trial %d: RangeFreq(%d,%d,%d,%d) = %d, want %d",
					trial, l, r, a, b, got, bruteFreq(vals, l, r, a, b))
			}
			i := rng.Intn(n)
			if got, _ := tr.Access(i); got != vals[i] {
				t.Fatalf("trial %d: Access(%d) = %d, want %d", trial, i, got, vals[i])
			}
		}
	}
}

func TestAllEqualAndSingle(t *testing.T) {
	tr, err := New([]int{-7, -7, -7, -7})
	if err != nil {
		t.Fatal(err)
	}
	for k := 1; k <= 4; k++ {
		if got, err := tr.Quantile(0, 3, k); err != nil || got != -7 {
			t.Errorf("Quantile(0,3,%d) = %d, %v; want -7", k, got, err)
		}
	}
	if got, _ := tr.RangeFreq(0, 3, -7, -7); got != 4 {
		t.Errorf("RangeFreq = %d, want 4", got)
	}
	if got, _ := tr.RangeFreq(0, 3, 0, 5); got != 0 {
		t.Errorf("RangeFreq outside alphabet = %d, want 0", got)
	}
	one, err := New([]int{42})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := one.Access(0); got != 42 {
		t.Errorf("Access(0) = %d, want 42", got)
	}
	if _, err := New(nil); err == nil {
		t.Error("New(nil) should fail")
	}
}

func TestStatsLayoutConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	vals := make([]int, 1000)
	for i := range vals {
		vals[i] = rng.Intn(200) - 100
	}
	tr, err := New(vals)
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Stats()
	// every tree level stores exactly n bits
	if s.BitvectorBits != 1000*(s.Levels-1) {
		t.Errorf("BitvectorBits = %d, want %d", s.BitvectorBits, 1000*(s.Levels-1))
	}
	// packed words must cover the used bits
	if s.BitvectorBytes*8 < s.BitvectorBits {
		t.Errorf("bitvector bytes %d cannot hold %d bits", s.BitvectorBytes, s.BitvectorBits)
	}
	if s.Total() <= 0 {
		t.Error("Total() must be positive")
	}
	t.Logf("layout: %+v total=%d bytes (raw int64 slice would be %d bytes)",
		s, s.Total(), 8*len(vals))
}
