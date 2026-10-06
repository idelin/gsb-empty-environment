package isotonic

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

func obs(value *big.Rat, weight int64) Observation {
	return Observation{Value: value, Weight: big.NewInt(weight)}
}

// bruteForce enumerates every contiguous partition of the
// observations, keeps the feasible ones (block means non-decreasing),
// and returns the minimum loss together with the optimal per-point
// fitted values. The optimum is unique because the loss is strictly
// convex in the fitted values.
func bruteForce(o []Observation) (bestLoss *big.Rat, bestValues []*big.Rat) {
	n := len(o)
	for mask := 0; mask < 1<<uint(n-1); mask++ {
		// Block boundaries: cut after i iff bit i of mask is set.
		var means []*big.Rat
		var blocks [][2]int
		start := 0
		for i := 0; i < n; i++ {
			if i == n-1 || mask&(1<<uint(i)) != 0 {
				w := new(big.Int)
				s := new(big.Rat)
				for j := start; j <= i; j++ {
					w.Add(w, o[j].Weight)
					s.Add(s, new(big.Rat).Mul(o[j].Value, new(big.Rat).SetInt(o[j].Weight)))
				}
				means = append(means, new(big.Rat).Quo(s, new(big.Rat).SetInt(w)))
				blocks = append(blocks, [2]int{start, i})
				start = i + 1
			}
		}
		feasible := true
		for i := 1; i < len(means); i++ {
			if means[i-1].Cmp(means[i]) > 0 {
				feasible = false
				break
			}
		}
		if !feasible {
			continue
		}
		values := make([]*big.Rat, n)
		for b, m := range means {
			for i := blocks[b][0]; i <= blocks[b][1]; i++ {
				values[i] = m
			}
		}
		loss, err := Loss(o, values)
		if err != nil {
			panic(err)
		}
		if bestLoss == nil || loss.Cmp(bestLoss) < 0 {
			bestLoss = loss
			bestValues = values
		}
	}
	return bestLoss, bestValues
}

// checkAgainstBruteForce verifies the exact optimum and the block
// structure invariants for one instance.
func checkAgainstBruteForce(t *testing.T, o []Observation) {
	t.Helper()
	blocks, err := Fit(o)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}

	// Blocks tile [0, n-1] contiguously.
	if len(blocks) == 0 {
		t.Fatal("no blocks")
	}
	if blocks[0].Start != 0 || blocks[len(blocks)-1].End != len(o)-1 {
		t.Fatalf("blocks do not cover [0,%d]: %+v", len(o)-1, blocks)
	}
	for i := 1; i < len(blocks); i++ {
		if blocks[i].Start != blocks[i-1].End+1 {
			t.Fatalf("blocks not contiguous: %+v", blocks)
		}
		// Adjacent blocks must be strictly increasing (equal-valued
		// runs merged).
		if blocks[i-1].Value.Cmp(blocks[i].Value) >= 0 {
			t.Fatalf("adjacent blocks not strictly increasing: %+v", blocks)
		}
	}

	gotLoss, err := Loss(o, Values(blocks))
	if err != nil {
		t.Fatalf("Loss: %v", err)
	}
	wantLoss, wantValues := bruteForce(o)
	if gotLoss.Cmp(wantLoss) != 0 {
		t.Fatalf("loss mismatch: got %s, want %s (obs=%v)", gotLoss, wantLoss, o)
	}
	gotValues := Values(blocks)
	for i := range gotValues {
		if gotValues[i].Cmp(wantValues[i]) != 0 {
			t.Fatalf("fitted value %d mismatch: got %s, want %s (obs=%v)",
				i, gotValues[i], wantValues[i], o)
		}
	}
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(8)
		o := make([]Observation, n)
		for i := range o {
			// Small numerators/denominators produce many ties and
			// exact rational merges.
			num := int64(rng.Intn(9) - 4)
			den := int64(1 + rng.Intn(3))
			w := int64(1 + rng.Intn(6))
			o[i] = obs(rat(num, den), w)
		}
		checkAgainstBruteForce(t, o)
	}
}

func TestStrictlyDecreasing(t *testing.T) {
	o := []Observation{
		obs(rat(5, 1), 1), obs(rat(4, 1), 1), obs(rat(3, 1), 1),
		obs(rat(2, 1), 1), obs(rat(1, 1), 1),
	}
	blocks, err := Fit(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %+v", blocks)
	}
	b := blocks[0]
	if b.Start != 0 || b.End != 4 || b.Value.Cmp(rat(3, 1)) != 0 {
		t.Fatalf("unexpected block %+v", b)
	}
	checkAgainstBruteForce(t, o)
}

func TestDuplicateValues(t *testing.T) {
	o := []Observation{
		obs(rat(1, 1), 1), obs(rat(1, 1), 2), obs(rat(1, 1), 1),
		obs(rat(2, 1), 1), obs(rat(2, 1), 3),
	}
	blocks, err := Fit(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %+v", blocks)
	}
	if blocks[0].Start != 0 || blocks[0].End != 2 || blocks[0].Value.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("unexpected first block %+v", blocks[0])
	}
	if blocks[1].Start != 3 || blocks[1].End != 4 || blocks[1].Value.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("unexpected second block %+v", blocks[1])
	}
	checkAgainstBruteForce(t, o)
}

// A strictly greater previous pool would leave the two trailing
// equal-valued blocks unmerged; the fit must merge them.
func TestEqualAdjacentBlocksMerged(t *testing.T) {
	o := []Observation{obs(rat(0, 1), 1), obs(rat(1, 1), 1), obs(rat(1, 1), 1)}
	blocks, err := Fit(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %+v", blocks)
	}
	if blocks[1].Start != 1 || blocks[1].End != 2 || blocks[1].Value.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("trailing equal values not merged: %+v", blocks[1])
	}
	checkAgainstBruteForce(t, o)
}

func TestWeightDisparity(t *testing.T) {
	// A huge weight pins the middle observation at 0; the first
	// observation is pulled into a block whose mean is dominated by
	// the heavy point.
	o := []Observation{
		obs(rat(10, 1), 1),
		obs(rat(0, 1), 1000),
		obs(rat(10, 1), 1),
	}
	blocks, err := Fit(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %+v", blocks)
	}
	if blocks[0].Start != 0 || blocks[0].End != 1 ||
		blocks[0].Value.Cmp(rat(10, 1001)) != 0 {
		t.Fatalf("unexpected first block %+v", blocks[0])
	}
	if blocks[1].Start != 2 || blocks[1].End != 2 ||
		blocks[1].Value.Cmp(rat(10, 1)) != 0 {
		t.Fatalf("unexpected second block %+v", blocks[1])
	}
	checkAgainstBruteForce(t, o)
}

func TestReducedRationalOutput(t *testing.T) {
	// Weighted mean (3*(1/3) + 1*0) / 4 = 1/4, already lowest terms.
	o := []Observation{obs(rat(1, 3), 3), obs(rat(0, 1), 1)}
	blocks, err := Fit(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Value.Cmp(rat(1, 4)) != 0 {
		t.Fatalf("unexpected blocks %+v", blocks)
	}
	if got := blocks[0].Value.RatString(); got != "1/4" {
		t.Fatalf("value not in lowest terms: %s", got)
	}
	checkAgainstBruteForce(t, o)
}

func TestInvalidInput(t *testing.T) {
	if _, err := Fit([]Observation{obs(rat(1, 1), 0)}); err == nil {
		t.Fatal("expected error for zero weight")
	}
	if _, err := Fit([]Observation{obs(rat(1, 1), -2)}); err == nil {
		t.Fatal("expected error for negative weight")
	}
	if _, err := Fit([]Observation{{Value: nil, Weight: big.NewInt(1)}}); err == nil {
		t.Fatal("expected error for nil value")
	}
	blocks, err := Fit(nil)
	if err != nil || len(blocks) != 0 {
		t.Fatalf("empty input: blocks=%v err=%v", blocks, err)
	}
}
