package ratlsq

import (
	"fmt"
	"math/big"
)

// Verify independently checks a Solution against A and b, recomputing
// everything from scratch (no solver internals are trusted):
//
//  1. Normal equations: A^T A x = A^T b holds for Particular and MinNorm.
//  2. Null-space relations: A v = 0 for every NullBasis vector.
//  3. The null basis is linearly independent and complete:
//     rank(A) recomputed by RREF equals Rank, and Rank + len(NullBasis) = n.
//  4. MinNorm is orthogonal to the null space, hence the unique
//     minimum-norm solution.
//  5. Affine consistency: Particular - MinNorm lies in span(NullBasis).
func Verify(a *Matrix, b *Vector, sol *Solution, budget Budget) error {
	if a.Rows != b.Len() {
		return ErrDimension
	}
	n := a.Cols
	if sol.Particular.Len() != n || sol.MinNorm.Len() != n {
		return fmt.Errorf("ratlsq: verify: solution dimension mismatch")
	}
	at := a.Transpose()
	ata, err := at.Mul(a, budget)
	if err != nil {
		return err
	}
	atb, err := at.MulVec(b, budget)
	if err != nil {
		return err
	}
	checkNormal := func(x *Vector, name string) error {
		lhs, err := ata.MulVec(x, budget)
		if err != nil {
			return err
		}
		if !lhs.Equal(atb) {
			return fmt.Errorf("ratlsq: verify: %s violates the normal equations", name)
		}
		return nil
	}
	if err := checkNormal(sol.Particular, "particular solution"); err != nil {
		return err
	}
	if err := checkNormal(sol.MinNorm, "min-norm solution"); err != nil {
		return err
	}
	for i, v := range sol.NullBasis {
		if v.Len() != n {
			return fmt.Errorf("ratlsq: verify: null basis vector %d has wrong dimension", i)
		}
		av, err := a.MulVec(v, budget)
		if err != nil {
			return err
		}
		if !av.IsZero() {
			return fmt.Errorf("ratlsq: verify: null basis vector %d is not in null(A)", i)
		}
	}
	if len(sol.NullBasis) > 0 {
		nb := nullMatrix(sol.NullBasis)
		_, piv, err := rref(nb, budget)
		if err != nil {
			return err
		}
		if len(piv) != len(sol.NullBasis) {
			return fmt.Errorf("ratlsq: verify: null basis is linearly dependent")
		}
	}
	_, apiv, err := rref(a, budget)
	if err != nil {
		return err
	}
	if len(apiv) != sol.Rank {
		return fmt.Errorf("ratlsq: verify: reported rank %d, recomputed rank %d", sol.Rank, len(apiv))
	}
	if sol.Rank+len(sol.NullBasis) != n {
		return fmt.Errorf("ratlsq: verify: null basis incomplete: rank %d + nullity %d != %d unknowns",
			sol.Rank, len(sol.NullBasis), n)
	}
	for i, v := range sol.NullBasis {
		if dot(sol.MinNorm, v).Sign() != 0 {
			return fmt.Errorf("ratlsq: verify: min-norm solution not orthogonal to null vector %d", i)
		}
	}
	if len(sol.NullBasis) > 0 {
		diff := sub(sol.Particular, sol.MinNorm)
		if _, _, err := solveLinear(nullMatrix(sol.NullBasis), diff, budget); err != nil {
			return fmt.Errorf("ratlsq: verify: particular and min-norm solutions differ outside null(A)")
		}
	}
	return nil
}

func nullMatrix(basis []*Vector) *Matrix {
	m := NewMatrix(basis[0].Len(), len(basis))
	for j, v := range basis {
		for i := 0; i < v.Len(); i++ {
			m.set(i, j, v.At(i))
		}
	}
	return m
}

func dot(v, w *Vector) *big.Rat {
	acc := new(big.Rat)
	tmp := new(big.Rat)
	for i := 0; i < v.Len(); i++ {
		tmp.Mul(v.At(i), w.At(i))
		acc.Add(acc, tmp)
	}
	return acc
}

func sub(v, w *Vector) *Vector {
	out := v.Clone()
	tmp := new(big.Rat)
	for i := 0; i < out.Len(); i++ {
		tmp.Sub(v.At(i), w.At(i))
		out.Set(i, tmp)
	}
	return out
}
