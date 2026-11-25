package parity_test

import (
	"context"
	"math/big"
	"testing"

	parity "prescreen.local/ps-2b994de5fb"
)

func TestBlindQueryArithmetic(t *testing.T) {
	N := big.NewInt(143)
	e := big.NewInt(7)
	x := big.NewInt(99)
	r := big.NewInt(4)
	got := parity.BlindQuery(x, r, e, N)
	rE := new(big.Int).Exp(r, e, N)
	want := new(big.Int).Mod(new(big.Int).Mul(x, rE), N)
	if got.Cmp(want) != 0 {
		t.Fatalf("BlindQuery=%s want %s", got, want)
	}
	if got.Sign() < 0 || got.Cmp(N) >= 0 {
		t.Fatalf("blinded query out of range: %s", got)
	}

	factors := parity.FactorSmall(big.NewInt(12))
	prod := big.NewInt(1)
	for _, f := range factors {
		prod.Mul(prod, f)
	}
	if prod.Cmp(big.NewInt(12)) != 0 {
		t.Fatalf("factors %v do not multiply to 12", factors)
	}
}

// dupGuard blocks any repeated x as a single-point duplicate and can inject a
// one-shot unavailable burst to force the retry path.
type dupGuard struct {
	inner            parity.Callback
	seen             map[string]bool
	unavailableFirst int
	calls            int
}

func (d *dupGuard) query(x *big.Int) (parity.Parity, error) {
	d.calls++
	key := x.Text(16)
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	if d.seen[key] {
		return parity.ParityRejected, parity.ErrUnavailable
	}
	d.seen[key] = true
	if d.unavailableFirst > 0 {
		d.unavailableFirst--
		return parity.ParityRejected, parity.ErrUnavailable
	}
	return d.inner(x)
}

func TestBlindRetryBypassesDuplicatePoint(t *testing.T) {
	k := makeTinyKey(17, 23) // N=391
	for m64 := int64(1); m64 < k.N.Int64(); m64++ {
		g := &dupGuard{inner: k.oracle(), unavailableFirst: 1}
		res, err := parity.Recover(paramsFor(k, m64), g.query, parity.Options{QueryBudget: 300})
		if err != nil {
			t.Fatalf("m=%d: %v", m64, err)
		}
		if res.Plaintext.Int64() != m64 {
			t.Fatalf("m=%d got %s", m64, res.Plaintext)
		}
		sawBlind := false
		parityX := map[string]bool{}
		for _, h := range res.History {
			if h.Kind == "blind" {
				sawBlind = true
			}
			if h.Outcome == parity.OutcomeEven || h.Outcome == parity.OutcomeOdd {
				if parityX[h.X] {
					t.Fatalf("m=%d: parity succeeded on duplicate x=%s", m64, h.X)
				}
				parityX[h.X] = true
			}
		}
		if !sawBlind {
			t.Fatalf("m=%d: expected a blinded retry record", m64)
		}
	}
}

func fixedBaseR(r int64) parity.BlindFunc {
	return func(N *big.Int) (parity.Blind, error) {
		rr := big.NewInt(r)
		if new(big.Int).GCD(nil, nil, rr, N).Cmp(big.NewInt(1)) != 0 {
			return parity.Blind{}, parity.ErrInvalidInput
		}
		return parity.Blind{R: rr, Factors: parity.FactorSmall(rr)}, nil
	}
}

func TestBaseBlindExhaustive(t *testing.T) {
	k := makeTinyKey(13, 17) // N=221
	for m64 := int64(0); m64 < k.N.Int64(); m64++ {
		for _, r := range []int64{3, 5, 9} {
			p := parity.Params{N: k.N, E: k.E, C: encrypt(k, big.NewInt(m64)), KeyVersion: "bv"}
			res, err := parity.Recover(p, k.oracle(), parity.Options{
				BaseBlind:  true,
				BaseBlindR: fixedBaseR(r),
			})
			if err != nil {
				t.Fatalf("m=%d r=%d: %v", m64, r, err)
			}
			if res.Plaintext.Int64() != m64 {
				t.Fatalf("m=%d r=%d got %s", m64, r, res.Plaintext)
			}
			if len(res.Intervals) != 1 {
				t.Fatalf("m=%d r=%d: intervals=%d", m64, r, len(res.Intervals))
			}
		}
	}
}

func TestBaseBlindRerandomizesPointsAndResume(t *testing.T) {
	k := makeTinyKey(17, 19) // N=323
	p := paramsFor(k, 250)
	g := &dupGuard{inner: k.oracle()}
	res, err := parity.Recover(p, g.query, parity.Options{
		BaseBlind:   true,
		BaseBlindR:  fixedBaseR(7),
		QueryBudget: 5,
	})
	if err == nil || !parity.IsBudget(err) {
		t.Fatalf("want budget, got %v", err)
	}
	cp := res.Checkpoint
	if cp.BaseBlindR == "" || cp.WorkingC == "" {
		t.Fatal("base-blind checkpoint missing r/working c")
	}
	buf, _ := cp.Marshal()
	cp2, err := parity.ParseCheckpoint(buf)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := parity.Resume(context.Background(), p, cp2, k.oracle(), parity.Options{BaseBlind: true})
	if err != nil {
		t.Fatalf("resume base-blind: %v", err)
	}
	if res2.Plaintext.Int64() != 250 {
		t.Fatalf("got %s", res2.Plaintext)
	}
	if new(big.Int).Exp(res2.Plaintext, k.E, k.N).Cmp(p.C) != 0 {
		t.Fatal("outer c verification failed")
	}
}

func TestCiphertextZeroUnderUnavailable(t *testing.T) {
	k := makeTinyKey(13, 19) // N=247
	g := &scripted{inner: k.oracle(), unavailableCalls: 7}
	res, err := parity.Recover(parity.Params{N: k.N, E: k.E, C: big.NewInt(0)}, g.query, parity.Options{QueryBudget: 500})
	if err != nil {
		t.Fatalf("c=0: %v", err)
	}
	if res.Plaintext.Sign() != 0 {
		t.Fatalf("c=0 must recover m=0, got %s", res.Plaintext)
	}
	if new(big.Int).Exp(res.Plaintext, k.E, k.N).Sign() != 0 {
		t.Fatal("recovered 0 failed verification")
	}
}
