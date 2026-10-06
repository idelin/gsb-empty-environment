// Package linsolve solves sparse linear systems over the rational numbers
// using exact arithmetic (math/big.Rat). No floating point is used anywhere,
// so rank decisions are exact and never depend on a numerical threshold.
//
// The solver reports one of three outcomes:
//
//   - Infeasible:  together with a certificate, i.e. coefficients of a linear
//     combination of the input equations that yields 0 = c with c != 0.
//   - Unique:      the unique solution vector.
//   - Parametric:  a particular solution plus one direction vector per free
//     variable. Free variables are the non-pivot columns, listed by
//     increasing variable index; parameter t_i corresponds to FreeVars[i].
//
// Every outcome can be checked independently of the solver with Verify,
// which re-substitutes the answer into the original system.
package linsolve

import (
	"errors"
	"fmt"
	"math/big"
)

// Equation is one sparse linear equation:
//
//	sum_i Coeffs[i] * x_i = RHS
//
// Keys of Coeffs are variable indices in [0, NumVars). Entries with a zero
// value are treated as absent.
type Equation struct {
	Coeffs map[int]*big.Rat
	RHS    *big.Rat
}

// System is a sparse linear system A x = b.
type System struct {
	NumVars   int
	Equations []Equation
}

// Validate checks that the system is well formed.
func (s *System) Validate() error {
	if s.NumVars < 0 {
		return errors.New("linsolve: negative NumVars")
	}
	for i, eq := range s.Equations {
		if eq.RHS == nil {
			return fmt.Errorf("linsolve: equation %d has nil RHS", i)
		}
		for c, v := range eq.Coeffs {
			if c < 0 || c >= s.NumVars {
				return fmt.Errorf("linsolve: equation %d references variable %d out of range [0,%d)", i, c, s.NumVars)
			}
			if v == nil {
				return fmt.Errorf("linsolve: equation %d has nil coefficient for variable %d", i, c)
			}
		}
	}
	return nil
}

// Status is the outcome kind of a Solve call.
type Status int

const (
	// Infeasible means the system has no solution; Result.Certificate is set.
	Infeasible Status = iota
	// Unique means the system has exactly one solution; Result.Solution is set.
	Unique
	// Parametric means the system has infinitely many solutions;
	// Result.Particular, Result.Directions and Result.FreeVars are set.
	Parametric
)

func (s Status) String() string {
	switch s {
	case Infeasible:
		return "Infeasible"
	case Unique:
		return "Unique"
	case Parametric:
		return "Parametric"
	}
	return "Unknown"
}

// Result is the outcome of Solve. Exactly the fields relevant to Status are
// non-nil.
type Result struct {
	Status Status

	// Certificate is set when Status == Infeasible. It has one entry per
	// input equation and satisfies:
	//
	//	sum_e Certificate[e] * A[e][:] = 0   (the zero vector)
	//	sum_e Certificate[e] * b[e]    != 0
	//
	// i.e. the corresponding linear combination of equations is the
	// contradiction 0 = (nonzero constant).
	Certificate []*big.Rat

	// Solution is set when Status == Unique. It has one entry per variable.
	Solution []*big.Rat

	// Particular is set when Status == Parametric. It has one entry per
	// variable and is the solution obtained by setting every free variable
	// to zero.
	Particular []*big.Rat

	// Directions is set when Status == Parametric. Directions[i] is the
	// direction vector of parameter t_i, which corresponds to the free
	// variable FreeVars[i]. The general solution is
	//
	//	x = Particular + sum_i t_i * Directions[i],   t_i in Q.
	Directions [][]*big.Rat

	// FreeVars lists the free (non-pivot) variable indices in strictly
	// increasing order. FreeVars[i] is the variable that parameter t_i
	// (and Directions[i]) corresponds to.
	FreeVars []int
}
