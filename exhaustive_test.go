package parity_test

import (
	"math/big"
	"testing"

	parity "prescreen.local/ps-2b994de5fb"
)

type tinyKey struct {
	N, E, D *big.Int
}

func makeTinyKey(p, q int64) tinyKey {
	P := big.NewInt(p)
	Q := big.NewInt(q)
	N := new(big.Int).Mul(P, Q)
	phi := new(big.Int).Mul(big.NewInt(p-1), big.NewInt(q-1))
	var E, D *big.Int
	for _, cand := range []int64{3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37} {
		e := big.NewInt(cand)
		if new(big.Int).GCD(nil, nil, e, phi).Cmp(big.NewInt(1)) != 0 {
			continue
		}
		d := new(big.Int).ModInverse(e, phi)
		if d == nil {
			continue
		}
		E, D = e, d
		break
	}
	if E == nil {
		panic("no suitable e for tiny key")
	}
	return tinyKey{N: N, E: E, D: D}
}

func (k tinyKey) oracle() parity.Callback {
	return func(x *big.Int) (parity.Parity, error) {
		if x.Sign() < 0 || x.Cmp(k.N) >= 0 {
			return parity.ParityEven, parity.ErrInvalidQuery
		}
		z := new(big.Int).Exp(x, k.D, k.N)
		if z.Bit(0) == 1 {
			return parity.ParityOdd, nil
		}
		return parity.ParityEven, nil
	}
}

func encrypt(k tinyKey, m *big.Int) *big.Int {
	return new(big.Int).Exp(m, k.E, k.N)
}

// expectedDyadicEndpoints returns Lo=j*N/2^i, Hi=(j+1)*N/2^i with
// j=floor(2^i*m/N), exactly as rational numbers.
func expectedDyadicEndpoints(N, m *big.Int, i int) (*big.Rat, *big.Rat) {
	pow2 := new(big.Int).Lsh(big.NewInt(1), uint(i))
	num := new(big.Int).Mul(pow2, m)
	j := new(big.Int).Quo(num, N)

	loNum := new(big.Int).Mul(j, N)
	lo := new(big.Rat).SetInt(loNum)
	lo.Quo(lo, new(big.Rat).SetInt(pow2))

	j1 := new(big.Int).Add(j, big.NewInt(1))
	hiNum := new(big.Int).Mul(j1, N)
	hi := new(big.Rat).SetInt(hiNum)
	hi.Quo(hi, new(big.Rat).SetInt(pow2))
	return lo, hi
}

func TestExhaustiveSmallModuliEndpoints(t *testing.T) {
	primes := []int64{3, 5, 7, 11, 13, 17, 19, 23}
	moduli := 0
	plaintexts := 0
	for ai, p := range primes {
		for _, q := range primes[ai+1:] {
			k := makeTinyKey(p, q)
			moduli++
			for m64 := int64(0); m64 < k.N.Int64(); m64++ {
				m := big.NewInt(m64)
				c := encrypt(k, m)
				rec := parity.NewRecordingLogger()
				res, err := parity.Recover(
					parity.Params{N: k.N, E: k.E, C: c, KeyVersion: "tiny-v1"},
					k.oracle(),
					parity.Options{
						Logger:   rec,
						Blinding: parity.BlindNone,
					},
				)
				if err != nil {
					t.Fatalf("N=%s m=%d: Recover error: %v", k.N, m64, err)
				}
				if res.Status != parity.StatusRecovered || res.Plaintext.Cmp(m) != 0 {
					t.Fatalf("N=%s m=%d: status=%s got=%v candidates=%s", k.N, m64, res.Status, res.Plaintext, res.Candidates)
				}
				if !verifyPow(k, res.Plaintext, c) {
					t.Fatalf("N=%s m=%d: recovered candidate fails c check", k.N, m64)
				}

				// Validate every logged derivation endpoint against the exact
				// dyadic cell dictated by the known plaintext.
				for _, ev := range rec.Events() {
					if ev.Kind != parity.EventDerivation {
						continue
					}
					i := ev.Step
					wantLo, wantHi := expectedDyadicEndpoints(k.N, m, i)
					gotLo, ok1 := new(big.Rat).SetString(ev.Lower)
					gotHi, ok2 := new(big.Rat).SetString(ev.Upper)
					if !ok1 || !ok2 {
						t.Fatalf("N=%s m=%d step=%d: bad rat endpoints %q %q", k.N, m64, i, ev.Lower, ev.Upper)
					}
					if gotLo.Cmp(wantLo) != 0 || gotHi.Cmp(wantHi) != 0 {
						t.Fatalf("N=%s m=%d step=%d: endpoint [%s,%s] want [%s,%s]",
							k.N, m64, i, gotLo.RatString(), gotHi.RatString(), wantLo.RatString(), wantHi.RatString())
					}
					if ev.Intervals != 1 {
						t.Fatalf("N=%s m=%d step=%d: canonical path must keep one interval, got %d", k.N, m64, i, ev.Intervals)
					}
				}

				// Every observable query must be in [0,N).
				for _, h := range res.History {
					x := new(big.Int)
					if _, ok := x.SetString(h.X, 16); !ok {
						t.Fatalf("N=%s m=%d: bad history x %q", k.N, m64, h.X)
					}
					if x.Sign() < 0 || x.Cmp(k.N) >= 0 {
						t.Fatalf("N=%s m=%d: history x out of range %s", k.N, m64, x)
					}
				}
				plaintexts++
			}
		}
	}
	t.Logf("exhausted %d plaintexts across %d small moduli", plaintexts, moduli)
}

func verifyPow(k tinyKey, m, c *big.Int) bool {
	return new(big.Int).Exp(m, k.E, k.N).Cmp(c) == 0
}

func TestEdgePlaintextsZeroAndNearN(t *testing.T) {
	k := makeTinyKey(11, 13) // N=143
	for _, m := range []*big.Int{
		big.NewInt(0),
		big.NewInt(1),
		new(big.Int).Sub(k.N, big.NewInt(1)),
		new(big.Int).Sub(k.N, big.NewInt(2)),
	} {
		c := encrypt(k, m)
		res, err := parity.Recover(parity.Params{N: k.N, E: k.E, C: c}, k.oracle(), parity.Options{Blinding: parity.BlindNone})
		if err != nil {
			t.Fatalf("m=%s: %v", m, err)
		}
		if res.Plaintext.Cmp(m) != 0 {
			t.Fatalf("m=%s got %s", m, res.Plaintext)
		}
	}
}
