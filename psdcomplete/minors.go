package psdcomplete

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// MinorCert records the exact determinant of one principal submatrix.
// Subset holds 0-based row/column indices in increasing order.
type MinorCert struct {
	Subset []int
	Det    *big.Rat
}

// AllPrincipalMinors computes the determinants of every principal submatrix
// of m (all 2^n - 1 non-empty index subsets), sorted by subset size and then
// lexicographically by subset. A symmetric matrix is positive semidefinite
// if and only if every one of these values is >= 0; checking only the
// diagonal entries and the full determinant is NOT sufficient.
func AllPrincipalMinors(m Matrix) []MinorCert {
	n := len(m)
	var out []MinorCert
	for mask := 1; mask < (1 << uint(n)); mask++ {
		var idx []int
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				idx = append(idx, i)
			}
		}
		out = append(out, MinorCert{Subset: idx, Det: Det(m.Submatrix(idx))})
	}
	sort.Slice(out, func(a, b int) bool {
		if len(out[a].Subset) != len(out[b].Subset) {
			return len(out[a].Subset) < len(out[b].Subset)
		}
		for k := range out[a].Subset {
			if out[a].Subset[k] != out[b].Subset[k] {
				return out[a].Subset[k] < out[b].Subset[k]
			}
		}
		return false
	})
	return out
}

// IsPSDMinors reports whether m is positive semidefinite according to the
// all-principal-minors criterion. It also returns the first negative minor
// found, or nil if none exists.
func IsPSDMinors(m Matrix) (bool, *MinorCert) {
	minors := AllPrincipalMinors(m)
	for i := range minors {
		if minors[i].Det.Sign() < 0 {
			bad := minors[i]
			return false, &bad
		}
	}
	return true, nil
}

func formatSubset(subset []int) string {
	parts := make([]string, len(subset))
	for i, v := range subset {
		parts[i] = fmt.Sprintf("%d", v+1) // 1-based for display
	}
	return "{" + strings.Join(parts, ",") + "}"
}
