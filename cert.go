package affine

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Term is one coefficient of a certified affine form.
type Term struct {
	ID   int
	Coef float64
}

// Certificate is a checkable bound witness: it claims that the value
// of Center + sum Terms[i].Coef * e_i, with every e_i in [-1, 1],
// always lies in [Lo, Hi]. Check verifies the claim independently
// using exact rational arithmetic.
type Certificate struct {
	Center float64
	Terms  []Term
	Lo, Hi float64
}

// Certify exports f's tight affine range as a checkable certificate.
func (f Form) Certify() Certificate {
	ids := make([]int, 0, len(f.coefs))
	for id := range f.coefs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	cert := Certificate{Center: f.c}
	for _, id := range ids {
		cert.Terms = append(cert.Terms, Term{ID: id, Coef: f.coefs[id]})
	}
	cert.Lo, cert.Hi = f.Range()
	return cert
}

func rat(v float64) *big.Rat {
	r := new(big.Rat)
	if err := r.UnmarshalText([]byte(fmt.Sprintf("%g", v))); err != nil {
		// %g is always exact-parseable; fall back to float conversion.
		r.SetFloat64(v)
	}
	return r
}

// Check independently verifies a certificate with exact rational
// arithmetic: (1) the independent-interval rule, requiring
// [Lo,Hi] to contain Center +- sum|Coef|; (2) evaluation on a small
// rational grid over each noise symbol, confirming every sampled
// value lies inside [Lo, Hi].
func Check(cert Certificate) error {
	if len(cert.Terms) == 0 {
		if cert.Lo > cert.Center || cert.Hi < cert.Center {
			return errors.New("affine: constant certificate bounds do not contain center")
		}
		return nil
	}
	c := rat(cert.Center)
	lo, hi := rat(cert.Lo), rat(cert.Hi)
	if lo.Cmp(hi) > 0 {
		return errors.New("affine: certificate Lo > Hi")
	}

	// Rule 1: independent interval arithmetic.
	radius := new(big.Rat)
	for _, t := range cert.Terms {
		radius.Add(radius, new(big.Rat).Abs(rat(t.Coef)))
	}
	ilo := new(big.Rat).Sub(c, radius)
	ihi := new(big.Rat).Add(c, radius)
	if lo.Cmp(ilo) > 0 {
		return errors.New("affine: Lo exceeds independent-interval lower bound")
	}
	if hi.Cmp(ihi) < 0 {
		return errors.New("affine: Hi below independent-interval upper bound")
	}

	// Rule 2: small rational grid over the noise symbols.
	const maxCombos = 1 << 20
	grid := []*big.Rat{
		big.NewRat(-1, 1), big.NewRat(-1, 2), big.NewRat(0, 1),
		big.NewRat(1, 2), big.NewRat(1, 1),
	}
	fits := func(g []*big.Rat) bool {
		combos := 1
		for range cert.Terms {
			combos *= len(g)
			if combos > maxCombos {
				return false
			}
		}
		return true
	}
	if !fits(grid) {
		grid = []*big.Rat{big.NewRat(-1, 1), big.NewRat(0, 1), big.NewRat(1, 1)}
	}
	if !fits(grid) {
		return errors.New("affine: too many symbols for grid verification")
	}
	n := len(cert.Terms)
	idx := make([]int, n)
	for {
		v := new(big.Rat).Set(c)
		for i, t := range cert.Terms {
			term := new(big.Rat).Mul(rat(t.Coef), grid[idx[i]])
			v.Add(v, term)
		}
		if v.Cmp(lo) < 0 || v.Cmp(hi) > 0 {
			return fmt.Errorf("affine: grid point %s escapes [%s, %s]",
				v.RatString(), lo.RatString(), hi.RatString())
		}
		i := n - 1
		for ; i >= 0; i-- {
			idx[i]++
			if idx[i] < len(grid) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			break
		}
	}
	return nil
}
