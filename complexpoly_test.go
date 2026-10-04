package complexpoly

import (
	"math/big"
	"strings"
	"testing"
)

// p builds a polynomial from degree -> (re, im) rational strings.
func p(terms map[int][2]string) *Poly {
	q := New()
	for d, ri := range terms {
		q.SetTerm(d, C(ri[0], ri[1]))
	}
	return q
}

// mulTermwise is an independent, deliberately naive reference product used
// only by the tests to cross-check Mul.
func mulTermwise(a, b *Poly) *Poly {
	res := New()
	for _, da := range a.Degrees() {
		ca := a.Term(da)
		for _, db := range b.Degrees() {
			cb := b.Term(db)
			re := new(big.Rat).Sub(
				new(big.Rat).Mul(ca.Re, cb.Re),
				new(big.Rat).Mul(ca.Im, cb.Im),
			)
			im := new(big.Rat).Add(
				new(big.Rat).Mul(ca.Re, cb.Im),
				new(big.Rat).Mul(ca.Im, cb.Re),
			)
			res.addInto(da+db, Coeff{Re: re, Im: im})
		}
	}
	return res
}

func sampleA() *Poly {
	return p(map[int][2]string{
		0: {"1/2", "3"},
		2: {"-2/3", "1/4"},
		5: {"0", "-7/5"},
	})
}

func sampleB() *Poly {
	return p(map[int][2]string{
		1: {"4", "-1/6"},
		3: {"5/8", "2"},
		4: {"0", "0"},
	})
}

func TestMulMatchesIndependentTermwise(t *testing.T) {
	a, b := sampleA(), sampleB()
	got := Mul(a, b)
	want := mulTermwise(a, b)
	if !Equal(got, want) {
		t.Fatalf("Mul mismatch:\n got  %s\n want %s", got, want)
	}
}

func TestAddIsTermwise(t *testing.T) {
	a := p(map[int][2]string{0: {"1/2", "1"}, 3: {"2", "0"}})
	b := p(map[int][2]string{0: {"1/2", "-1"}, 1: {"1/3", "1/3"}})
	got := Add(a, b)
	want := p(map[int][2]string{
		0: {"1", "0"},
		1: {"1/3", "1/3"},
		3: {"2", "0"},
	})
	if !Equal(got, want) {
		t.Fatalf("Add mismatch: got %s want %s", got, want)
	}
}

func TestConjDistributesOverMul(t *testing.T) {
	a, b := sampleA(), sampleB()
	left := Conj(Mul(a, b))
	right := mulTermwise(Conj(a), Conj(b))
	if !Equal(left, right) {
		t.Fatalf("conj(a*b) != conj(a)*conj(b):\n %s\n %s", left, right)
	}
}

func TestConjDistributesOverAdd(t *testing.T) {
	a, b := sampleA(), sampleB()
	if !Equal(Conj(Add(a, b)), Add(Conj(a), Conj(b))) {
		t.Fatal("conj(a+b) != conj(a)+conj(b)")
	}
}

func TestSquaredModulusIsReal(t *testing.T) {
	a := sampleA()
	mod2 := Mul(a, Conj(a))
	ref := mulTermwise(a, Conj(a))
	if !Equal(mod2, ref) {
		t.Fatalf("a*conj(a) mismatch vs reference:\n %s\n %s", mod2, ref)
	}
	for _, d := range mod2.Degrees() {
		if im := mod2.Term(d).Im; im.Sign() != 0 {
			t.Fatalf("degree %d has nonzero imaginary part %s", d, im)
		}
	}
	// Hand-computed: (1+i*x)(1-i*x) = 1 + x^2.
	one := p(map[int][2]string{0: {"1", "0"}, 1: {"0", "1"}})
	got := Mul(one, Conj(one))
	want := p(map[int][2]string{0: {"1", "0"}, 2: {"1", "0"}})
	if !Equal(got, want) {
		t.Fatalf("(1+ix)(1-ix): got %s want %s", got, want)
	}
}

func TestImaginaryPartsCancel(t *testing.T) {
	// (2+3i)(2-3i) = 13, imaginary parts cancel exactly.
	a := p(map[int][2]string{0: {"2", "3"}})
	got := Mul(a, Conj(a))
	want := p(map[int][2]string{0: {"13", "0"}})
	if !Equal(got, want) {
		t.Fatalf("got %s want %s", got, want)
	}
	// Addition cancels imaginary parts term by term.
	b := p(map[int][2]string{4: {"1/2", "5/6"}})
	c := p(map[int][2]string{4: {"1/2", "-5/6"}})
	sum := Add(b, c)
	if sum.Term(4).Im.Sign() != 0 {
		t.Fatalf("imaginary part did not cancel: %s", sum)
	}
}

func TestZeroPolynomial(t *testing.T) {
	z := New()
	a := sampleA()
	if !z.IsZero() || z.Degree() != -1 || z.String() != "0" {
		t.Fatalf("bad zero polynomial: %s deg=%d", z, z.Degree())
	}
	if !Equal(Add(z, a), a) || !Equal(Add(a, z), a) {
		t.Fatal("0 + a != a")
	}
	if !Mul(z, a).IsZero() || !Mul(a, z).IsZero() {
		t.Fatal("0 * a != 0")
	}
	if !Conj(z).IsZero() {
		t.Fatal("conj(0) != 0")
	}
	// Terms that cancel must disappear entirely.
	b := p(map[int][2]string{2: {"1", "1"}})
	c := p(map[int][2]string{2: {"-1", "-1"}})
	if !Add(b, c).IsZero() {
		t.Fatal("cancelling terms left residue")
	}
}

func TestSparseHighDegree(t *testing.T) {
	const hi = 1 << 20
	a := p(map[int][2]string{0: {"1", "0"}, hi: {"3/2", "-1/2"}})
	got := Mul(a, a)
	want := p(map[int][2]string{
		0:      {"1", "0"},
		hi:     {"3", "-1"},
		2 * hi: {"2", "-3/2"}, // (3/2 - i/2)^2 = 9/4-1/4 - 2*(3/2)(1/2)i
	})
	if !Equal(got, want) {
		t.Fatalf("sparse square mismatch:\n got  %s\n want %s", got, want)
	}
	if n := len(got.Degrees()); n != 3 {
		t.Fatalf("expected 3 sparse terms, got %d", n)
	}
}

func TestMulTruncExplicitDegree(t *testing.T) {
	a := p(map[int][2]string{1: {"1", "1"}, 2: {"2", "0"}})
	b := p(map[int][2]string{1: {"1", "-1"}, 3: {"1/2", "0"}})
	full := Mul(a, b)
	tr := MulTrunc(a, b, 3)
	for _, d := range full.Degrees() {
		got := tr.Term(d)
		want := full.Term(d)
		if d <= 3 {
			if !got.eq(want) {
				t.Fatalf("degree %d: got (%s,%s) want (%s,%s)",
					d, got.Re, got.Im, want.Re, want.Im)
			}
		} else if !got.isZero() {
			t.Fatalf("degree %d should have been truncated", d)
		}
	}
	// Truncating below every product term yields the zero polynomial.
	if !MulTrunc(a, b, 1).IsZero() {
		t.Fatal("expected zero after truncating above degree 1")
	}
}

func TestCacheIsolation(t *testing.T) {
	c := NewCache()
	a, b := sampleA(), sampleB()
	first := c.Mul(a, b)
	snapshot := first.Clone()
	// The caller corrupts its copy in every way it can.
	first.SetTerm(0, C("999", "999"))
	first.SetTerm(2, C("0", "0"))
	tm := first.Term(1)
	tm.Re.SetInt64(-1)
	second := c.Mul(a, b)
	if !Equal(second, snapshot) {
		t.Fatalf("cached result was polluted by caller:\n got  %s\n want %s", second, snapshot)
	}
	if !Equal(second, Mul(a, b)) {
		t.Fatal("cached result disagrees with direct Mul")
	}
}

func TestInputsAreNotMutated(t *testing.T) {
	a, b := sampleA(), sampleB()
	ac, bc := a.Clone(), b.Clone()
	Mul(a, b)
	Add(a, b)
	Conj(a)
	MulTrunc(a, b, 2)
	if !Equal(a, ac) || !Equal(b, bc) {
		t.Fatal("operation mutated its input")
	}
}

func TestReducedOutput(t *testing.T) {
	// Unreduced input is normalised on the way in.
	a := New().SetTerm(0, C("2/4", "6/9"))
	if got := a.Term(0); got.Re.RatString() != "1/2" || got.Im.RatString() != "2/3" {
		t.Fatalf("coefficient not reduced: %s", a)
	}
	// Sums reduce too: 1/6 + 1/3 = 1/2, 1/2 + 1/2 = 1.
	b := p(map[int][2]string{0: {"1/6", "1/2"}})
	c := p(map[int][2]string{0: {"1/3", "1/2"}})
	sum := Add(b, c)
	s := sum.String()
	if !strings.Contains(s, "(1/2+1i)") {
		t.Fatalf("unexpected rendering: %s", s)
	}
	// Product reduces: (1/2 + i/2)^2 = i/2.
	d := p(map[int][2]string{0: {"1/2", "1/2"}})
	sq := Mul(d, d)
	if got := sq.Term(0); got.Re.Sign() != 0 || got.Im.RatString() != "1/2" {
		t.Fatalf("bad square: %s", sq)
	}
}
