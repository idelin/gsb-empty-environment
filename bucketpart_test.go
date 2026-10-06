package bucketpart

import (
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// bruteForce enumerates every composition of n samples into b = 1..min(k,n)
// consecutive non-empty buckets and applies the same tie-breaking rules as
// Partition: minimal cost, then fewer buckets, then lexicographic starts.
func bruteForce(samples []Sample, k int) ([]int, *big.Int) {
	n := len(samples)
	if n == 0 {
		return nil, new(big.Int)
	}
	if k > n {
		k = n
	}
	var bestPath []int
	var bestCost *big.Int
	for b := 1; b <= k; b++ {
		path := make([]int, b)
		var rec func(idx, lo int)
		rec = func(idx, lo int) {
			if idx == b {
				c := totalCost(samples, path)
				if bestCost == nil || c.Cmp(bestCost) < 0 ||
					(c.Cmp(bestCost) == 0 && b == len(bestPath) && lessPath(path, bestPath)) {
					bestCost = c
					bestPath = append([]int(nil), path...)
				}
				return
			}
			for v := lo; v <= n-(b-idx); v++ {
				path[idx] = v
				rec(idx+1, v+1)
			}
		}
		path[0] = 0
		rec(1, 1)
	}
	return bestPath, bestCost
}

// totalCost sums the per-bucket costs implied by the given start indices.
func totalCost(samples []Sample, starts []int) *big.Int {
	total := new(big.Int)
	for t := 0; t < len(starts); t++ {
		hi := len(samples)
		if t+1 < len(starts) {
			hi = starts[t+1]
		}
		total.Add(total, directBucketCost(samples, starts[t], hi))
	}
	return total
}

// directBucketCost is an independent, naive implementation: it scans for the
// lower weighted median and sums weighted absolute deviations element by
// element, without prefix sums.
func directBucketCost(samples []Sample, lo, hi int) *big.Int {
	w := new(big.Int)
	for t := lo; t < hi; t++ {
		w.Add(w, big.NewInt(samples[t].Weight))
	}
	if w.Sign() == 0 {
		return new(big.Int)
	}
	threshold := new(big.Int).Rsh(new(big.Int).Add(w, big.NewInt(1)), 1)
	cum := new(big.Int)
	var rep int64
	for t := lo; t < hi; t++ {
		cum.Add(cum, big.NewInt(samples[t].Weight))
		if cum.Cmp(threshold) >= 0 {
			rep = samples[t].Value
			break
		}
	}
	r := big.NewInt(rep)
	sum := new(big.Int)
	for t := lo; t < hi; t++ {
		d := new(big.Int).Sub(big.NewInt(samples[t].Value), r)
		d.Abs(d)
		d.Mul(d, big.NewInt(samples[t].Weight))
		sum.Add(sum, d)
	}
	return sum
}

func checkAgainstBrute(t *testing.T, samples []Sample, k int) ([]int, *big.Int) {
	t.Helper()
	gotPath, gotCost, err := Partition(samples, k)
	if err != nil {
		t.Fatalf("Partition(%v, %d) error: %v", samples, k, err)
	}
	wantPath, wantCost := bruteForce(samples, k)
	if gotCost.Cmp(wantCost) != 0 {
		t.Fatalf("Partition(%v, %d) cost = %v, brute force = %v", samples, k, gotCost, wantCost)
	}
	if !reflect.DeepEqual(gotPath, wantPath) {
		t.Fatalf("Partition(%v, %d) path = %v, brute force = %v", samples, k, gotPath, wantPath)
	}
	return gotPath, gotCost
}

func TestEvenWeightChoosesSmallerRepresentative(t *testing.T) {
	// Bucket {0,10} with equal weights: both 0 and 10 give cost 10; the
	// smaller representative must be chosen.
	path, cost := checkAgainstBrute(t, []Sample{{0, 1}, {10, 1}}, 1)
	if !reflect.DeepEqual(path, []int{0}) || cost.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("got path %v cost %v, want [0] and 10", path, cost)
	}
	// Same with doubled weights (even total, zero-weight gap in the middle).
	path, cost = checkAgainstBrute(t, []Sample{{0, 2}, {5, 0}, {10, 2}}, 1)
	if cost.Cmp(big.NewInt(20)) != 0 || !reflect.DeepEqual(path, []int{0}) {
		t.Fatalf("got path %v cost %v, want [0] and 20", path, cost)
	}
}

func TestRepeatedValues(t *testing.T) {
	// All values equal: every partition has cost 0, so a single bucket wins.
	path, cost := checkAgainstBrute(t, []Sample{{5, 2}, {5, 1}, {5, 3}, {5, 1}}, 3)
	if !reflect.DeepEqual(path, []int{0}) || cost.Sign() != 0 {
		t.Fatalf("got path %v cost %v, want [0] and 0", path, cost)
	}
	checkAgainstBrute(t, []Sample{{2, 1}, {2, 2}, {7, 1}, {7, 1}, {7, 4}, {9, 1}}, 3)
	checkAgainstBrute(t, []Sample{{1, 3}, {1, 0}, {1, 2}, {2, 0}, {2, 1}}, 4)
}

func TestZeroWeights(t *testing.T) {
	// All weights zero: cost is always 0, one bucket is preferred.
	path, cost := checkAgainstBrute(t, []Sample{{1, 0}, {2, 0}, {3, 0}}, 2)
	if !reflect.DeepEqual(path, []int{0}) || cost.Sign() != 0 {
		t.Fatalf("got path %v cost %v, want [0] and 0", path, cost)
	}
	checkAgainstBrute(t, []Sample{{0, 0}, {5, 2}, {9, 0}, {10, 2}}, 2)
	checkAgainstBrute(t, []Sample{{4, 0}, {4, 0}, {4, 0}, {9, 1}}, 3)
	// Zero-weight samples between weighted ones must not shift the median.
	checkAgainstBrute(t, []Sample{{1, 5}, {2, 0}, {3, 0}, {8, 1}, {9, 4}}, 2)
}

func TestLargeIntegerCosts(t *testing.T) {
	// Cost 2^30 * 2^63 = 2^93 overflows int64; big.Int must carry it.
	samples := []Sample{{-(1 << 62), 1 << 30}, {1 << 62, 1 << 30}}
	path, cost := checkAgainstBrute(t, samples, 1)
	want := new(big.Int).Lsh(big.NewInt(1), 93)
	if !reflect.DeepEqual(path, []int{0}) || cost.Cmp(want) != 0 {
		t.Fatalf("got path %v cost %v, want [0] and %v", path, cost, want)
	}
	// Mixed huge magnitudes with several buckets.
	checkAgainstBrute(t, []Sample{
		{-(1 << 60), 1 << 20}, {-7, 1 << 18}, {0, 0},
		{1 << 59, 3}, {1 << 61, 1 << 22},
	}, 3)
}

func TestFewerBucketsPreferred(t *testing.T) {
	// k=3 but a single bucket already reaches cost 0.
	path, cost := checkAgainstBrute(t, []Sample{{7, 1}, {7, 1}, {7, 1}}, 3)
	if !reflect.DeepEqual(path, []int{0}) || cost.Sign() != 0 {
		t.Fatalf("got path %v cost %v, want [0] and 0", path, cost)
	}
}

func TestLexicographicBoundaries(t *testing.T) {
	// Two buckets both reach cost 0 via cuts [0 1] and [0 2]; the
	// lexicographically smaller boundary sequence must win.
	path, cost := checkAgainstBrute(t, []Sample{{0, 1}, {5, 0}, {10, 1}}, 2)
	if !reflect.DeepEqual(path, []int{0, 1}) || cost.Sign() != 0 {
		t.Fatalf("got path %v cost %v, want [0 1] and 0", path, cost)
	}
}

func TestInputValidation(t *testing.T) {
	if _, _, err := Partition([]Sample{{1, 1}}, 0); err != ErrBadK {
		t.Fatalf("k=0: got %v, want ErrBadK", err)
	}
	if _, _, err := Partition([]Sample{{2, 1}, {1, 1}}, 1); err != ErrNotOrdered {
		t.Fatalf("unsorted: got %v, want ErrNotOrdered", err)
	}
	if _, _, err := Partition([]Sample{{1, -1}}, 1); err != ErrNegativeWeight {
		t.Fatalf("negative weight: got %v, want ErrNegativeWeight", err)
	}
	path, cost, err := Partition(nil, 3)
	if err != nil || path != nil || cost.Sign() != 0 {
		t.Fatalf("empty input: got %v %v %v", path, cost, err)
	}
	// k larger than n is clamped: each sample becomes its own bucket.
	path, cost = checkAgainstBrute(t, []Sample{{1, 1}, {5, 1}, {9, 1}}, 10)
	if !reflect.DeepEqual(path, []int{0, 1, 2}) || cost.Sign() != 0 {
		t.Fatalf("got path %v cost %v, want [0 1 2] and 0", path, cost)
	}
}

func TestAgainstBruteForceFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(9)
		samples := make([]Sample, n)
		prev := int64(-3)
		for i := range samples {
			// Small value pool with duplicates; occasional huge jumps.
			v := prev + int64(rng.Intn(4))
			if rng.Intn(40) == 0 {
				v += 1 << 50
			}
			w := int64(rng.Intn(5)) // includes zero weights
			if rng.Intn(30) == 0 {
				w = 1 << 40
			}
			samples[i] = Sample{Value: v, Weight: w}
			prev = v
		}
		k := 1 + rng.Intn(n+2)
		checkAgainstBrute(t, samples, k)
	}
}
