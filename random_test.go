package sparselu

import (
	"math/big"
	"math/rand"
	"testing"
)

// TestRandomMatricesCrossCheck factors random sparse rational matrices of
// varying shapes and densities and verifies PAQ == LU for each.
func TestRandomMatricesCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		rows := 1 + rng.Intn(5)
		cols := 1 + rng.Intn(5)
		a := NewMatrix(rows, cols)
		for i := 0; i < rows; i++ {
			for j := 0; j < cols; j++ {
				if rng.Float64() < 0.55 {
					num := int64(rng.Intn(7) - 3)
					if num == 0 {
						continue
					}
					den := int64(1 + rng.Intn(4))
					a.Set(i, j, big.NewRat(num, den))
				}
			}
		}
		res := Factorize(a)
		checkFactorization(t, a, res)
		// A budget of one elimination at a time must give the same result.
		f := NewFactorizer(a)
		for !f.Done() {
			if err := f.Run(1); err != nil && err != ErrBudgetExhausted {
				t.Fatalf("trial %d: %v", trial, err)
			}
		}
		got := f.Result()
		if got.Rank != res.Rank || !Equal(got.L, res.L) || !Equal(got.U, res.U) {
			t.Fatalf("trial %d: budgeted run differs from one-shot", trial)
		}
	}
}
