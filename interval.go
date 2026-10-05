package ratbez

import "math/big"

// Iv is a closed interval with rational endpoints. A root isolating interval
// with Lo == Hi represents an exactly known rational root.
type Iv struct {
	Lo *big.Rat
	Hi *big.Rat
}

func (iv Iv) Width() *big.Rat { return new(big.Rat).Sub(iv.Hi, iv.Lo) }
func (iv Iv) IsPoint() bool   { return iv.Lo.Cmp(iv.Hi) == 0 }

// ContainsZero reports whether 0 lies in the interval.
func (iv Iv) ContainsZero() bool { return iv.Lo.Sign() <= 0 && iv.Hi.Sign() >= 0 }

func avg(a, b *big.Rat) *big.Rat {
	return new(big.Rat).Quo(new(big.Rat).Add(a, b), big.NewRat(2, 1))
}

func overlap(a, b Iv) bool {
	return a.Lo.Cmp(b.Hi) <= 0 && b.Lo.Cmp(a.Hi) <= 0
}

// IsolateRoots returns sorted, pairwise non-overlapping isolating intervals
// for the real roots of square-free p in the open interval (a, b).
// Each returned interval contains exactly one root.
func IsolateRoots(p Poly, a, b *big.Rat) []Iv {
	st := NewSturm(p)
	var out []Iv
	var rec func(lo, hi *big.Rat)
	rec = func(lo, hi *big.Rat) {
		cnt := st.CountOpen(lo, hi)
		if cnt == 0 {
			return
		}
		if cnt == 1 {
			out = append(out, Iv{lo, hi})
			return
		}
		m := avg(lo, hi)
		if p.Eval(m).Sign() == 0 {
			rec(lo, m)
			out = append(out, Iv{new(big.Rat).Set(m), new(big.Rat).Set(m)})
			rec(m, hi)
		} else {
			rec(lo, m)
			rec(m, hi)
		}
	}
	rec(a, b)
	return out
}

// RefineRoot halves an isolating interval, keeping the half that contains
// the unique root.
func RefineRoot(p Poly, st *Sturm, iv Iv) Iv {
	if iv.IsPoint() {
		return iv
	}
	m := avg(iv.Lo, iv.Hi)
	if p.Eval(m).Sign() == 0 {
		return Iv{new(big.Rat).Set(m), new(big.Rat).Set(m)}
	}
	if st.CountOpen(iv.Lo, m) == 1 {
		return Iv{iv.Lo, m}
	}
	return Iv{m, iv.Hi}
}

// IsolateUnit isolates all roots of square-free p in the open unit interval
// (0,1). Returned intervals I satisfy 0 < I.Lo <= I.Hi < 1 and consecutive
// intervals are separated by a positive gap (so a rational sample point can
// always be chosen between them).
func IsolateUnit(p Poly) []Iv {
	var a, b *big.Rat
	for k := int64(1); ; k++ {
		a = big.NewRat(-k, 1)
		b = big.NewRat(k+1, 1)
		if p.Eval(a).Sign() != 0 && p.Eval(b).Sign() != 0 {
			break
		}
	}
	st := NewSturm(p)
	ivs := IsolateRoots(p, a, b)
	zero := big.NewRat(0, 1)
	one := big.NewRat(1, 1)
	pAt0 := p.Eval(zero).Sign() == 0
	pAt1 := p.Eval(one).Sign() == 0
	var out []Iv
	for _, iv := range ivs {
		// Resolve intervals straddling 0.
		if iv.Lo.Cmp(zero) < 0 && iv.Hi.Cmp(zero) > 0 {
			if pAt0 {
				continue // the unique root in iv is exactly 0: an endpoint, not interior
			}
			for iv.Lo.Cmp(zero) < 0 && iv.Hi.Cmp(zero) > 0 {
				if st.CountOpen(iv.Lo, zero) == 1 {
					iv = Iv{iv.Lo, zero}
				} else {
					iv = Iv{zero, iv.Hi}
				}
			}
		}
		if iv.Hi.Cmp(zero) <= 0 {
			continue
		}
		if iv.Lo.Cmp(zero) == 0 {
			if iv.IsPoint() {
				continue // root exactly at 0
			}
			for iv.Lo.Cmp(zero) == 0 {
				iv = RefineRoot(p, st, iv)
			}
		}
		// Resolve intervals straddling 1.
		if iv.Lo.Cmp(one) < 0 && iv.Hi.Cmp(one) > 0 {
			if pAt1 {
				continue
			}
			for iv.Lo.Cmp(one) < 0 && iv.Hi.Cmp(one) > 0 {
				if st.CountOpen(iv.Lo, one) == 1 {
					iv = Iv{iv.Lo, one}
				} else {
					iv = Iv{one, iv.Hi}
				}
			}
		}
		if iv.Lo.Cmp(one) >= 0 {
			continue
		}
		if iv.Hi.Cmp(one) == 0 {
			if iv.IsPoint() {
				continue // root exactly at 1
			}
			for iv.Hi.Cmp(one) == 0 {
				iv = RefineRoot(p, st, iv)
			}
		}
		out = append(out, iv)
	}
	// Enforce positive gaps between consecutive intervals.
	for i := 0; i+1 < len(out); i++ {
		for out[i].Hi.Cmp(out[i+1].Lo) >= 0 {
			if !out[i].IsPoint() {
				out[i] = RefineRoot(p, st, out[i])
			} else {
				out[i+1] = RefineRoot(p, st, out[i+1])
			}
		}
	}
	return out
}

// --- exact rational interval arithmetic (used only to bound values) ---

func addIv(a, b Iv) Iv {
	return Iv{new(big.Rat).Add(a.Lo, b.Lo), new(big.Rat).Add(a.Hi, b.Hi)}
}

func mulIv(a, b Iv) Iv {
	p0 := new(big.Rat).Mul(a.Lo, b.Lo)
	p1 := new(big.Rat).Mul(a.Lo, b.Hi)
	p2 := new(big.Rat).Mul(a.Hi, b.Lo)
	p3 := new(big.Rat).Mul(a.Hi, b.Hi)
	lo, hi := p0, p0
	for _, v := range []*big.Rat{p1, p2, p3} {
		if v.Cmp(lo) < 0 {
			lo = v
		}
		if v.Cmp(hi) > 0 {
			hi = v
		}
	}
	return Iv{lo, hi}
}

// divIv computes a / b; the caller must ensure 0 is not in b.
func divIv(a, b Iv) Iv {
	inv := Iv{new(big.Rat).Inv(b.Hi), new(big.Rat).Inv(b.Lo)}
	return mulIv(a, inv)
}

// evalPolyIv evaluates p over the interval x with Horner interval arithmetic.
func evalPolyIv(p Poly, x Iv) Iv {
	acc := Iv{new(big.Rat).Set(p.Lead()), new(big.Rat).Set(p.Lead())}
	for i := p.Deg() - 1; i >= 0; i-- {
		acc = mulIv(acc, x)
		c := p.coeff(i)
		acc = addIv(acc, Iv{new(big.Rat).Set(c), new(big.Rat).Set(c)})
	}
	return acc
}
