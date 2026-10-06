package isotonic

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

func ints(vals ...int64) []*big.Rat {
	out := make([]*big.Rat, len(vals))
	for i, v := range vals {
		out[i] = big.NewRat(v, 1)
	}
	return out
}

func weights(vals ...int64) []*big.Int {
	out := make([]*big.Int, len(vals))
	for i, v := range vals {
		out[i] = big.NewInt(v)
	}
	return out
}

// checkStructure verifies the block invariants: contiguous coverage
// of [0,n), strictly increasing values (equal adjacents merged), and
// each block value equal to the weighted mean of its range.
func checkStructure(t *testing.T, y []*big.Rat, w []*big.Int, blocks []Block) {
	t.Helper()
	if len(blocks) == 0 {
		t.Fatal("no blocks")
	}
	if blocks[0].Start != 0 || blocks[len(blocks)-1].End != len(y) {
		t.Fatalf("blocks do not cover [0,%d): %+v", len(y), blocks)
	}
	for i, b := range blocks {
		if b.Start >= b.End {
			t.Fatalf("empty block %+v", b)
		}
		if i+1 < len(blocks) {
			if blocks[i+1].Start != b.End {
				t.Fatalf("gap between blocks %d and %d", i, i+1)
			}
			if blocks[i+1].Value.Cmp(b.Value) <= 0 {
				t.Fatalf("values not strictly increasing: %v then %v",
					b.Value, blocks[i+1].Value)
			}
		}
		sumW := new(big.Rat)
		sumWY := new(big.Rat)
		for j := b.Start; j < b.End; j++ {
			wj := new(big.Rat).SetInt(w[j])
			sumW.Add(sumW, wj)
			sumWY.Add(sumWY, new(big.Rat).Mul(wj, y[j]))
		}
		if mean := new(big.Rat).Quo(sumWY, sumW); mean.Cmp(b.Value) != 0 {
			t.Fatalf("block %v value %v != weighted mean %v", b, b.Value, mean)
		}
	}
}

// bruteForce enumerates all 2^(n-1) contiguous partitions and returns
// the minimum achievable loss and one optimal fitted-value vector.
func bruteForce(y []*big.Rat, w []*big.Int) (*big.Rat, []*big.Rat) {
	n := len(y)
	var bestLoss *big.Rat
	var bestVals []*big.Rat
	for mask := 0; mask < 1<<uint(n-1); mask++ {
		var blocks []Block
		start := 0
		for i := 0; i < n; i++ {
			if i == n-1 || mask&(1<<uint(i)) != 0 {
				sumW := new(big.Rat)
				sumWY := new(big.Rat)
				for j := start; j <= i; j++ {
					wj := new(big.Rat).SetInt(w[j])
					sumW.Add(sumW, wj)
					sumWY.Add(sumWY, new(big.Rat).Mul(wj, y[j]))
				}
				blocks = append(blocks, Block{
					Value: new(big.Rat).Quo(sumWY, sumW),
					Start: start,
					End:   i + 1,
				})
				start = i + 1
			}
		}
		// Keep only non-decreasing fits (the isotonic constraint).
		feasible := true
		for k := 1; k < len(blocks); k++ {
			if blocks[k].Value.Cmp(blocks[k-1].Value) < 0 {
				feasible = false
				break
			}
		}
		if !feasible {
			continue
		}
		loss := Loss(y, w, blocks)
		if bestLoss == nil || loss.Cmp(bestLoss) < 0 {
			bestLoss = loss
			bestVals = Values(n, blocks)
		}
	}
	return bestLoss, bestVals
}

func checkAgainstBruteForce(t *testing.T, y []*big.Rat, w []*big.Int) {
	t.Helper()
	blocks, err := Fit(y, w)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	checkStructure(t, y, w, blocks)

	gotLoss := Loss(y, w, blocks)
	wantLoss, wantVals := bruteForce(y, w)
	if gotLoss.Cmp(wantLoss) != 0 {
		t.Fatalf("loss mismatch: got %v, optimal %v (y=%v w=%v)",
			gotLoss, wantLoss, y, w)
	}
	// Weighted LS is strictly convex, so fitted values are unique.
	gotVals := Values(len(y), blocks)
	for i := range gotVals {
		if gotVals[i].Cmp(wantVals[i]) != 0 {
			t.Fatalf("fitted value at %d: got %v, want %v", i, gotVals[i], wantVals[i])
		}
	}
}

func TestStrictlyDecreasing(t *testing.T) {
	y := ints(5, 4, 3, 2, 1)
	w := weights(1, 1, 1, 1, 1)
	blocks, err := Fit(y, w)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Start != 0 || blocks[0].End != 5 ||
		blocks[0].Value.Cmp(rat(3, 1)) != 0 {
		t.Fatalf("got %+v, want single block value 3 over [0,5)", blocks)
	}
	checkAgainstBruteForce(t, y, w)
}

func TestRepeatedValues(t *testing.T) {
	y := ints(2, 2, 1, 1, 3, 3)
	w := weights(1, 1, 1, 1, 1, 1)
	blocks, err := Fit(y, w)
	if err != nil {
		t.Fatal(err)
	}
	// PAVA pools the 2,2,1,1 prefix into 3/2; trailing 3,3 stays.
	want := []Block{
		{Value: rat(3, 2), Start: 0, End: 4},
		{Value: rat(3, 1), Start: 4, End: 6},
	}
	if len(blocks) != len(want) {
		t.Fatalf("got %+v, want %+v", blocks, want)
	}
	for i := range want {
		if blocks[i].Value.Cmp(want[i].Value) != 0 ||
			blocks[i].Start != want[i].Start || blocks[i].End != want[i].End {
			t.Fatalf("got %+v, want %+v", blocks, want)
		}
	}
	checkAgainstBruteForce(t, y, w)
}

func TestAdjacentEqualBlocksMerged(t *testing.T) {
	// Naive PAVA would emit [2,2,2] as three blocks; they must merge.
	y := ints(3, 1, 2, 2)
	w := weights(1, 1, 1, 1)
	blocks, err := Fit(y, w)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Value.Cmp(rat(2, 1)) != 0 ||
		blocks[0].Start != 0 || blocks[0].End != 4 {
		t.Fatalf("got %+v, want single merged block value 2 over [0,4)", blocks)
	}
	checkAgainstBruteForce(t, y, w)
}

func TestExtremeWeights(t *testing.T) {
	y := ints(2, 0, 1)
	w := weights(1, 1000000, 1)
	blocks, err := Fit(y, w)
	if err != nil {
		t.Fatal(err)
	}
	// The huge weight on 0 drags the pooled mean to 2/1000001.
	want := []Block{
		{Value: rat(2, 1000001), Start: 0, End: 2},
		{Value: rat(1, 1), Start: 2, End: 3},
	}
	if len(blocks) != len(want) {
		t.Fatalf("got %+v, want %+v", blocks, want)
	}
	for i := range want {
		if blocks[i].Value.Cmp(want[i].Value) != 0 ||
			blocks[i].Start != want[i].Start || blocks[i].End != want[i].End {
			t.Fatalf("got %+v, want %+v", blocks, want)
		}
	}
	checkAgainstBruteForce(t, y, w)
}

func TestRationalObservations(t *testing.T) {
	y := []*big.Rat{rat(7, 3), rat(-1, 2), rat(5, 6), rat(0, 1)}
	w := weights(2, 3, 5, 7)
	blocks, err := Fit(y, w)
	if err != nil {
		t.Fatal(err)
	}
	checkStructure(t, y, w, blocks)
	checkAgainstBruteForce(t, y, w)
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(8) // 1..8 observations
		y := make([]*big.Rat, n)
		w := make([]*big.Int, n)
		for i := range y {
			y[i] = big.NewRat(int64(rng.Intn(21)-10), int64(1+rng.Intn(5)))
			if trial%3 == 0 {
				// Extreme weight disparities.
				if rng.Intn(2) == 0 {
					w[i] = big.NewInt(1)
				} else {
					w[i] = big.NewInt(1000000)
				}
			} else {
				w[i] = big.NewInt(int64(1 + rng.Intn(5)))
			}
		}
		checkAgainstBruteForce(t, y, w)
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := Fit(nil, nil); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := Fit(ints(1, 2), weights(1)); err == nil {
		t.Fatal("expected error for length mismatch")
	}
	if _, err := Fit(ints(1, 2), weights(1, 0)); err == nil {
		t.Fatal("expected error for non-positive weight")
	}
}
