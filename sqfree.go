package rootfind

// SqFreeFactor is one component of the square-free factorization
// p = c * prod F_i^Exp_i with F monic, square-free and pairwise coprime.
type SqFreeFactor struct {
	F   Poly
	Exp int
}

// SqFree computes Yun's square-free factorization of p (characteristic 0).
// The leading coefficient of p is discarded; only roots matter here.
// A root of F with exponent Exp is a root of p of multiplicity exactly Exp.
func SqFree(p Poly) []SqFreeFactor {
	if p.IsConst() {
		return nil
	}
	g := GCD(p, p.Deriv())
	w := p.Quo(g).Monic()
	var out []SqFreeFactor
	for i := 1; !w.IsConst(); i++ {
		y := GCD(w, g)
		z := w.Quo(y)
		if !z.IsConst() {
			out = append(out, SqFreeFactor{F: z.Monic(), Exp: i})
		}
		w = y
		g = g.Quo(y)
	}
	return out
}

// SquareFreePart returns the monic square-free part of p: the product of
// its distinct irreducible factors, i.e. p / gcd(p, p'). It has exactly
// the same distinct real roots as p, all simple.
func SquareFreePart(p Poly) Poly {
	if p.IsConst() {
		return p
	}
	return p.Quo(GCD(p, p.Deriv())).Monic()
}
