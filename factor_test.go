package binom

import "testing"

func TestIsPrime(t *testing.T) {
	primes := []uint64{2, 3, 5, 97, 1000003, 1000033, 1_000_000_007, 2305843009213693951}
	composites := []uint64{
		0, 1, 4, 6, 561, 1105, 410041, 3215031751,
		1000003 * 1000033, 1 << 62, 2305843009213693951 * 3,
	}
	for _, p := range primes {
		if !isPrime(p) {
			t.Errorf("isPrime(%d) = false, want true", p)
		}
	}
	for _, c := range composites {
		if isPrime(c) {
			t.Errorf("isPrime(%d) = true, want false", c)
		}
	}
}

func TestFactorize(t *testing.T) {
	cases := []struct {
		m    uint64
		want []PrimePower
	}{
		{1, nil},
		{2, []PrimePower{{2, 1, 2}}},
		{1 << 20, []PrimePower{{2, 20, 1 << 20}}},
		{12, []PrimePower{{2, 2, 4}, {3, 1, 3}}},
		{360, []PrimePower{{2, 3, 8}, {3, 2, 9}, {5, 1, 5}}},
		{1000003 * 1000033, []PrimePower{{1000003, 1, 1000003}, {1000033, 1, 1000033}}},
		{531441 * 4, []PrimePower{{2, 2, 4}, {3, 12, 531441}}},
	}
	for _, c := range cases {
		fz := NewFactorizer(testBudget)
		got, err := fz.Factorize(c.m)
		if err != nil {
			t.Fatalf("Factorize(%d): %v", c.m, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("Factorize(%d) = %v, want %v", c.m, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("Factorize(%d) = %v, want %v", c.m, got, c.want)
			}
		}
	}
}

func TestFactorizeBudget(t *testing.T) {
	fz := NewFactorizer(0)
	if _, err := fz.Factorize(4); err != ErrBudgetExceeded {
		t.Errorf("Factorize(4) with zero budget: err = %v, want ErrBudgetExceeded", err)
	}
	if _, err := fz.Factorize(1); err != nil {
		t.Errorf("Factorize(1) with zero budget: err = %v, want nil", err)
	}
	if _, err := fz.Factorize(999983); err != nil {
		t.Errorf("Factorize(prime) with zero budget: err = %v, want nil", err)
	}

	fz = NewFactorizer(0)
	if _, err := fz.Factorize(1000003 * 1000033); err != ErrBudgetExceeded {
		t.Errorf("Factorize(semiprime) with zero budget: err = %v, want ErrBudgetExceeded", err)
	}
}

func TestFactorizeCache(t *testing.T) {
	fz := NewFactorizer(testBudget)
	m := uint64(1) << 20 * 531441
	if _, err := fz.Factorize(m); err != nil {
		t.Fatal(err)
	}
	remaining := fz.RemainingBudget()
	for i := 0; i < 10; i++ {
		if _, err := fz.Factorize(m); err != nil {
			t.Fatal(err)
		}
	}
	if fz.RemainingBudget() != remaining {
		t.Errorf("cache hits consumed budget: %d -> %d", remaining, fz.RemainingBudget())
	}
	if fz.Factorizations != 1 {
		t.Errorf("Factorizations = %d, want 1", fz.Factorizations)
	}
}
