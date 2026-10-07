package psdcomplete

import (
	"fmt"
	"math/big"
	"strings"
)

// LDLCert is the result of an independent exact-elimination PSD check:
// symmetric Gaussian elimination (an LDL^T factorization attempt carried out
// entirely in exact rational arithmetic). A symmetric matrix is positive
// semidefinite iff elimination never meets a negative pivot and every zero
// pivot has an entirely zero remaining row/column.
//
// This verifier shares no code with the principal-minor computation, so the
// two cross-check each other.
type LDLCert struct {
	// Pivots are the exact diagonal entries of D in the (partial) LDL^T
	// factorization, i.e. the Schur-complement diagonals, in elimination
	// order. When PSD is false the final pivot is the offending value.
	Pivots []*big.Rat
	// PSD is true iff the elimination certifies positive semidefiniteness.
	PSD bool
	// Detail explains a failure ("negative pivot" or "zero pivot with
	// nonzero row"); empty when PSD is true.
	Detail string
}

// VerifyLDL runs exact symmetric Gaussian elimination on m and returns the
// resulting certificate.
func VerifyLDL(m Matrix) LDLCert {
	n := len(m)
	a := m.Clone()
	cert := LDLCert{PSD: true}
	zero := new(big.Rat)
	for k := 0; k < n; k++ {
		pivot := a[k][k]
		switch pivot.Sign() {
		case -1:
			cert.Pivots = append(cert.Pivots, cloneRat(pivot))
			cert.PSD = false
			cert.Detail = fmt.Sprintf("negative pivot %s at stage %d", ratString(pivot), k+1)
			return cert
		case 0:
			for j := k + 1; j < n; j++ {
				if a[k][j].Sign() != 0 {
					cert.Pivots = append(cert.Pivots, cloneRat(zero))
					cert.PSD = false
					cert.Detail = fmt.Sprintf(
						"zero pivot at stage %d but row entry (%d,%d) = %s",
						k+1, k+1, j+1, ratString(a[k][j]))
					return cert
				}
			}
			cert.Pivots = append(cert.Pivots, cloneRat(zero))
		default:
			cert.Pivots = append(cert.Pivots, cloneRat(pivot))
			for i := k + 1; i < n; i++ {
				if a[i][k].Sign() == 0 {
					continue
				}
				for j := k + 1; j < n; j++ {
					// a[i][j] -= a[i][k] * a[k][j] / pivot
					t := new(big.Rat).Mul(a[i][k], a[k][j])
					t.Quo(t, pivot)
					a[i][j].Sub(a[i][j], t)
				}
			}
		}
	}
	return cert
}

func (c LDLCert) String() string {
	parts := make([]string, len(c.Pivots))
	for i, p := range c.Pivots {
		parts[i] = ratString(p)
	}
	status := "all >= 0, PSD"
	if !c.PSD {
		status = "NOT PSD: " + c.Detail
	}
	return fmt.Sprintf("exact LDL^T elimination pivots: [%s] (%s)", strings.Join(parts, ", "), status)
}
