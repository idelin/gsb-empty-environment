package unitinfer

import (
	"math/rand"
	"testing"
)

// Randomized cross-check: for random integer systems A x = b, the symbolic
// solution (particular + kernel basis) must match brute-force enumeration on
// a small box, in both directions.
func TestSolveSystemMatchesEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const lo, hi = -3, 3
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(4)
		m := rng.Intn(5)
		a := make([][]int, m)
		b := make([]int, m)
		for i := range a {
			a[i] = make([]int, n)
			for j := range a[i] {
				a[i][j] = rng.Intn(5) - 2
			}
			b[i] = rng.Intn(7) - 3
		}
		part, basis, ok := solveSystem(a, b, n)
		sols := EnumerateSolutions(a, b, n, lo, hi)
		if !ok {
			if len(sols) != 0 {
				t.Fatalf("trial %d: solver reports unsat, but %v solves %v x = %v", trial, sols[0], a, b)
			}
			continue
		}
		// Particular solution really solves the system.
		for i := range a {
			sum := 0
			for j := 0; j < n; j++ {
				sum += a[i][j] * part[j]
			}
			if sum != b[i] {
				t.Fatalf("trial %d: particular %v fails row %d of %v = %v", trial, part, i, a, b)
			}
		}
		// Basis vectors lie in the kernel.
		for k, bv := range basis {
			for i := range a {
				sum := 0
				for j := 0; j < n; j++ {
					sum += a[i][j] * bv[j]
				}
				if sum != 0 {
					t.Fatalf("trial %d: basis vector %d not in kernel of %v", trial, k, a)
				}
			}
		}
		// Every enumerated solution is an instance of the general solution.
		sol := &Solution{NumDims: 1, Particular: part, Basis: basis}
		for _, s := range sols {
			if !sol.Contains(s) {
				t.Fatalf("trial %d: enumerated %v not covered by %v + %v", trial, s, part, basis)
			}
		}
		// Every in-box lattice point of the general solution is a solution.
		set := map[[8]int]bool{}
		key := func(x []int) (k [8]int) {
			copy(k[:], x)
			return
		}
		for _, s := range sols {
			set[key(s)] = true
		}
		for _, s := range sols {
			for _, bv := range basis {
				for _, sgn := range []int{1, -1} {
					y := make([]int, n)
					inBox := true
					for i := range y {
						y[i] = s[i] + sgn*bv[i]
						if y[i] < lo || y[i] > hi {
							inBox = false
						}
					}
					if inBox && !set[key(y)] {
						t.Fatalf("trial %d: lattice point %v in box but not a solution of %v = %v", trial, y, a, b)
					}
				}
			}
		}
	}
}
