package cpoly

import (
	"math/big"
	"testing"
)

// rat is a shorthand for building a rational from num/den.
func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

// cx builds a Cplx from integer parts.
func cx(re, im int64) Cplx { return NewCplxInt(re, im) }

// cxf builds a Cplx from fractional parts.
func cxf(reNum, reDen, imNum, imDen int64) Cplx {
	return NewCplx(rat(reNum, reDen), rat(imNum, imDen))
}

// sample builds a sparse polynomial with fractional complex coeffs:
// (1/2 + 1/3 i) x^5 + (-3/4) x^2 + (2 - 5/7 i)
func sample() *Poly {
	return NewPoly(map[int]Cplx{
		5: cxf(1, 2, 1, 3),
		2: NewCplx(rat(-3, 4), nil),
		0: cxf(2, 1, -5, 7),
	})
}

func sample2() *Poly {
	return NewPoly(map[int]Cplx{
		3: cxf(-2, 5, 4, 9),
		1: NewCplx(nil, rat(7, 11)),
		0: cx(1, -1),
	})
}

// independentConj conjugates a polynomial term by term using only the
// public Coeff accessor, i.e. without touching the Conj method.
func independentConj(p *Poly) *Poly {
	terms := make(map[int]Cplx)
	for _, d := range p.Degrees() {
		c := p.Coeff(d)
		terms[d] = NewCplx(c.Re, new(big.Rat).Neg(c.Im))
	}
	return NewPoly(terms)
}

// independentMul multiplies two polynomials by accumulating every
// pairwise term product through the public API only.
func independentMul(a, b *Poly) *Poly {
	acc := Zero()
	for _, da := range a.Degrees() {
		for _, db := range b.Degrees() {
			acc = acc.Add(Mono(da+db, a.Coeff(da).Mul(b.Coeff(db))))
		}
	}
	return acc
}

func TestConjDistributesOverAdd(t *testing.T) {
	p, q := sample(), sample2()
	lhs := p.Add(q).Conj()
	rhs := p.Conj().Add(q.Conj())
	if !lhs.Equal(rhs) {
		t.Fatalf("conj(p+q) != conj(p)+conj(q):\n lhs=%s\n rhs=%s", lhs, rhs)
	}
	// Cross-check against the fully independent term-by-term path.
	if indep := independentConj(p.Add(q)); !lhs.Equal(indep) {
		t.Fatalf("Conj disagrees with independent conjugation:\n got=%s\n want=%s", lhs, indep)
	}
}

func TestConjDistributesOverMul(t *testing.T) {
	p, q := sample(), sample2()
	lhs := p.Mul(q).Conj()
	rhs := p.Conj().Mul(q.Conj())
	if !lhs.Equal(rhs) {
		t.Fatalf("conj(p*q) != conj(p)*conj(q):\n lhs=%s\n rhs=%s", lhs, rhs)
	}
	// Independent term-by-term product, then independent conjugation.
	if indep := independentConj(independentMul(p, q)); !lhs.Equal(indep) {
		t.Fatalf("Conj(Mul) disagrees with independent computation:\n got=%s\n want=%s", lhs, indep)
	}
}

func TestConjInvolution(t *testing.T) {
	p := sample()
	if !p.Conj().Conj().Equal(p) {
		t.Fatalf("conj(conj(p)) != p")
	}
}

func TestSquaredModulusIsReal(t *testing.T) {
	p := sample()
	m := p.AbsSqPoly()
	for _, d := range m.Degrees() {
		if c := m.Coeff(d); !c.IsReal() {
			t.Fatalf("p*conj(p) has non-real coeff at x^%d: %s", d, c)
		}
	}
	// Independent check: c_k = sum_{i+j=k} a_i * conj(a_j).
	pc := p.Conj()
	want := make(map[int]*big.Rat)
	for _, di := range p.Degrees() {
		for _, dj := range pc.Degrees() {
			prod := p.Coeff(di).Mul(pc.Coeff(dj))
			k := di + dj
			if want[k] == nil {
				want[k] = new(big.Rat)
			}
			want[k].Add(want[k], prod.Re) // imag cancels pairwise
		}
	}
	for k, re := range want {
		if got := m.Coeff(k); got.Re.Cmp(re) != 0 {
			t.Fatalf("modulus coeff x^%d: got %s, want %s", k, got.Re, re)
		}
	}
	// Constant term equals |a_0|^2.
	if got, want := m.Coeff(0), p.Coeff(0).AbsSq(); got.Re.Cmp(want) != 0 {
		t.Fatalf("constant term: got %s, want |a_0|^2 = %s", got.Re, want)
	}
}

func TestImaginaryCancellation(t *testing.T) {
	p := sample()
	// p + conj(p) is purely real, p - conj(p) purely imaginary.
	sum := p.Add(p.Conj())
	for _, d := range sum.Degrees() {
		if c := sum.Coeff(d); !c.IsReal() {
			t.Fatalf("p+conj(p) not real at x^%d: %s", d, c)
		}
	}
	diff := p.Sub(p.Conj())
	for _, d := range diff.Degrees() {
		if c := diff.Coeff(d); c.Re.Sign() != 0 {
			t.Fatalf("p-conj(p) not imaginary at x^%d: %s", d, c)
		}
	}
	// (x + i)(x - i) = x^2 + 1: exact imaginary cancellation in a product.
	a := NewPoly(map[int]Cplx{1: cx(1, 0), 0: cx(0, 1)})
	b := NewPoly(map[int]Cplx{1: cx(1, 0), 0: cx(0, -1)})
	prod := a.Mul(b)
	want := NewPoly(map[int]Cplx{2: cx(1, 0), 0: cx(1, 0)})
	if !prod.Equal(want) {
		t.Fatalf("(x+i)(x-i): got %s, want %s", prod, want)
	}
	// Exact cancellation of a whole term: (1+i)x + (1-i)x = 2x.
	u := NewPoly(map[int]Cplx{1: cx(1, 1)})
	v := NewPoly(map[int]Cplx{1: cx(1, -1)})
	if got := u.Add(v); !got.Equal(Mono(1, cx(2, 0))) {
		t.Fatalf("term cancellation: got %s, want 2x", got)
	}
}

func TestZeroPolynomial(t *testing.T) {
	z := Zero()
	p := sample()
	if !z.IsZero() || z.Degree() != -1 || z.String() != "0" {
		t.Fatalf("bad zero poly: deg=%d str=%q", z.Degree(), z.String())
	}
	if !z.Add(p).Equal(p) || !p.Add(z).Equal(p) {
		t.Fatalf("zero is not additive identity")
	}
	if !z.Mul(p).IsZero() || !p.Mul(z).IsZero() {
		t.Fatalf("zero is not multiplicative annihilator")
	}
	if !z.Conj().IsZero() {
		t.Fatalf("conj(0) != 0")
	}
	if !p.Sub(p).IsZero() {
		t.Fatalf("p - p != 0")
	}
	// Constructing with explicit zero coefficients must drop them.
	w := NewPoly(map[int]Cplx{3: cx(0, 0), 1: cx(1, 0)})
	if w.Degree() != 1 || w.NumTerms() != 1 {
		t.Fatalf("zero coefficients not dropped: %s", w)
	}
}

func TestCacheAndReturnIsolation(t *testing.T) {
	p := sample()

	// Prime the caches.
	_ = p.Degree()
	s1 := p.String()
	if p.String() != s1 || p.Degree() != 5 {
		t.Fatalf("cached values inconsistent")
	}

	// Mutating a returned coefficient must not corrupt the polynomial.
	c := p.Coeff(5)
	c.Re.SetInt64(999)
	c.Im.SetInt64(-999)
	if again := p.Coeff(5); !again.Equal(cxf(1, 2, 1, 3)) {
		t.Fatalf("caller mutation leaked into polynomial: %s", again)
	}
	if p.String() != s1 {
		t.Fatalf("string changed after caller mutation: %q -> %q", s1, p.String())
	}

	// Mutating constructor inputs must not corrupt the polynomial either.
	re, im := rat(1, 2), rat(3, 4)
	q := Mono(7, NewCplx(re, im))
	re.SetInt64(1000)
	im.SetInt64(-1000)
	if got := q.Coeff(7); !got.Equal(cxf(1, 2, 3, 4)) {
		t.Fatalf("input aliasing leaked into polynomial: %s", got)
	}

	// Mutating the map handed to NewPoly must not corrupt it.
	m := map[int]Cplx{2: cx(1, 1)}
	r := NewPoly(m)
	m[2] = cx(5, 5)
	m[9] = cx(1, 0)
	if r.NumTerms() != 1 || !r.Coeff(2).Equal(cx(1, 1)) {
		t.Fatalf("map aliasing leaked into polynomial: %s", r)
	}

	// Results of operations must be independent of their operands.
	a := Mono(1, cx(1, 0))
	b := Mono(1, cx(0, 1))
	prod := a.Mul(b)
	prodCoeffsBefore := prod.String()
	tampered := prod.Coeff(2)
	tampered.Re.SetInt64(42)
	if prod.String() != prodCoeffsBefore {
		t.Fatalf("operation result corrupted by caller mutation")
	}
}

func TestSparseHighDegree(t *testing.T) {
	const hi = 1000000
	// (x^hi + 1)(x^hi - 1) = x^(2hi) - 1, computed sparsely.
	a := NewPoly(map[int]Cplx{hi: cx(1, 0), 0: cx(1, 0)})
	b := NewPoly(map[int]Cplx{hi: cx(1, 0), 0: cx(-1, 0)})
	prod := a.Mul(b)
	if prod.NumTerms() != 2 || prod.Degree() != 2*hi {
		t.Fatalf("sparse product wrong shape: terms=%d deg=%d", prod.NumTerms(), prod.Degree())
	}
	if !prod.Coeff(2*hi).Equal(cx(1, 0)) || !prod.Coeff(0).Equal(cx(-1, 0)) {
		t.Fatalf("sparse product wrong coeffs: %s", prod)
	}
	// A high-degree monomial times a conjugate pair stays tiny.
	m := Mono(hi, cx(3, 4))
	msq := m.AbsSqPoly()
	if msq.NumTerms() != 1 || !msq.Coeff(2*hi).Equal(cx(25, 0)) {
		t.Fatalf("high-degree modulus: got %s, want 25x^%d", msq, 2*hi)
	}
}

func TestTruncationIsExplicitOnly(t *testing.T) {
	p := sample() // degree 5
	q := sample2() // degree 3
	prod := p.Mul(q)
	if prod.Degree() != 8 {
		t.Fatalf("Mul truncated implicitly: degree %d, want 8", prod.Degree())
	}
	tr := prod.Truncate(5)
	if tr.Degree() > 5 {
		t.Fatalf("Truncate(5) left degree %d", tr.Degree())
	}
	for _, d := range tr.Degrees() {
		if !tr.Coeff(d).Equal(prod.Coeff(d)) {
			t.Fatalf("Truncate altered kept coeff at x^%d", d)
		}
	}
	if prod.NumTerms() != tr.NumTerms()+countAbove(prod, 5) {
		t.Fatalf("Truncate dropped wrong number of terms")
	}
	// Truncating everything away yields zero.
	if !prod.Truncate(-1).IsZero() && prod.Degree() >= 0 {
		// Truncate(-1) keeps nothing since degrees are >= 0.
		if !prod.Truncate(-1).IsZero() {
			t.Fatalf("Truncate(-1) should be zero")
		}
	}
}

func countAbove(p *Poly, maxDeg int) int {
	n := 0
	for _, d := range p.Degrees() {
		if d > maxDeg {
			n++
		}
	}
	return n
}

func TestReducedOutput(t *testing.T) {
	// 2/4 must print as 1/2; 6/8 i as 3/4 i.
	p := NewPoly(map[int]Cplx{
		1: NewCplx(rat(2, 4), rat(6, 8)),
		0: NewCplx(rat(10, 5), nil),
	})
	got := p.String()
	want := "(1/2 + 3/4i)x + 2"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Arithmetic stays reduced: 1/3 + 1/6 = 1/2.
	a := Mono(0, NewCplx(rat(1, 3), nil))
	b := Mono(0, NewCplx(rat(1, 6), nil))
	if got := a.Add(b).String(); got != "1/2" {
		t.Fatalf("got %q, want 1/2", got)
	}
}

func TestStringForms(t *testing.T) {
	cases := []struct {
		p    *Poly
		want string
	}{
		{Zero(), "0"},
		{Mono(0, cx(3, 0)), "3"},
		{Mono(1, cx(0, 1)), "ix"},
		{Mono(2, cx(0, -1)), "-ix^2"},
		{Mono(1, cx(1, 1)), "(1 + i)x"},
		{NewPoly(map[int]Cplx{2: cx(1, 0), 0: cx(-2, 3)}), "x^2 + (-2 + 3i)"},
	}
	for i, tc := range cases {
		if got := tc.p.String(); got != tc.want {
			t.Fatalf("case %d: got %q, want %q", i, got, tc.want)
		}
	}
}
