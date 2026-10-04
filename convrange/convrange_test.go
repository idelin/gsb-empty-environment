package convrange

import (
	"context"
	"math/big"
	"math/rand"
	"testing"
)

// naiveMul is the reference big-integer convolution over nonzero term pairs.
func naiveMul(a, b []*big.Int) []*big.Int {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make([]*big.Int, len(a)+len(b)-1)
	for i := range out {
		out[i] = new(big.Int)
	}
	for i, ai := range a {
		if ai.Sign() == 0 {
			continue
		}
		for j, bj := range b {
			if bj.Sign() == 0 {
				continue
			}
			out[i+j].Add(out[i+j], new(big.Int).Mul(ai, bj))
		}
	}
	return out
}

func checkRange(t *testing.T, a, b []*big.Int, lo, hi int, opts *Options) {
	t.Helper()
	full := naiveMul(a, b)
	res, err := MulRange(context.Background(), a, b, lo, hi, opts)
	if err != nil {
		t.Fatalf("MulRange(%d,%d): %v", lo, hi, err)
	}
	if len(res.Coeffs) != hi-lo {
		t.Fatalf("got %d coeffs, want %d", len(res.Coeffs), hi-lo)
	}
	for k := lo; k < hi; k++ {
		var want *big.Int
		if k < len(full) {
			want = full[k]
		} else {
			want = new(big.Int)
		}
		got := res.Coeffs[k-lo]
		if got == nil {
			t.Fatalf("coeff %d is nil", k)
		}
		if got.Cmp(want) != 0 {
			t.Fatalf("coeff %d: got %s, want %s", k, got, want)
		}
	}
}

func randPoly(r *rand.Rand, n, coeffBits int) []*big.Int {
	p := make([]*big.Int, n)
	for i := range p {
		v := new(big.Int).Rand(r, new(big.Int).Lsh(big.NewInt(1), uint(coeffBits)))
		if r.Intn(2) == 0 {
			v.Neg(v)
		}
		p[i] = v
	}
	return p
}

func TestSmallRandomAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		n := 1 + r.Intn(40)
		m := 1 + r.Intn(40)
		a := randPoly(r, n, 8+r.Intn(40))
		b := randPoly(r, m, 8+r.Intn(40))
		deg := n + m - 1
		lo := r.Intn(deg)
		hi := lo + r.Intn(deg-lo+1)
		checkRange(t, a, b, lo, hi, &Options{BlockSize: 1 + r.Intn(17)})
	}
}

func TestNegativeCoefficients(t *testing.T) {
	a := []*big.Int{big.NewInt(-3), big.NewInt(5), big.NewInt(-7), big.NewInt(2)}
	b := []*big.Int{big.NewInt(4), big.NewInt(-11), big.NewInt(-1), big.NewInt(6), big.NewInt(-2)}
	checkRange(t, a, b, 0, len(a)+len(b)-1, nil)
	checkRange(t, a, b, 2, 6, nil)
	checkRange(t, a, b, 7, 8, nil) // top coefficient only
}

func TestSparseHighDegree(t *testing.T) {
	// Degrees far beyond any feasible dense full convolution.
	a := make([]*big.Int, 50001)
	b := make([]*big.Int, 40001)
	for i := range a {
		a[i] = new(big.Int)
	}
	for i := range b {
		b[i] = new(big.Int)
	}
	a[50000].SetInt64(3)
	a[12345].SetInt64(-7)
	a[0].SetInt64(2)
	b[40000].SetInt64(-5)
	b[7].SetInt64(11)
	b[0].SetInt64(-1)
	// Products land at 90000, 52345, 50007, 40000, 12352, 12345, 7, 0.
	checkRange(t, a, b, 89995, 90001, nil)
	checkRange(t, a, b, 12340, 12355, nil)
	checkRange(t, a, b, 0, 10, nil)
	checkRange(t, a, b, 52340, 52350, &Options{BlockSize: 3})
}

func TestLargeCoefficientsManyPrimes(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	// 260-bit coefficients force ~9 primes (bound ~ 260+260+6 bits).
	a := randPoly(r, 12, 260)
	b := randPoly(r, 9, 260)
	checkRange(t, a, b, 0, len(a)+len(b)-1, &Options{BlockSize: 4})
	checkRange(t, a, b, 5, 15, nil)
}

func TestBoundSaturation(t *testing.T) {
	// All coefficients equal to K: c_k = t_k * K^2 hits the proven bound
	// exactly at the tent peak, so any off-by-one in M > 2*bound would wrap.
	K := new(big.Int).Lsh(big.NewInt(1), 100)
	mk := func(n int) []*big.Int {
		p := make([]*big.Int, n)
		for i := range p {
			p[i] = new(big.Int).Set(K)
		}
		return p
	}
	a, b := mk(64), mk(48)
	checkRange(t, a, b, 0, 111, &Options{BlockSize: 10})
	// Same with negative K on one side: c_k = -t_k*K^2 hits the lower bound.
	nb := mk(48)
	for _, c := range nb {
		c.Neg(c)
	}
	checkRange(t, a, nb, 0, 111, nil)
}

func TestCancellationCompletedIntervals(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	a := randPoly(r, 300, 30)
	b := randPoly(r, 200, 30)
	full := naiveMul(a, b)

	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	opts := &Options{
		BlockSize: 10,
		OnBlockDone: func(lo, hi int) {
			calls++
			if calls == 2 { // stop after the second block
				cancel()
			}
		},
	}
	res, err := MulRange(ctx, a, b, 5, 105, opts)
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// Exactly the first two blocks of [5,105) must be completed.
	want := []Range{{5, 15}, {15, 25}}
	if len(res.Completed) != len(want) {
		t.Fatalf("Completed = %v, want %v", res.Completed, want)
	}
	for i, rg := range res.Completed {
		if rg != want[i] {
			t.Fatalf("Completed[%d] = %v, want %v", i, rg, want[i])
		}
	}
	// Completed coefficients are exact; unfinished ones are nil.
	for k := 5; k < 105; k++ {
		got := res.Coeffs[k-5]
		if k < 25 {
			if got == nil || got.Cmp(full[k]) != 0 {
				t.Fatalf("coeff %d: got %v, want %s", k, got, full[k])
			}
		} else if got != nil {
			t.Fatalf("coeff %d should be nil (unfinished), got %s", k, got)
		}
	}
	if res.Coeff(6).Cmp(full[6]) != 0 || res.Coeff(30) != nil {
		t.Fatal("Coeff accessor inconsistent with completed state")
	}
}

func TestEdgeCases(t *testing.T) {
	a := []*big.Int{big.NewInt(1), big.NewInt(2)}
	b := []*big.Int{big.NewInt(3), big.NewInt(4), big.NewInt(5)}
	// hi beyond degree: tail must be zero-filled.
	checkRange(t, a, b, 0, 10, nil)
	// Empty interval.
	res, err := MulRange(context.Background(), a, b, 3, 3, nil)
	if err != nil || len(res.Coeffs) != 0 {
		t.Fatalf("empty range: res=%v err=%v", res, err)
	}
	// Zero polynomial.
	z := []*big.Int{big.NewInt(0), big.NewInt(0)}
	checkRange(t, z, b, 0, 4, nil)
	// Empty polynomial.
	res, err = MulRange(context.Background(), nil, b, 0, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range res.Coeffs {
		if c.Sign() != 0 {
			t.Fatalf("coeff %d = %s, want 0", i, c)
		}
	}
	// Invalid ranges.
	if _, err := MulRange(context.Background(), a, b, -1, 2, nil); err == nil {
		t.Fatal("negative lo should fail")
	}
	if _, err := MulRange(context.Background(), a, b, 4, 2, nil); err == nil {
		t.Fatal("lo > hi should fail")
	}
}

func TestMaxTermsShape(t *testing.T) {
	// n=5, m=3: t(k) = 1,2,3,3,3,2,1 for k=0..6.
	want := []int{1, 2, 3, 3, 3, 2, 1}
	for k, w := range want {
		if got := termsAt(k, 5, 3); got != w {
			t.Fatalf("termsAt(%d) = %d, want %d", k, got, w)
		}
	}
	if got := maxTerms(2, 5, 5, 3); got != 3 {
		t.Fatalf("maxTerms = %d, want 3", got)
	}
	if got := maxTerms(5, 7, 5, 3); got != 2 {
		t.Fatalf("maxTerms = %d, want 2", got)
	}
	if got := maxTerms(0, 1, 5, 3); got != 1 {
		t.Fatalf("maxTerms = %d, want 1", got)
	}
}

// TestInsufficientModulusWraps demonstrates that the bound M > 2*bound is
// load-bearing: with too few primes the centered lift silently wraps, which
// is exactly what selectPrimes prevents.
func TestInsufficientModulusWraps(t *testing.T) {
	p := uint64(1<<31 - 1)
	// True coefficient c = p (|c| = p > p/2 = M/2): residues are all 0,
	// so the lift returns 0 instead of p. Wraparound confirmed.
	crt := newCRT([]uint64{p})
	got := crt.lift([]uint64{0})
	if got.Sign() != 0 {
		t.Fatalf("expected wrapped value 0, got %s", got)
	}
	// With the bound respected (M = p*q > 2p), the same coefficient is exact.
	q := prevPrime(p - 1)
	crt2 := newCRT([]uint64{p, q})
	c := big.NewInt(int64(p))
	r := []uint64{uint64(0), new(big.Int).Mod(c, big.NewInt(int64(q))).Uint64()}
	if got := crt2.lift(r); got.Cmp(c) != 0 {
		t.Fatalf("with sufficient modulus: got %s, want %s", got, c)
	}
	// And selectPrimes never under-provisions: M > 2*bound must hold.
	bound := new(big.Int).Mul(big.NewInt(1e9), big.NewInt(1e9))
	primes := selectPrimes(bound)
	M := big.NewInt(1)
	for _, p := range primes {
		M.Mul(M, big.NewInt(int64(p)))
	}
	if M.Cmp(new(big.Int).Lsh(bound, 1)) <= 0 {
		t.Fatalf("selectPrimes: M=%s <= 2*bound=%s", M, bound)
	}
}
