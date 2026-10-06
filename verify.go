package linsolve

import (
	"errors"
	"fmt"
	"math/big"
)

// dot computes sum Coeffs[i]*x[i] exactly. x entries may be nil (treated as
// zero) only if the corresponding coefficient is absent; here x must have
// length >= NumVars and no nil entries.
func dot(coeffs map[int]*big.Rat, x []*big.Rat) *big.Rat {
	acc := new(big.Rat)
	tmp := new(big.Rat)
	for c, v := range coeffs {
		acc.Add(acc, tmp.Mul(v, x[c]))
	}
	return acc
}

// checkSolution verifies that x satisfies every equation of the system.
func checkSolution(sys *System, x []*big.Rat, what string) error {
	if len(x) != sys.NumVars {
		return fmt.Errorf("linsolve: %s has length %d, want %d", what, len(x), sys.NumVars)
	}
	for i, v := range x {
		if v == nil {
			return fmt.Errorf("linsolve: %s has nil entry %d", what, i)
		}
	}
	for i, eq := range sys.Equations {
		if got := dot(eq.Coeffs, x); got.Cmp(eq.RHS) != 0 {
			return fmt.Errorf("linsolve: %s violates equation %d: lhs = %s, rhs = %s",
				what, i, got.RatString(), eq.RHS.RatString())
		}
	}
	return nil
}

// checkHomogeneous verifies that d satisfies A*d = 0 exactly.
func checkHomogeneous(sys *System, d []*big.Rat, what string) error {
	if len(d) != sys.NumVars {
		return fmt.Errorf("linsolve: %s has length %d, want %d", what, len(d), sys.NumVars)
	}
	for i, v := range d {
		if v == nil {
			return fmt.Errorf("linsolve: %s has nil entry %d", what, i)
		}
	}
	zero := new(big.Rat)
	for i, eq := range sys.Equations {
		if got := dot(eq.Coeffs, d); got.Cmp(zero) != 0 {
			return fmt.Errorf("linsolve: %s violates homogeneous equation %d: lhs = %s",
				what, i, got.RatString())
		}
	}
	return nil
}

// Verify independently checks a Result against the original system by
// substitution. It does not trust any solver internals:
//
//   - Infeasible: the certificate lambda is checked to satisfy
//     lambda*A = 0 (exactly, column by column) and lambda*b != 0.
//   - Unique: the solution is substituted into every equation.
//   - Parametric: the particular solution is substituted into every
//     equation, every direction is substituted into the homogeneous
//     system A*d = 0, and the parameterization is checked to be in
//     canonical form: FreeVars strictly increasing, Directions[i] has 1 in
//     coordinate FreeVars[i] and 0 in all other free coordinates, and
//     Particular is 0 on all free coordinates.
func Verify(sys *System, res *Result) error {
	if err := sys.Validate(); err != nil {
		return err
	}
	if res == nil {
		return errors.New("linsolve: nil result")
	}
	switch res.Status {
	case Infeasible:
		return verifyCertificate(sys, res.Certificate)
	case Unique:
		return checkSolution(sys, res.Solution, "unique solution")
	case Parametric:
		return verifyParametric(sys, res)
	}
	return fmt.Errorf("linsolve: unknown status %d", int(res.Status))
}

// verifyCertificate checks that cert is a Farkas-style infeasibility
// certificate: cert*A = 0 and cert*b != 0.
func verifyCertificate(sys *System, cert []*big.Rat) error {
	if len(cert) != len(sys.Equations) {
		return fmt.Errorf("linsolve: certificate has length %d, want %d",
			len(cert), len(sys.Equations))
	}
	for i, v := range cert {
		if v == nil {
			return fmt.Errorf("linsolve: certificate entry %d is nil", i)
		}
	}
	// Column by column: sum_e cert[e] * A[e][c] must be exactly 0.
	for c := 0; c < sys.NumVars; c++ {
		acc := new(big.Rat)
		tmp := new(big.Rat)
		for e, eq := range sys.Equations {
			if v, ok := eq.Coeffs[c]; ok {
				acc.Add(acc, tmp.Mul(cert[e], v))
			}
		}
		if acc.Sign() != 0 {
			return fmt.Errorf("linsolve: certificate fails: (cert*A)[%d] = %s != 0",
				c, acc.RatString())
		}
	}
	// The combination must yield a non-zero constant.
	rhs := new(big.Rat)
	tmp := new(big.Rat)
	for e, eq := range sys.Equations {
		rhs.Add(rhs, tmp.Mul(cert[e], eq.RHS))
	}
	if rhs.Sign() == 0 {
		return errors.New("linsolve: certificate fails: cert*b = 0, not a contradiction")
	}
	return nil
}

func verifyParametric(sys *System, res *Result) error {
	n := sys.NumVars
	if len(res.FreeVars) != len(res.Directions) {
		return fmt.Errorf("linsolve: %d free variables but %d directions",
			len(res.FreeVars), len(res.Directions))
	}
	isFree := make([]bool, n)
	for i, f := range res.FreeVars {
		if f < 0 || f >= n {
			return fmt.Errorf("linsolve: free variable %d out of range [0,%d)", f, n)
		}
		if i > 0 && f <= res.FreeVars[i-1] {
			return errors.New("linsolve: free variables are not strictly increasing")
		}
		isFree[f] = true
	}
	if err := checkSolution(sys, res.Particular, "particular solution"); err != nil {
		return err
	}
	zero := big.NewRat(0, 1)
	one := big.NewRat(1, 1)
	for f := range isFree {
		if !isFree[f] {
			continue
		}
		if res.Particular[f].Cmp(zero) != 0 {
			return fmt.Errorf("linsolve: particular solution is non-zero on free variable %d", f)
		}
	}
	for i, d := range res.Directions {
		if err := checkHomogeneous(sys, d, fmt.Sprintf("direction %d", i)); err != nil {
			return err
		}
		for j, f := range res.FreeVars {
			want := zero
			if j == i {
				want = one
			}
			if d[f].Cmp(want) != 0 {
				return fmt.Errorf("linsolve: direction %d has value %s on free variable %d, want %s",
					i, d[f].RatString(), f, want.RatString())
			}
		}
	}
	return nil
}
