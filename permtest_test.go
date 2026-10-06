package permtest

import (
	"math/big"
	"math/rand"
	"testing"
)

// bruteForceTwoSample is an independent reference implementation: recursive
// combination generation, float64 arithmetic, epsilon comparison.
func bruteForceTwoSample(x, y []float64) (tail, total int64) {
	nx, ny := len(x), len(y)
	n := nx + ny
	pool := make([]float64, 0, n)
	pool = append(pool, x...)
	pool = append(pool, y...)
	var sum float64
	for _, v := range pool {
		sum += v
	}
	var sx float64
	for _, v := range x {
		sx += v
	}
	obs := sx/float64(nx) - (sum-sx)/float64(ny)
	if obs < 0 {
		obs = -obs
	}
	const eps = 1e-9
	chosen := make([]bool, n)
	var rec func(pos, left int, acc float64)
	rec = func(pos, left int, acc float64) {
		if left == 0 {
			d := acc/float64(nx) - (sum-acc)/float64(ny)
			if d < 0 {
				d = -d
			}
			if d >= obs-eps {
				tail++
			}
			total++
			return
		}
		if n-pos < left {
			return
		}
		for i := pos; i < n; i++ {
			if !chosen[i] {
				chosen[i] = true
				rec(i+1, left-1, acc+pool[i])
				chosen[i] = false
			}
		}
	}
	rec(0, nx, 0)
	return tail, total
}

func checkTwoSample(t *testing.T, x, y []float64, wantTail, wantTotal int64, wantP string) {
	t.Helper()
	res, err := TwoSampleExact(x, y)
	if err != nil {
		t.Fatalf("TwoSampleExact(%v, %v): %v", x, y, err)
	}
	if res.Tail.Int64() != wantTail || res.Total.Int64() != wantTotal {
		t.Errorf("TwoSampleExact(%v, %v) tail/total = %v/%v, want %d/%d",
			x, y, res.Tail, res.Total, wantTail, wantTotal)
	}
	if got := res.PValue.RatString(); got != wantP {
		t.Errorf("TwoSampleExact(%v, %v) p = %v, want %v", x, y, got, wantP)
	}
	num, den := res.NumDen()
	reduced := new(big.Rat).SetFrac(num, den)
	if reduced.Cmp(res.PValue) != 0 {
		t.Errorf("NumDen %v/%v inconsistent with PValue %v", num, den, res.PValue)
	}
}

func TestTwoSampleDistinct(t *testing.T) {
	// Splits of {1,2,3,4}: diffs -2,-1,0,0,1,2 -> |d|>=2 twice.
	checkTwoSample(t, []float64{1, 2}, []float64{3, 4}, 2, 6, "1/3")
}

func TestTwoSampleDuplicatesByPosition(t *testing.T) {
	// x={1,1}, y={1,2}: every one of the 6 positional splits yields
	// |diff| = 1/2, equal to the observed one. Deduplicating values would
	// give a different answer.
	checkTwoSample(t, []float64{1, 1}, []float64{1, 2}, 6, 6, "1")
}

func TestTwoSampleNegatives(t *testing.T) {
	// Splits of {-2,-1,1,3}: diffs -3.5,-1.5,-0.5,0.5,1.5,3.5.
	checkTwoSample(t, []float64{-1, -2}, []float64{1, 3}, 2, 6, "1/3")
}

func TestTwoSampleUnequalSizes(t *testing.T) {
	// Splits of {5,1,2} with one sample in x: diffs 3.5,-2.5,-1.
	checkTwoSample(t, []float64{5}, []float64{1, 2}, 1, 3, "1/3")
}

func TestTwoSampleReducedFraction(t *testing.T) {
	// x={1,2,4}, y={3,5,6}: obs diff = 7/3-14/3 = -7/3. 20 splits; the
	// tail count must reduce, e.g. 4/20 -> 1/5.
	res, err := TwoSampleExact([]float64{1, 2, 4}, []float64{3, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total.Int64() != 20 {
		t.Fatalf("total = %v, want 20", res.Total)
	}
	tail, total := bruteForceTwoSample([]float64{1, 2, 4}, []float64{3, 5, 6})
	if res.Tail.Int64() != tail || total != 20 {
		t.Fatalf("tail = %v, want %v", res.Tail, tail)
	}
	num, den := res.NumDen()
	g := new(big.Int).GCD(nil, nil, res.Tail, res.Total)
	if new(big.Int).Quo(res.Tail, g).Cmp(num) != 0 || new(big.Int).Quo(res.Total, g).Cmp(den) != 0 {
		t.Errorf("NumDen = %v/%v, want reduced %v/%v", num, den,
			new(big.Int).Quo(res.Tail, g), new(big.Int).Quo(res.Total, g))
	}
}

func TestTwoSampleCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		nx := 1 + rng.Intn(5)
		ny := 1 + rng.Intn(5)
		x := make([]float64, nx)
		y := make([]float64, ny)
		for i := range x {
			x[i] = float64(rng.Intn(7) - 3) // duplicates and negatives
		}
		for j := range y {
			y[j] = float64(rng.Intn(7) - 3)
		}
		res, err := TwoSampleExact(x, y)
		if err != nil {
			t.Fatal(err)
		}
		tail, total := bruteForceTwoSample(x, y)
		if res.Tail.Int64() != tail || res.Total.Int64() != total {
			t.Errorf("x=%v y=%v: got %v/%v, want %d/%d", x, y, res.Tail, res.Total, tail, total)
		}
	}
}

func TestTwoSampleMaxSize(t *testing.T) {
	// 12 vs 12 -> C(24,12) = 2704156 splits.
	x := []float64{0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5}
	y := []float64{0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 6}
	res, err := TwoSampleExact(x, y)
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Binomial(24, 12)
	if res.Total.Cmp(want) != 0 {
		t.Fatalf("total = %v, want %v", res.Total, want)
	}
	if res.Tail.Sign() <= 0 || res.Tail.Cmp(res.Total) > 0 {
		t.Fatalf("implausible tail %v of %v", res.Tail, res.Total)
	}
}

func TestTwoSampleErrors(t *testing.T) {
	if _, err := TwoSampleExact(nil, []float64{1}); err == nil {
		t.Error("empty x: expected error")
	}
	if _, err := TwoSampleExact([]float64{1}, nil); err == nil {
		t.Error("empty y: expected error")
	}
	big13 := make([]float64, MaxGroupSize+1)
	if _, err := TwoSampleExact(big13, []float64{1}); err == nil {
		t.Error("oversized x: expected error")
	}
}
