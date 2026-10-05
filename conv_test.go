package conv

import (
	"context"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

func randPoly(rng *rand.Rand, maxDeg, maxCoeff int) *Poly {
	c := make([]*big.Int, rng.Intn(maxDeg+1))
	for i := range c {
		c[i] = big.NewInt(int64(rng.Intn(2*maxCoeff+1) - maxCoeff))
	}
	return &Poly{Coeffs: c}
}

func naiveCoeff(full []*big.Int, k int) *big.Int {
	if k < 0 || k >= len(full) {
		return new(big.Int)
	}
	return full[k]
}

// checkInterval verifies res against the naive reference over [lo, hi),
// and verifies that Done tiles [lo, hi) exactly.
func checkInterval(t *testing.T, res *Result, a, b *Poly, lo, hi int) {
	t.Helper()
	full := Naive(a, b)
	for k := lo; k < hi; k++ {
		got, ok := res.At(k)
		if !ok {
			t.Fatalf("coefficient %d missing", k)
		}
		if want := naiveCoeff(full, k); got.Cmp(want) != 0 {
			t.Fatalf("coefficient %d: got %s, want %s", k, got, want)
		}
	}
	// Done must tile [lo, hi) exactly, in any order.
	done := make([]Interval, len(res.Done))
	copy(done, res.Done)
	sort.Slice(done, func(i, j int) bool { return done[i].Lo < done[j].Lo })
	cover := lo
	for _, iv := range done {
		if iv.Lo < lo || iv.Hi > hi || iv.Lo != cover {
			t.Fatalf("Done intervals do not tile [%d,%d): %v", lo, hi, res.Done)
		}
		cover = iv.Hi
	}
	if cover != hi {
		t.Fatalf("Done intervals cover up to %d, want %d: %v", cover, hi, res.Done)
	}
}

func TestRandomSmallVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		a := randPoly(rng, 20, 15)
		b := randPoly(rng, 20, 15)
		span := a.deg() + b.deg() + 3
		lo := rng.Intn(span + 1)
		hi := lo + rng.Intn(span-lo+1)
		opts := &Options{BlockSize: 1 + rng.Intn(7)}
		res, err := Product(context.Background(), a, b, lo, hi, opts)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		checkInterval(t, res, a, b, lo, hi)
	}
}

func TestNegativeCoefficients(t *testing.T) {
	a := NewPoly(big.NewInt(3), big.NewInt(-2), big.NewInt(0), big.NewInt(5), big.NewInt(-7))
	b := NewPoly(big.NewInt(-4), big.NewInt(1), big.NewInt(-1), big.NewInt(2))
	res, err := Product(context.Background(), a, b, 0, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkInterval(t, res, a, b, 0, 8)
	if c, _ := res.At(0); c.Cmp(big.NewInt(-12)) != 0 {
		t.Fatalf("c_0 = %s, want -12", c)
	}
	// A middle sub-interval must agree with the full computation.
	sub, err := Product(context.Background(), a, b, 2, 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k := 2; k < 6; k++ {
		got, _ := sub.At(k)
		want, _ := res.At(k)
		if got.Cmp(want) != 0 {
			t.Fatalf("sub-interval coefficient %d: got %s, want %s", k, got, want)
		}
	}
}

func TestSparseHighDegree(t *testing.T) {
	// a = x^5000 - 3, b = x^7000 + 2*x^100 - 1
	a := NewPoly(big.NewInt(-3), big.NewInt(0))
	a.Coeffs = append(a.Coeffs[:1], make([]*big.Int, 5000)...)
	a.Coeffs[5000] = big.NewInt(1)
	b := NewPoly(big.NewInt(-1))
	b.Coeffs = make([]*big.Int, 7001)
	b.Coeffs[0] = big.NewInt(-1)
	b.Coeffs[100] = big.NewInt(2)
	b.Coeffs[7000] = big.NewInt(1)

	for _, iv := range [][2]int{{0, 3}, {99, 102}, {5099, 5102}, {6999, 7002}, {11999, 12001}, {12000, 12005}} {
		res, err := Product(context.Background(), a, b, iv[0], iv[1], &Options{BlockSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		checkInterval(t, res, a, b, iv[0], iv[1])
	}
	// Spot checks of the cross terms:
	// c_0 = 3, c_100 = -6, c_5000 = -1, c_5100 = 2, c_7000 = -3,
	// c_12000 = 1, and c_7100 = 0 (no index pair sums to 7100).
	for k, want := range map[int]int64{0: 3, 100: -6, 5000: -1, 5100: 2, 7000: -3, 12000: 1, 7100: 0} {
		res, err := Product(context.Background(), a, b, k, k+1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c, _ := res.At(k); c.Cmp(big.NewInt(want)) != 0 {
			t.Fatalf("coefficient %d: got %s, want %d", k, c, want)
		}
	}
}

func TestHugeCoefficients(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	mk := func(n int) *Poly {
		c := make([]*big.Int, n)
		for i := range c {
			v := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 220))
			if rng.Intn(2) == 0 {
				v.Neg(v)
			}
			c[i] = v
		}
		return &Poly{Coeffs: c}
	}
	a, b := mk(7), mk(9)
	// bound ~ 2^440 * 7 -> needs at least 15 31-bit primes.
	res, err := Product(context.Background(), a, b, 0, 15, &Options{BlockSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	checkInterval(t, res, a, b, 0, 15)
}

func TestCancellation(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	dense := func() *Poly {
		c := make([]*big.Int, 1201)
		for i := range c {
			c[i] = big.NewInt(int64(rng.Intn(201) - 100))
			if c[i].Sign() == 0 {
				c[i] = big.NewInt(1)
			}
		}
		return &Poly{Coeffs: c}
	}
	a, b := dense(), dense()
	const lo, hi, bs = 0, 2601, 100
	ctx, cancel := context.WithCancel(context.Background())
	blocks := 0
	opts := &Options{
		BlockSize: bs,
		OnBlockDone: func(Interval) {
			blocks++
			if blocks == 3 {
				cancel()
			}
		},
	}
	res, err := Product(ctx, a, b, lo, hi, opts)
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// The zero tail [2401, 2601) completes immediately; afterwards
	// exactly the first three blocks must be completed.
	wantDone := []Interval{{2401, 2601}, {0, 100}, {100, 200}, {200, 300}}
	if len(res.Done) != len(wantDone) {
		t.Fatalf("Done = %v, want %v", res.Done, wantDone)
	}
	for i, iv := range wantDone {
		if res.Done[i] != iv {
			t.Fatalf("Done[%d] = %v, want %v", i, res.Done[i], iv)
		}
	}
	// Completed intervals hold correct coefficients; the cancelled
	// region [300, 2401) is unset; the zero tail is exact zeros.
	full := Naive(a, b)
	for k := lo; k < hi; k++ {
		got, ok := res.At(k)
		switch {
		case k < 300 || k >= 2401:
			if !ok || got.Cmp(naiveCoeff(full, k)) != 0 {
				t.Fatalf("completed coefficient %d wrong: ok=%v got=%v", k, ok, got)
			}
		case ok:
			t.Fatalf("coefficient %d computed despite cancellation", k)
		}
	}
	// A fresh run completes and matches the naive reference everywhere.
	res2, err := Product(context.Background(), a, b, lo, hi, &Options{BlockSize: bs})
	if err != nil {
		t.Fatal(err)
	}
	checkInterval(t, res2, a, b, lo, hi)
	// The cancelled result must not have been mutated by the rerun.
	if len(res.Done) != 4 {
		t.Fatalf("cancelled result mutated: Done = %v", res.Done)
	}
}

func TestZeroPolyEmptyAndBeyondDegree(t *testing.T) {
	zero := NewPoly()
	p := NewPoly(big.NewInt(1), big.NewInt(2))

	res, err := Product(context.Background(), zero, p, 0, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 5; k++ {
		if c, ok := res.At(k); !ok || c.Sign() != 0 {
			t.Fatalf("zero product coefficient %d: ok=%v c=%v", k, ok, c)
		}
	}

	res, err = Product(context.Background(), p, p, 3, 3, nil)
	if err != nil || len(res.Coeffs) != 0 {
		t.Fatalf("empty interval: err=%v len=%d", err, len(res.Coeffs))
	}

	// Interval extending past the product degree: tail must be zero.
	res, err = Product(context.Background(), p, p, 0, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	full := Naive(p, p)
	for k := 0; k < 8; k++ {
		if c, _ := res.At(k); c.Cmp(naiveCoeff(full, k)) != 0 {
			t.Fatalf("coefficient %d: got %s, want %s", k, c, naiveCoeff(full, k))
		}
	}

	if _, err = Product(context.Background(), p, p, -1, 2, nil); err == nil {
		t.Fatal("expected error for negative lo")
	}
	if _, err = Product(context.Background(), p, p, 5, 2, nil); err == nil {
		t.Fatal("expected error for hi < lo")
	}
}

func TestBoundGuarantee(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for trial := 0; trial < 50; trial++ {
		a := randPoly(rng, 30, 1000)
		b := randPoly(rng, 30, 1000)
		bound := coeffBound(a, b)
		for k, c := range Naive(a, b) {
			if new(big.Int).Abs(c).Cmp(bound) > 0 {
				t.Fatalf("trial %d: |c_%d| exceeds bound %s", trial, k, bound)
			}
		}
		if bound.Sign() == 0 {
			continue
		}
		ch, err := newChain(bound)
		if err != nil {
			t.Fatal(err)
		}
		limit := new(big.Int).Lsh(bound, 1)
		if ch.mod.Cmp(limit) <= 0 {
			t.Fatalf("trial %d: M=%s not > 2*bound=%s", trial, ch.mod, limit)
		}
		// The chain must be minimal: dropping the last prime breaks M > 2B.
		without := new(big.Int).Quo(ch.mod, ch.bigP[len(ch.bigP)-1])
		if without.Cmp(limit) > 0 {
			t.Fatalf("trial %d: chain not minimal, M/p=%s > 2*bound", trial, without)
		}
	}
}
