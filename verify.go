package unitinfer

import (
	"fmt"
	"strings"
)

// EnumerateSolutions brute-forces all integer solutions of A x = b with every
// unknown in [lo, hi]. Used to cross-check the symbolic general solution on a
// small exponent domain.
func EnumerateSolutions(a [][]int, b []int, n, lo, hi int) [][]int {
	var out [][]int
	x := make([]int, n)
	var rec func(i int)
	rec = func(i int) {
		if i == n {
			for r := range a {
				sum := 0
				for j := 0; j < n; j++ {
					sum += a[r][j] * x[j]
				}
				if sum != b[r] {
					return
				}
			}
			cp := make([]int, n)
			copy(cp, x)
			out = append(out, cp)
			return
		}
		for v := lo; v <= hi; v++ {
			x[i] = v
			rec(i + 1)
		}
	}
	rec(0)
	return out
}

// Verify cross-checks the symbolic general solution against brute-force
// enumeration of all assignments with exponents in [lo, hi]:
//
//  1. Soundness: every enumerated solution is an instance of the general
//     solution (x - Particular in the Z-span of Basis).
//  2. Completeness of free directions: if s is a solution and s +/- Basis[k]
//     stays inside the box, it must also be a solution -- so a reported free
//     direction is genuine, not an artifact.
//  3. No fabricated fixing: any coordinate the general solution declares
//     fixed (zero in every basis vector) must take Particular's value in
//     every enumerated solution. This is what catches the classic bug of
//     silently defaulting underdetermined units to dimensionless.
//
// Returns nil on success, or an error describing the first mismatch.
func (in *Infer) Verify(sol *Solution, lo, hi int) error {
	a, b, n := buildSystem(in.constraints, in.NumDims, len(in.varNames))
	sols := EnumerateSolutions(a, b, n, lo, hi)
	key := func(x []int) string {
		parts := make([]string, len(x))
		for i, v := range x {
			parts[i] = fmt.Sprint(v)
		}
		return strings.Join(parts, ",")
	}
	inBox := func(x []int) bool {
		for _, v := range x {
			if v < lo || v > hi {
				return false
			}
		}
		return true
	}
	set := map[string]bool{}
	for _, s := range sols {
		if !sol.Contains(s) {
			return fmt.Errorf("enumerated solution %v is not an instance of the general solution", s)
		}
		set[key(s)] = true
	}
	for _, s := range sols {
		for k, bv := range sol.Basis {
			for _, sgn := range []int{1, -1} {
				y := make([]int, n)
				for i := range y {
					y[i] = s[i] + sgn*bv[i]
				}
				if inBox(y) && !set[key(y)] {
					return fmt.Errorf("free direction %d is not genuine: %v lies in the box but is not a solution", k, y)
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		fixed := true
		for _, bv := range sol.Basis {
			if bv[i] != 0 {
				fixed = false
				break
			}
		}
		if fixed {
			for _, s := range sols {
				if s[i] != sol.Particular[i] {
					return fmt.Errorf("coordinate %d declared fixed to %d, but enumerated solution %v disagrees", i, sol.Particular[i], s)
				}
			}
		}
	}
	return nil
}
