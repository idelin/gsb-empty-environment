package binom

import (
	"math/big"
	"testing"
)

func factorMap(t *testing.T, m *big.Int, budget int64) map[string]int {
	t.Helper()
	factors, err := factorize(m, budget)
	if err != nil {
		t.Fatalf("factorize(%v): %v", m, err)
	}
	out := make(map[string]int, len(factors))
	prod := big.NewInt(1)
	for _, pp := range factors {
		out[pp.prime.String()] = pp.exp
		want := new(big.Int).Exp(pp.prime, big.NewInt(int64(pp.exp)), nil)
		if pp.power.Cmp(want) != 0 {
			t.Fatalf("power = %v, want %v", pp.power, want)
		}
		prod.Mul(prod, pp.power)
	}
	if prod.Cmp(m) != 0 {
		t.Fatalf("product of factors = %v, want %v", prod, m)
	}
	return out
}

func TestFactorizeBasic(t *testing.T) {
	got := factorMap(t, big.NewInt(360), -1)
	want := map[string]int{"2": 3, "3": 2, "5": 1}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p, e := range want {
		if got[p] != e {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFactorizeHighPowers(t *testing.T) {
	m := new(big.Int).Exp(big.NewInt(2), big.NewInt(30), nil)
	m.Mul(m, new(big.Int).Exp(big.NewInt(3), big.NewInt(15), nil))
	got := factorMap(t, m, -1)
	if got["2"] != 30 || got["3"] != 15 || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestFactorizePrimeCostsNoBudget(t *testing.T) {
	// A prime modulus is detected by the primality proof before any
	// trial division, so even a zero budget suffices.
	p := mustBig(t, "999999999989")
	got := factorMap(t, p, 0)
	if len(got) != 1 || got["999999999989"] != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestFactorizeBudgetExceeded(t *testing.T) {
	// 1000036000099 = 1000003 * 1000033; trial division needs ~500k
	// candidates to reach the smaller factor.
	m := mustBig(t, "1000036000099")
	if _, err := factorize(m, 1000); err != ErrFactorBudgetExceeded {
		t.Fatalf("err = %v, want ErrFactorBudgetExceeded", err)
	}
	got := factorMap(t, m, -1)
	if got["1000003"] != 1 || got["1000033"] != 1 || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestFactorizeSquareFreeAndPowersOfTwo(t *testing.T) {
	got := factorMap(t, big.NewInt(2*3*5*7*11*13), -1)
	if len(got) != 6 {
		t.Fatalf("got %v", got)
	}
	got = factorMap(t, new(big.Int).Lsh(big.NewInt(1), 64), -1)
	if len(got) != 1 || got["2"] != 64 {
		t.Fatalf("got %v", got)
	}
}
