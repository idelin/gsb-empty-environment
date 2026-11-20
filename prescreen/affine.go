package prescreen

import "math/bits"

// Affine is an affine subspace offset + span(Dir). The zero value is the
// singleton {0}; use the constructors rather than building literals.
type Affine struct {
	// Empty marks the empty set, which cannot be represented by an affine
	// coset (every consistent linear system has 2^(n-rank) solutions).
	Empty bool
	// Dir is the canonical RREF basis of the direction subspace.
	Dir []Vec
	// Offset is the canonical representative: zero on every Dir pivot.
	Offset Vec
}

// NewAffine builds the canonical affine space offset + span(dir).
func NewAffine(dir []Vec, offset Vec) Affine {
	rows := make([]Vec, len(dir))
	copy(rows, dir)
	basis := rrefVec(rows)
	offset &= Vec(fullMask)
	for _, b := range basis {
		p := bits.TrailingZeros64(uint64(b))
		if offset&(Vec(1)<<p) != 0 {
			offset ^= b
		}
	}
	if len(basis) == 0 {
		basis = nil
	}
	return Affine{Dir: basis, Offset: offset}
}

// FullSpace returns the whole 48-bit block space.
func FullSpace() Affine {
	dir := make([]Vec, Width)
	for i := 0; i < Width; i++ {
		dir[i] = Vec(1) << i
	}
	return Affine{Dir: rrefVec(dir), Offset: 0}
}

// EmptyAffine returns the empty set.
func EmptyAffine() Affine { return Affine{Empty: true} }

// Point returns the singleton affine space {v}.
func Point(v Vec) Affine { return NewAffine(nil, v) }

// SolveAffine solves a system of affine equations and returns the affine
// solution set, or EmptyAffine when the system is inconsistent.
func SolveAffine(eqs []Eq) Affine {
	rows := make([]uint64, 0, len(eqs))
	for _, e := range eqs {
		a := uint64(e) & fullMask
		if a == 0 {
			if e.rhs() == 1 {
				return EmptyAffine()
			}
			continue
		}
		rows = append(rows, a|(uint64(e.rhs())<<Width))
	}
	// Pivot over coefficient bits only, but carry the RHS bit through every
	// XOR so a contradictory 0 = 1 row survives for detection below.
	basis := rrefRowsAug(rows)
	for _, row := range basis {
		if row&fullMask == 0 && row&rhsBit != 0 {
			return EmptyAffine()
		}
	}

	pivotCols := make(map[int]bool, len(basis))
	var particular Vec
	for _, row := range basis {
		p := bits.TrailingZeros64(row & fullMask)
		pivotCols[p] = true
		if row&rhsBit != 0 {
			particular |= Vec(1) << p
		}
	}

	// Each free column yields one nullspace vector: set that free coordinate
	// to 1 and let each RREF row fix its own pivot coordinate.
	nullDir := make([]Vec, 0, Width-len(basis))
	// Free columns range over the Width variables only; bit Width holds the
	// right-hand side inside basis rows and must never be treated as a column.
	for f := 0; f < Width; f++ {
		if pivotCols[f] {
			continue
		}
		v := Vec(1) << f
		for _, row := range basis {
			if row&(uint64(1)<<f) != 0 {
				p := bits.TrailingZeros64(row & fullMask)
				// Only the pivot coordinate belongs to the null vector; the
				// row's RHS bit contributes to particular solutions instead.
				v |= Vec(1) << p
			}
		}
		nullDir = append(nullDir, v)
	}
	return NewAffine(nullDir, particular)
}

// Equations returns a canonical system of affine equations defining the set.
// The empty set is represented by the single inconsistent equation 0 = 1.
func (af Affine) Equations() []Eq {
	if af.Empty {
		return []Eq{MakeEq(0, 1)}
	}
	isPivot := make(map[int]bool, len(af.Dir))
	for _, b := range af.Dir {
		isPivot[pivot(b)] = true
	}
	// pivotRow maps a pivot column to the bit set in that row's offset
	// contribution. The canonical offset has zero on every pivot, so the
	// annihilator right-hand side reduces to the offset's free-coordinate bit.
	eqs := make([]Eq, 0, Width-len(af.Dir))
	for f := 0; f < Width; f++ {
		if isPivot[f] {
			continue
		}
		// Annihilator row for free column f: c_f = 1, and c_p = (basis row
		// pivot p has a 1 in column f). Its right-hand side is the same linear
		// functional applied to the canonical offset.
		coeff := Vec(1) << f
		var rhs uint64
		if af.Offset&(Vec(1)<<f) != 0 {
			rhs = 1
		}
		for _, b := range af.Dir {
			if b&(Vec(1)<<f) != 0 {
				p := pivot(b)
				coeff |= Vec(1) << p
				if af.Offset&(Vec(1)<<p) != 0 {
					rhs ^= 1
				}
			}
		}
		eqs = append(eqs, MakeEq(coeff, rhs))
	}
	return eqs
}

// Intersect returns the affine intersection of two sets.
func (af Affine) Intersect(other Affine) Affine {
	if af.Empty || other.Empty {
		return EmptyAffine()
	}
	eqs := make([]Eq, 0, Width*2)
	eqs = append(eqs, af.Equations()...)
	eqs = append(eqs, other.Equations()...)
	return SolveAffine(eqs)
}

// Contains reports whether v belongs to the affine set.
func (af Affine) Contains(v Vec) bool {
	if af.Empty {
		return false
	}
	d := (v ^ af.Offset) & Vec(fullMask)
	for _, b := range af.Dir {
		p := pivot(b)
		if d&(Vec(1)<<p) != 0 {
			d ^= b
		}
	}
	return d == 0
}

// Dim returns the affine dimension, or -1 for the empty set.
func (af Affine) Dim() int {
	if af.Empty {
		return -1
	}
	return len(af.Dir)
}

// Size returns the exact number of distinct blocks in the set.
func (af Affine) Size() uint64 {
	if af.Empty {
		return 0
	}
	return uint64(1) << len(af.Dir)
}

// Equal compares two affine sets semantically (canonical form), so the same
// set written through different equation systems compares equal.
func (af Affine) Equal(other Affine) bool {
	if af.Empty || other.Empty {
		return af.Empty == other.Empty
	}
	if len(af.Dir) != len(other.Dir) || af.Offset != other.Offset {
		return false
	}
	for i := range af.Dir {
		if af.Dir[i] != other.Dir[i] {
			return false
		}
	}
	return true
}
