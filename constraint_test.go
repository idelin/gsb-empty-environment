package parity

import (
	"math/big"
	"testing"
)

func bruteForceCandidates(N, T *big.Int, want Parity) []*big.Int {
	var got []*big.Int
	zero := big.NewInt(0)
	for m := big.NewInt(0); m.Cmp(N) < 0; m.Add(m, big.NewInt(1)) {
		res := new(big.Int).Mul(T, m)
		res.Mod(res, N)
		p := ParityEven
		if res.Bit(0) == 1 {
			p = ParityOdd
		}
		if p == want {
			got = append(got, new(big.Int).Set(m))
		}
	}
	_ = zero
	return got
}

func intervalCandidatesExact(intervals []RatInterval, N *big.Int) []*big.Int {
	nm1 := new(big.Int).Sub(N, big.NewInt(1))
	var out []*big.Int
	for _, iv := range intervals {
		lo, hi, ok := iv.integerRange(nm1)
		if !ok {
			continue
		}
		for m := new(big.Int).Set(lo); m.Cmp(hi) <= 0; m.Add(m, big.NewInt(1)) {
			out = append(out, new(big.Int).Set(m))
		}
	}
	return out
}

func TestApplyParityExactAgainstBruteForce(t *testing.T) {
	// Every odd N in range, several even multipliers including arbitrary r,
	// restricted to gcd(T,N)==1 (the invariant maintained by the blinder).
	for n64 := int64(3); n64 <= 65; n64 += 2 {
		N := big.NewInt(n64)
		for _, tBase := range []int64{1, 2, 3, 4, 5, 7, 8, 11, 13} {
			T := big.NewInt(2 * tBase) // must be even
			if new(big.Int).GCD(nil, nil, T, N).Cmp(big.NewInt(1)) != 0 {
				continue
			}
			for _, want := range []Parity{ParityEven, ParityOdd} {
				start := []RatInterval{{Lo: big.NewRat(0, 1), Hi: new(big.Rat).SetInt(N)}}
				got, err := applyParity(start, N, T, want, 0)
				if err != nil {
					t.Fatalf("N=%d T=%s want=%s: %v", n64, T, want, err)
				}
				cands := intervalCandidatesExact(got, N)
				wantCands := bruteForceCandidates(N, T, want)
				if len(cands) != len(wantCands) {
					t.Fatalf("N=%d T=%s want=%s: got %d candidates %v, want %d %v",
						n64, T, want, len(cands), bigInts(cands), len(wantCands), bigInts(wantCands))
				}
				for i := range cands {
					if cands[i].Cmp(wantCands[i]) != 0 {
						t.Fatalf("N=%d T=%s want=%s: mismatch at %d got %s want %s (all got=%v want=%v)",
							n64, T, want, i, cands[i], wantCands[i], bigInts(cands), bigInts(wantCands))
					}
				}
			}
		}
	}
}

func bigInts(xs []*big.Int) []int64 {
	out := make([]int64, len(xs))
	for i, x := range xs {
		out[i] = x.Int64()
	}
	return out
}

func TestApplyParityRepeatedPowerTwoStaysOneInterval(t *testing.T) {
	N := big.NewInt(143)
	for m64 := int64(0); m64 < 143; m64++ {
		m := big.NewInt(m64)
		intervals := []RatInterval{{Lo: big.NewRat(0, 1), Hi: new(big.Rat).SetInt(N)}}
		T := big.NewInt(1)
		for i := 1; i <= 12; i++ {
			T.Mul(T, big.NewInt(2))
			res := new(big.Int).Mul(T, m)
			res.Mod(res, N)
			want := ParityEven
			if res.Bit(0) == 1 {
				want = ParityOdd
			}
			var err error
			intervals, err = applyParity(intervals, N, T, want, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(intervals) != 1 {
				t.Fatalf("m=%d i=%d: expected 1 interval, got %d", m64, i, len(intervals))
			}
		}
		count, unique := countCandidates(intervals, N)
		if unique == nil || unique.Cmp(m) != 0 {
			t.Fatalf("m=%d: count=%s unique=%v intervals=%v", m64, count, unique, intervals)
		}
	}
}
