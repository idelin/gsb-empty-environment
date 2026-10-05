package binom

import (
	"math/big"
	"testing"
)

const testBudget = uint64(1) << 24

func TestExact(t *testing.T) {
	cases := []struct {
		n, k int64
		want string
	}{
		{0, 0, "1"},
		{1, 0, "1"},
		{1, 1, "1"},
		{5, 2, "10"},
		{10, 3, "120"},
		{20, 10, "184756"},
		{100, 50, "100891344545564193334812497256"},
	}
	for _, c := range cases {
		got := Exact(c.n, c.k)
		want, _ := new(big.Int).SetString(c.want, 10)
		if got.Cmp(want) != 0 {
			t.Errorf("Exact(%d, %d) = %s, want %s", c.n, c.k, got, want)
		}
	}
}

func TestExactOutOfRange(t *testing.T) {
	for _, nk := range [][2]int64{{5, 6}, {5, -1}, {0, 1}, {0, -3}, {-4, 2}} {
		if got := Exact(nk[0], nk[1]); got.Sign() != 0 {
			t.Errorf("Exact(%d, %d) = %s, want 0", nk[0], nk[1], got)
		}
	}
}

func TestModAgainstExact(t *testing.T) {
	moduli := []uint64{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 16, 25, 27, 36, 97, 100,
		128, 243, 1000, 1024, 2187, 1 << 20, 531441, 390625, 16807,
		999983, 1_000_000_007, 1 << 31, 1 << 40, 1 << 62,
		2 * 3 * 5 * 7 * 11 * 13, 1 << 10 * 729 * 625,
	}
	calc := NewCalculator(testBudget)
	for n := int64(0); n <= 60; n++ {
		for k := int64(-1); k <= n+1; k++ {
			exact := Exact(n, k)
			for _, m := range moduli {
				got, err := calc.Mod(n, k, m)
				if err != nil {
					t.Fatalf("Mod(%d, %d, %d) error: %v", n, k, m, err)
				}
				want := new(big.Int).Mod(exact, new(big.Int).SetUint64(m)).Uint64()
				if got != want {
					t.Fatalf("Mod(%d, %d, %d) = %d, want %d", n, k, m, got, want)
				}
			}
		}
	}
}

func TestModEdgeCases(t *testing.T) {
	calc := NewCalculator(testBudget)

	for _, m := range []uint64{1, 2, 7, 1 << 40} {
		got, err := calc.Mod(0, 0, m)
		if err != nil {
			t.Fatalf("Mod(0, 0, %d) error: %v", m, err)
		}
		if want := uint64(1) % m; got != want {
			t.Errorf("Mod(0, 0, %d) = %d, want %d", m, got, want)
		}
	}

	for _, m := range []uint64{1, 6, 1 << 30, 999983} {
		got, err := calc.Mod(12345, 678, m)
		if err != nil {
			t.Fatalf("Mod(12345, 678, %d) error: %v", m, err)
		}
		want := new(big.Int).Mod(Exact(12345, 678), new(big.Int).SetUint64(m)).Uint64()
		if got != want {
			t.Errorf("Mod(12345, 678, %d) = %d, want %d", m, got, want)
		}
	}

	if _, err := calc.Mod(10, 3, 0); err != ErrNonPositiveModulus {
		t.Errorf("Mod(10, 3, 0) err = %v, want ErrNonPositiveModulus", err)
	}
}

func TestModOutOfRangeIsFree(t *testing.T) {
	calc := NewCalculator(0)
	for _, nk := range [][2]int64{{10, 11}, {10, -1}, {0, 2}} {
		got, err := calc.Mod(nk[0], nk[1], 1<<62)
		if err != nil || got != 0 {
			t.Errorf("Mod(%d, %d, big) = %d, %v; want 0, nil", nk[0], nk[1], got, err)
		}
	}
}

func TestHighPrimePowerModuli(t *testing.T) {
	moduli := []uint64{
		1 << 30, 1 << 50, 1 << 63,
		1 << 20 * 531441,        // 2^20 * 3^12
		390625 * 16807,          // 5^8 * 7^5
		1 << 12 * 243 * 25 * 49, // 2^12 * 3^5 * 5^2 * 7^2
	}
	calc := NewCalculator(testBudget)
	for _, m := range moduli {
		bm := new(big.Int).SetUint64(m)
		for n := int64(0); n <= 300; n += 17 {
			for k := int64(0); k <= n; k += 5 {
				got, err := calc.Mod(n, k, m)
				if err != nil {
					t.Fatalf("Mod(%d, %d, %d) error: %v", n, k, m, err)
				}
				want := new(big.Int).Mod(Exact(n, k), bm).Uint64()
				if got != want {
					t.Fatalf("Mod(%d, %d, %d) = %d, want %d", n, k, m, got, want)
				}
			}
		}
	}
}

func TestLargeNIdentities(t *testing.T) {
	m := uint64(1) << 20 * 531441 // 2^20 * 3^12
	calc := NewCalculator(testBudget)
	n, k := int64(1_000_000), int64(500_000)

	a, err := calc.Mod(n, k, m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := calc.Mod(n-1, k, m)
	if err != nil {
		t.Fatal(err)
	}
	c, err := calc.Mod(n-1, k-1, m)
	if err != nil {
		t.Fatal(err)
	}
	if (b+c)%m != a {
		t.Errorf("Pascal identity failed: C(n-1,k)+C(n-1,k-1) = %d, C(n,k) = %d (mod %d)", (b+c)%m, a, m)
	}

	sym, err := calc.Mod(n, n-k, m)
	if err != nil {
		t.Fatal(err)
	}
	if sym != a {
		t.Errorf("symmetry failed: C(n, n-k) = %d, C(n, k) = %d (mod %d)", sym, a, m)
	}
}

func TestBatchSharesFactorizations(t *testing.T) {
	calc := NewCalculator(testBudget)
	m1 := uint64(1) << 20 * 531441
	m2 := uint64(999983)
	queries := make([]Query, 0, 64)
	for i := int64(0); i < 30; i++ {
		queries = append(queries, Query{N: 500 + i, K: 100 + i, M: m1})
		queries = append(queries, Query{N: 500 + i, K: 100 + i, M: m2})
	}
	results, err := calc.ModBatch(queries)
	if err != nil {
		t.Fatal(err)
	}
	if got := calc.Factorizer().Factorizations; got != 2 {
		t.Errorf("Factorizations = %d, want 2 (one per distinct modulus)", got)
	}
	for i, q := range queries {
		want := new(big.Int).Mod(Exact(q.N, q.K), new(big.Int).SetUint64(q.M)).Uint64()
		if results[i] != want {
			t.Fatalf("batch query %d: got %d, want %d", i, results[i], want)
		}
	}
}

func TestBudgetLimitsFactorization(t *testing.T) {
	calc := NewCalculator(0)
	if _, err := calc.Mod(10, 5, 12); err != ErrBudgetExceeded {
		t.Errorf("zero budget: err = %v, want ErrBudgetExceeded", err)
	}

	calc = NewCalculator(1)
	if _, err := calc.Mod(10, 5, 12); err != nil {
		t.Errorf("budget 1 suffices for m=12, got err %v", err)
	}

	semiprime := uint64(1000003) * 1000033
	calc = NewCalculator(50)
	if _, err := calc.Mod(100, 40, semiprime); err != ErrBudgetExceeded {
		t.Errorf("budget 50 on semiprime: err = %v, want ErrBudgetExceeded", err)
	}

	calc = NewCalculator(testBudget)
	got, err := calc.Mod(100, 40, semiprime)
	if err != nil {
		t.Fatalf("ample budget on semiprime: %v", err)
	}
	want := new(big.Int).Mod(Exact(100, 40), new(big.Int).SetUint64(semiprime)).Uint64()
	if got != want {
		t.Errorf("Mod(100, 40, %d) = %d, want %d", semiprime, got, want)
	}
}

func TestCachedFactorizationIsFree(t *testing.T) {
	calc := NewCalculator(1)
	if _, err := calc.Mod(10, 5, 1<<20); err != nil {
		t.Fatalf("first factorization: %v", err)
	}
	before := calc.Factorizer().RemainingBudget()
	for i := int64(0); i < 100; i++ {
		if _, err := calc.Mod(1000+i, 500, 1<<20); err != nil {
			t.Fatalf("cached query %d: %v", i, err)
		}
	}
	if after := calc.Factorizer().RemainingBudget(); after != before {
		t.Errorf("cached queries consumed budget: before %d, after %d", before, after)
	}
}
