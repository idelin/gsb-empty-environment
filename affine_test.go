package affine

import (
	"math"
	"math/rand"
	"testing"
)

func rangeOf(f Form) (float64, float64) { return f.Range() }

func TestSelfSubtractionCancels(t *testing.T) {
	env := NewEnv(0)
	x := env.Var()
	d := x.Sub(x)
	lo, hi := rangeOf(d)
	if lo != 0 || hi != 0 {
		t.Fatalf("x - x = [%v, %v], want [0, 0]", lo, hi)
	}
	if len(d.coefs) != 0 {
		t.Fatalf("x - x keeps %d coefficients, want 0", len(d.coefs))
	}
	// 2x - x - x must also vanish exactly.
	e := x.Scale(2).Sub(x).Sub(x)
	if lo, hi := rangeOf(e); lo != 0 || hi != 0 {
		t.Fatalf("2x - x - x = [%v, %v], want [0, 0]", lo, hi)
	}
}

func TestSharedSubexpression(t *testing.T) {
	env := NewEnv(0)
	x := env.Var()
	// Shared: s used twice keeps correlation, s - s == 0.
	s := x.Shift(1)
	shared := s.Sub(s)
	if lo, hi := rangeOf(shared); lo != 0 || hi != 0 {
		t.Fatalf("shared s - s = [%v, %v], want [0, 0]", lo, hi)
	}
	// Independent reconstruction loses the correlation.
	s1 := x.Shift(1)
	s2 := env.Var().Shift(1)
	indep := s1.Sub(s2)
	lo, hi := rangeOf(indep)
	if !approx(lo, -2) || !approx(hi, 2) {
		t.Fatalf("independent s1 - s2 = [%v, %v], want [-2, 2]", lo, hi)
	}
	// x*x: one remainder symbol, range [-1,1] enclosing true [0,1].
	sq := x.Mul(x)
	if sq.NumRemainder() != 1 {
		t.Fatalf("x*x has %d remainders, want 1", sq.NumRemainder())
	}
	if lo, hi := rangeOf(sq); !approx(lo, -1) || !approx(hi, 1) {
		t.Fatalf("x*x = [%v, %v], want [-1, 1]", lo, hi)
	}
}

func TestRemainderBudgetMerge(t *testing.T) {
	env := NewEnv(1)
	// Shift the variables so the linear parts survive multiplication
	// and each product genuinely appends a remainder symbol.
	x := env.Var().Shift(2)
	y := env.Var().Shift(2)
	z := env.Var().Shift(2)
	w := env.Var().Shift(2)
	f := x.Mul(y).Mul(z).Mul(w)
	if f.NumRemainder() > 1 {
		t.Fatalf("budget 1 exceeded: %d remainders", f.NumRemainder())
	}
	// Conservative merge must still enclose the true function.
	lo, hi := rangeOf(f)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		a, b, c, d := rnd(rng), rnd(rng), rnd(rng), rnd(rng)
		v := (a + 2) * (b + 2) * (c + 2) * (d + 2)
		if v < lo || v > hi {
			t.Fatalf("merged form [%v, %v] misses %v", lo, hi, v)
		}
	}
	// Unlimited budget keeps symbols separate and is at least as tight.
	env2 := NewEnv(0)
	x2 := env2.Var().Shift(2)
	y2 := env2.Var().Shift(2)
	z2 := env2.Var().Shift(2)
	w2 := env2.Var().Shift(2)
	f2 := x2.Mul(y2).Mul(z2).Mul(w2)
	if f2.NumRemainder() != 3 {
		t.Fatalf("unlimited: %d remainders, want 3", f2.NumRemainder())
	}
	lo2, hi2 := rangeOf(f2)
	if !approx(lo2, lo) || !approx(hi2, hi) {
		t.Fatalf("unlimited [%v,%v] differs from merged [%v,%v]", lo2, hi2, lo, hi)
	}
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	m := math.Abs(a) + math.Abs(b) + 1
	return d <= 1e-12*m
}

func TestDivZeroCrossingRejected(t *testing.T) {
	env := NewEnv(0)
	x := env.Var()
	y := env.Var()
	if _, err := x.Div(y); err != ErrZeroDivisor {
		t.Fatalf("div by [-1,1]: err = %v, want ErrZeroDivisor", err)
	}
	// Divisor shifted to touch zero is still rejected.
	z := env.Var().Shift(1) // [0, 2]
	if _, err := x.Div(z); err != ErrZeroDivisor {
		t.Fatalf("div by [0,2]: err = %v, want ErrZeroDivisor", err)
	}
	// Piecewise domain of a zero-crossing divisor splits at zero.
	parts := SplitDomain(-2, 3)
	if len(parts) != 2 || parts[0] != [2]float64{-2, 0} || parts[1] != [2]float64{0, 3} {
		t.Fatalf("SplitDomain(-2,3) = %v", parts)
	}
	if parts := SplitDomain(1, 4); len(parts) != 1 {
		t.Fatalf("SplitDomain(1,4) = %v", parts)
	}
}

func TestDivSound(t *testing.T) {
	env := NewEnv(4)
	x := env.Var()
	y := env.Var().Scale(0.5).Shift(2) // [1.5, 2.5]
	q, err := x.Div(y)
	if err != nil {
		t.Fatalf("div: %v", err)
	}
	lo, hi := rangeOf(q)
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 10000; i++ {
		a, b := rnd(rng), rnd(rng)
		v := a / (0.5*b + 2)
		if v < lo || v > hi {
			t.Fatalf("quotient range [%v, %v] misses %v", lo, hi, v)
		}
	}
	// Negative divisor, no zero crossing.
	envN := NewEnv(4)
	u := envN.Var()
	vv := envN.Var().Scale(0.5).Shift(-2) // [-2.5, -1.5]
	if _, err := u.Div(vv); err != nil {
		t.Fatalf("negative divisor: %v", err)
	}
}

func TestCertificateCheck(t *testing.T) {
	env := NewEnv(4)
	x, y := env.Var(), env.Var()
	f := x.Shift(1).Mul(y.Shift(-2)).Shift(0.5)
	cert := f.Certify()
	if err := Check(cert); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}
	// Tightened (unsound) bounds must be caught.
	bad := cert
	bad.Lo += 0.25
	if err := Check(bad); err == nil {
		t.Fatal("inflated Lo accepted")
	}
	bad2 := cert
	bad2.Hi -= 0.25
	if err := Check(bad2); err == nil {
		t.Fatal("deflated Hi accepted")
	}
	// A shifted center escapes both the interval rule and the grid.
	bad3 := cert
	bad3.Center += 10
	if err := Check(bad3); err == nil {
		t.Fatal("certificate with shifted center accepted")
	}
}

func TestCertificateConstant(t *testing.T) {
	env := NewEnv(0)
	cert := env.Const(3.5).Certify()
	if err := Check(cert); err != nil {
		t.Fatalf("constant certificate: %v", err)
	}
	if cert.Lo != 3.5 || cert.Hi != 3.5 {
		t.Fatalf("constant cert = [%v, %v]", cert.Lo, cert.Hi)
	}
}

func TestSoundnessRandomExpr(t *testing.T) {
	env := NewEnv(2)
	x, y := env.Var(), env.Var()
	// ((x+1)*(y-1) + x*y) / (x + 3)
	num := x.Shift(1).Mul(y.Shift(-1)).Add(x.Mul(y))
	den := x.Shift(3)
	f, err := num.Div(den)
	if err != nil {
		t.Fatalf("div: %v", err)
	}
	lo, hi := rangeOf(f)
	if math.IsNaN(lo) || math.IsNaN(hi) {
		t.Fatal("NaN range")
	}
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 20000; i++ {
		a, b := rnd(rng), rnd(rng)
		v := ((a+1)*(b-1) + a*b) / (a + 3)
		if v < lo || v > hi {
			t.Fatalf("range [%v, %v] misses %v (a=%v b=%v)", lo, hi, v, a, b)
		}
	}
	if err := Check(f.Certify()); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

func rnd(r *rand.Rand) float64 { return 2*r.Float64() - 1 }
