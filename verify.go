package linsolve

import "math/big"

// matVec computes A·x exactly into a fresh dense vector.
func matVec(A *SparseMatrix, x []*big.Rat) []*big.Rat {
	out := DenseVec(A.Rows())
	tmp := new(big.Rat)
	for i := 0; i < A.Rows(); i++ {
		acc := out[i]
		for c, a := range A.rows[i] {
			if c < len(x) && x[c] != nil && x[c].Sign() != 0 {
				acc.Add(acc, tmp.Mul(a, x[c]))
			}
		}
	}
	return out
}

func vecEq(u, v []*big.Rat) bool {
	if len(u) != len(v) {
		return false
	}
	for i := range u {
		ui, vi := u[i], v[i]
		if ui == nil {
			ui = new(big.Rat)
		}
		if vi == nil {
			vi = new(big.Rat)
		}
		if ui.Cmp(vi) != 0 {
			return false
		}
	}
	return true
}

func isZeroVec(v []*big.Rat) bool {
	for _, x := range v {
		if x != nil && x.Sign() != 0 {
			return false
		}
	}
	return true
}

// VerifyPoint independently checks that A·x = b by substitution into the
// original system.
func VerifyPoint(A *SparseMatrix, b, x []*big.Rat) bool {
	if len(x) != A.Cols() || len(b) != A.Rows() {
		return false
	}
	return vecEq(matVec(A, x), b)
}

// VerifyDirection independently checks that A·d = 0, i.e. d lies in the
// null space of A.
func VerifyDirection(A *SparseMatrix, d []*big.Rat) bool {
	if len(d) != A.Cols() {
		return false
	}
	return isZeroVec(matVec(A, d))
}

// VerifyCertificate independently checks an inconsistency certificate:
// c·A must be the zero row vector while c·b is a nonzero constant, so the
// corresponding linear combination of equations is 0 = (nonzero).
func VerifyCertificate(A *SparseMatrix, b, c []*big.Rat) bool {
	if len(c) != A.Rows() || len(b) != A.Rows() {
		return false
	}
	combo := DenseVec(A.Cols())
	tmp := new(big.Rat)
	constant := new(big.Rat)
	for i := 0; i < A.Rows(); i++ {
		if c[i] == nil || c[i].Sign() == 0 {
			continue
		}
		for col, a := range A.rows[i] {
			combo[col].Add(combo[col], tmp.Mul(c[i], a))
		}
		if b[i] != nil {
			constant.Add(constant, tmp.Mul(c[i], b[i]))
		}
	}
	return isZeroVec(combo) && constant.Sign() != 0
}

// Check fully and independently validates a Solution against (A, b),
// recomputing everything from the original inputs:
//
//	Inconsistent: the certificate is verified (c·A = 0, c·b != 0).
//	Unique:       the point is substituted into A·x = b.
//	Parametric:   the particular point satisfies A·x = b, every
//	              direction satisfies A·d = 0, and the directions are
//	              independent with one parameter per free variable in
//	              increasing column-index order (direction i is 1 at
//	              FreeVars[i] and 0 at every other free variable).
func Check(A *SparseMatrix, b []*big.Rat, s *Solution) bool {
	if A == nil || s == nil || len(b) != A.Rows() {
		return false
	}
	switch s.Status {
	case Inconsistent:
		return VerifyCertificate(A, b, s.Certificate)
	case Unique:
		if len(s.Directions) != 0 || len(s.FreeVars) != 0 {
			return false
		}
		return VerifyPoint(A, b, s.Point)
	case Parametric:
		if len(s.FreeVars) == 0 || len(s.FreeVars) != len(s.Directions) {
			return false
		}
		if !VerifyPoint(A, b, s.Point) {
			return false
		}
		one := big.NewRat(1, 1)
		for i, d := range s.Directions {
			if !VerifyDirection(A, d) {
				return false
			}
			for j, f := range s.FreeVars {
				if j > 0 && f <= s.FreeVars[j-1] {
					return false // FreeVars must be strictly increasing
				}
				if f < 0 || f >= A.Cols() {
					return false
				}
				if i == j {
					if d[f] == nil || d[f].Cmp(one) != 0 {
						return false
					}
				} else if d[f] != nil && d[f].Sign() != 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}
