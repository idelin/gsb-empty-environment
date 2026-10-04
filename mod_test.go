package binom

import (
	"math/big"
	"testing"
)

func mustBig(t *testing.T, s string) *big.Int {
	t.Helper()
	m, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad integer literal %q", s)
	}
	return m
}

// checkMod verifies Mod(n, k, m) against the exact big-integer result.
func checkMod(t *testing.T, c *Calculator, n, k int64, m *big.Int) {
	t.Helper()
	var got *big.Int
	var err error
	if c != nil {
		got, err = c.Mod(n, k, m)
	} else {
		got, err = Mod(n, k, m)
	}
	if err != nil {
		t.Fatalf("Mod(%d, %d, %v): %v", n, k, m, err)
	}
	want := new(big.Int).Mod(exact(n, k), m)
	if got.Cmp(want) != 0 {
		t.Fatalf("Mod(%d, %d, %v) = %v, want %v", n, k, m, got, want)
	}
}

func TestModAgainstExactSmall(t *testing.T) {
	moduli := []string{
		"1", "2", "3", "4", "6", "97", "360",
		"1024",          // 2^10
		"2187",          // 3^7
		"1000000007",    // large prime
		"2147483647",    // Mersenne prime
		"1000036000099", // 1000003 * 1000033, semiprime
		"3240000",       // 2^6 * 3^4 * 5^4 * ... mixed
	}
	for _, ms := range moduli {
		m := mustBig(t, ms)
		for n := int64(0); n <= 45; n++ {
			for k := int64(-1); k <= n+1; k++ {
				checkMod(t, nil, n, k, m)
			}
		}
	}
}

func TestHighPrimePowerModuli(t *testing.T) {
	pow := func(p, e int64) *big.Int {
		return new(big.Int).Exp(big.NewInt(p), big.NewInt(e), nil)
	}
	moduli := []*big.Int{
		pow(2, 40),
		pow(3, 25),
		pow(5, 17),
		pow(7, 13),
		new(big.Int).Mul(pow(7, 13), pow(11, 9)), // ~2.3e20, exceeds int64
		new(big.Int).Mul(pow(2, 50), pow(3, 30)),
	}
	for _, m := range moduli {
		for n := int64(0); n <= 300; n += 7 {
			for _, k := range []int64{0, 1, n / 3, n / 2, n - 1, n} {
				checkMod(t, nil, n, k, m)
			}
		}
	}
}

func TestModLargerN(t *testing.T) {
	m := new(big.Int).Mul(
		new(big.Int).Exp(big.NewInt(2), big.NewInt(40), nil),
		new(big.Int).Exp(big.NewInt(3), big.NewInt(25), nil),
	)
	for _, nk := range [][2]int64{
		{1000, 500}, {5000, 2500}, {20000, 10000}, {20000, 3}, {20000, 19999},
	} {
		checkMod(t, nil, nk[0], nk[1], m)
	}
}

func TestModBatchSharesFactorization(t *testing.T) {
	c := NewCalculator()
	m := mustBig(t, "1000036000099") // 1000003 * 1000033

	// Budget too small to factor the semiprime: batch must fail.
	c.Budget = 10
	qs := []Query{{N: 40, K: 17}, {N: 0, K: 0}, {N: 100, K: 50}, {N: 25, K: 30}}
	if _, err := c.ModBatch(qs, m); err != ErrFactorBudgetExceeded {
		t.Fatalf("ModBatch err = %v, want ErrFactorBudgetExceeded", err)
	}

	// Unlimited budget factors once and caches the result.
	c.Budget = -1
	got, err := c.ModBatch(qs, m)
	if err != nil {
		t.Fatal(err)
	}
	for i, q := range qs {
		want := new(big.Int).Mod(exact(q.N, q.K), m)
		if got[i].Cmp(want) != 0 {
			t.Fatalf("batch[%d] C(%d,%d) mod m = %v, want %v", i, q.N, q.K, got[i], want)
		}
	}

	// With the factorization cached, even a zero budget now succeeds.
	c.Budget = 0
	got2, err := c.ModBatch(qs, m)
	if err != nil {
		t.Fatalf("cached ModBatch: %v", err)
	}
	for i := range got {
		if got[i].Cmp(got2[i]) != 0 {
			t.Fatalf("cached batch[%d] = %v, want %v", i, got2[i], got[i])
		}
	}
}

func TestModBatchValidation(t *testing.T) {
	c := NewCalculator()
	if _, err := c.ModBatch([]Query{{N: -1, K: 0}}, big.NewInt(7)); err != ErrNegativeN {
		t.Fatalf("err = %v, want ErrNegativeN", err)
	}
	if _, err := c.ModBatch([]Query{{N: 1, K: 0}}, big.NewInt(0)); err != ErrNonPositiveModulus {
		t.Fatalf("err = %v, want ErrNonPositiveModulus", err)
	}
	// Modulus 1 short-circuits to zeros without factorization.
	got, err := c.ModBatch([]Query{{N: 5, K: 2}, {N: 9, K: 9}}, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range got {
		if r.Sign() != 0 {
			t.Fatalf("got[%d] = %v, want 0", i, r)
		}
	}
}
