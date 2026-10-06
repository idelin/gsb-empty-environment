package convlib

import (
	"math/big"
	"math/rand"
	"testing"
)

// TestPrimitiveRoots verifies every tabled g is a primitive root mod p and
// that 2^maxLog really divides p-1, so the NTT roots of unity exist.
func TestPrimitiveRoots(t *testing.T) {
	for _, pr := range nttPrimes {
		pm1 := pr.p - 1
		if pm1%(uint64(1)<<pr.maxLog) != 0 {
			t.Fatalf("p=%d: 2^%d does not divide p-1", pr.p, pr.maxLog)
		}
		// Factor p-1 by trial division; g is a primitive root iff
		// g^((p-1)/q) != 1 for every prime factor q of p-1.
		n := pm1
		for q := uint64(2); q*q <= n; q++ {
			if n%q == 0 {
				if powmod(pr.g, pm1/q, pr.p) == 1 {
					t.Fatalf("p=%d g=%d: not primitive (fails for factor %d)", pr.p, pr.g, q)
				}
				for n%q == 0 {
					n /= q
				}
			}
		}
		if n > 1 && powmod(pr.g, pm1/n, pr.p) == 1 {
			t.Fatalf("p=%d g=%d: not primitive (fails for factor %d)", pr.p, pr.g, n)
		}
	}
}

func checkEqual(t *testing.T, got, want []*big.Int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Cmp(want[i]) != 0 {
			t.Fatalf("coefficient %d mismatch: got %s want %s", i, got[i], want[i])
		}
	}
}

// TestRandomShortSequences cross-checks NTT+CRT against the naive big-int
// double loop on random short sequences that include negative values.
func TestRandomShortSequences(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		la := 1 + rng.Intn(24)
		lb := 1 + rng.Intn(24)
		a := make([]int64, la)
		b := make([]int64, lb)
		mag := int64(1) << uint(1+rng.Intn(40)) // up to ~2^40 magnitude
		for i := range a {
			a[i] = rng.Int63n(2*mag) - mag
		}
		for i := range b {
			b[i] = rng.Int63n(2*mag) - mag
		}
		got, bound, m, err := Convolve(a, b)
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		if new(big.Int).Lsh(bound, 1).Cmp(m) >= 0 {
			t.Fatalf("trial %d: invariant violated: 2B=%s >= M=%s", trial, bound, m)
		}
		checkEqual(t, got, ConvolveNaive(a, b))
		if trial < 3 {
			t.Logf("trial %d: len=%d+%d bound B=%s M=%s", trial, la, lb, bound, m)
		}
	}
}

// TestNearModulusBoundary builds sequences whose middle coefficient equals
// the bound B exactly, with 2B only just below the two-prime product
// M ~ 1.0028e18: the coefficient magnitude is >99% of M/2, so balanced-CRT
// sign recovery is exercised right at the edge of the provable range.
func TestNearModulusBoundary(t *testing.T) {
	const m = 1000
	const magA = 1000000   // 1e6
	const magB = 500000000 // 5e8
	// B = m * magA * magB = 5e17, 2B = 1e18 < M(2 primes) ~ 1.0028e18,
	// while 2B >> p1 alone, so exactly two primes are selected.
	mk := func(v int64) []int64 {
		s := make([]int64, m)
		for i := range s {
			s[i] = v
		}
		return s
	}
	cases := []struct {
		name string
		a, b []int64
	}{
		{"positive", mk(magA), mk(magB)},
		{"negative", mk(magA), mk(-magB)},
		{"mixed", mk(-magA), mk(magB)},
		{"bothneg", mk(-magA), mk(-magB)},
	}
	for _, c := range cases {
		got, bound, mprod, err := Convolve(c.a, c.b)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		checkEqual(t, got, ConvolveNaive(c.a, c.b))
		mid := got[m-1]
		if new(big.Int).Abs(mid).Cmp(bound) != 0 {
			t.Fatalf("%s: middle coefficient %s does not saturate bound %s", c.name, mid, bound)
		}
		ratio := new(big.Rat).SetFrac(new(big.Int).Lsh(new(big.Int).Abs(mid), 1), mprod)
		f, _ := ratio.Float64()
		t.Logf("%s: B=%s M=%s 2|c_mid|/M=%.6f", c.name, bound, mprod, f)
		if f < 0.99 {
			t.Fatalf("%s: coefficient not actually near the modulus boundary", c.name)
		}
	}
}

// TestRefusesTruncation uses a bound beyond the product of all tabled
// primes (full-table M ~ 5.9e43): the library must return an error instead
// of wrapped values.
func TestRefusesTruncation(t *testing.T) {
	huge := new(big.Int).Exp(big.NewInt(2), big.NewInt(200), nil)
	if _, _, err := selectPrimes(huge); err != ErrModulusProductTooSmall {
		t.Fatalf("expected ErrModulusProductTooSmall, got %v", err)
	}
	// Public API must surface the error, not truncated coefficients.
	// B = 2.5e6 * (2^62-1)^2 ~ 5.3e43, so 2B ~ 1.1e44 > M_full.
	big1 := make([]int64, 2500000)
	for i := range big1 {
		big1[i] = 1<<62 - 1
	}
	_, bound, mprod, err := Convolve(big1, big1)
	if err != ErrModulusProductTooSmall {
		t.Fatalf("expected ErrModulusProductTooSmall, got %v", err)
	}
	t.Logf("refused: 2B=%s exceeds full table product M=%s",
		new(big.Int).Lsh(bound, 1), mprod)
}

// TestSignedMod checks negative residue normalization.
func TestSignedMod(t *testing.T) {
	if got := signedMod(-1, 998244353); got != 998244352 {
		t.Fatalf("signedMod(-1) = %d", got)
	}
	if got := signedMod(-998244353, 998244353); got != 0 {
		t.Fatalf("signedMod(-p) = %d", got)
	}
	if got := signedMod(5, 998244353); got != 5 {
		t.Fatalf("signedMod(5) = %d", got)
	}
}
