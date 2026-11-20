// Package prescreen implements an importable read-admission component for a
// cold-storage archive host.
//
// Block numbers are 48-bit vectors over GF(2). Request slices, grant slices
// and revocation slices are affine subspaces expressed as systems of linear
// equations. View edges carry affine maps between versioned objects. All
// coverage, aliasing and cardinality decisions are computed algebraically
// through ranks and inclusion-exclusion; block numbers are never enumerated.
package prescreen

import (
	"math/bits"
	"sort"
)

// Width is the number of GF(2) coordinates in every block-number vector.
const Width = 48

const fullMask uint64 = (uint64(1) << Width) - 1
const rhsBit uint64 = uint64(1) << Width

// augMask covers both the Width coefficient bits and the augmented RHS bit.
const augMask uint64 = fullMask | rhsBit

// Vec is a 48-bit column vector over GF(2): bit i is coordinate i.
type Vec uint64

// Eq is one affine equation dot(coeff, x) = rhs. Coefficients occupy the low
// Width bits; the right-hand side is stored in bit Width.
type Eq uint64

// MakeEq builds the equation dot(coeff, x) = rhs. rhs is reduced modulo 2.
func MakeEq(coeff Vec, rhs uint64) Eq {
	return Eq((uint64(coeff) & fullMask) | ((rhs & 1) << Width))
}

func (e Eq) rhs() byte { return byte(uint64(e) >> Width) }

// Coeff returns the coefficient vector of the equation.
func (e Eq) Coeff() Vec { return Vec(uint64(e) & fullMask) }

// RHS returns the right-hand side bit of the equation.
func (e Eq) RHS() byte { return e.rhs() }

func pivot(row Vec) int { return bits.TrailingZeros64(uint64(row)) }

// rrefRows reduces rows into reduced row-echelon (XOR) basis form. Every
// pivot column carries a single 1 and rows are ordered with decreasing
// pivots. Rows whose bits inside pivotMask are zero are dropped by callers.
func rrefRows(rows []uint64, pivotMask uint64) []uint64 {
	basis := make([]uint64, 0, len(rows))
	for _, row := range rows {
		if row&pivotMask == 0 {
			continue
		}
		// Eliminate every existing pivot column from the incoming row.
		for _, b := range basis {
			p := bits.TrailingZeros64(b & pivotMask)
			if row&(uint64(1)<<p) != 0 {
				row ^= b
			}
		}
		if row&pivotMask == 0 {
			continue
		}
		// New pivot column: eliminate it from every existing row so that each
		// pivot column ends up with exactly one 1 (true reduced form).
		p := bits.TrailingZeros64(row & pivotMask)
		for i := range basis {
			if basis[i]&(uint64(1)<<p) != 0 {
				basis[i] ^= row
			}
		}
		basis = append(basis, row)
	}
	sort.Slice(basis, func(i, j int) bool {
		return bits.TrailingZeros64(basis[i]&pivotMask) >
			bits.TrailingZeros64(basis[j]&pivotMask)
	})
	return basis
}

func rrefVec(rows []Vec) []Vec {
	u := make([]uint64, len(rows))
	for i, v := range rows {
		u[i] = uint64(v)
	}
	out := rrefRows(u, fullMask)
	basis := make([]Vec, len(out))
	for i, v := range out {
		basis[i] = Vec(v)
	}
	return basis
}

// rrefRowsAug reduces augmented rows (coefficients in low Width bits, RHS in
// bit Width). Pivot columns are coefficient bits only; the RHS bit is carried
// through elimination and never chosen as a pivot. Zero-coefficient rows are
// retained so the caller can spot the inconsistent 0 = 1 row.
func rrefRowsAug(rows []uint64) []uint64 {
	basis := make([]uint64, 0, len(rows))
	var zeroRows []uint64
	for _, row := range rows {
		work := row
		for _, b := range basis {
			p := bits.TrailingZeros64(b & fullMask)
			if work&(uint64(1)<<p) != 0 {
				work ^= b
			}
		}
		if work&fullMask == 0 {
			if work&rhsBit != 0 {
				zeroRows = append(zeroRows, work)
			}
			continue
		}
		p := bits.TrailingZeros64(work & fullMask)
		for i := range basis {
			if basis[i]&(uint64(1)<<p) != 0 {
				basis[i] ^= work
			}
		}
		basis = append(basis, work)
	}
	sort.Slice(basis, func(i, j int) bool {
		return bits.TrailingZeros64(basis[i]&fullMask) >
			bits.TrailingZeros64(basis[j]&fullMask)
	})
	return append(basis, zeroRows...)
}
