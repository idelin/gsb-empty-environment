package sched

import "errors"

// MaxBruteForceOrders caps SolveBruteForce, which enumerates n! permutations.
const MaxBruteForceOrders = 10

// SolveBruteForce enumerates every permutation of the orders and returns the
// best one under the same lexicographic objectives as Solve:
// (max lateness, total setup, order-index sequence). It exists to verify
// Solve on small instances and is exponential in the number of orders.
func SolveBruteForce(p Problem) (Solution, error) {
	if err := validate(p); err != nil {
		return Solution{}, err
	}
	n := len(p.Orders)
	if n > MaxBruteForceOrders {
		return Solution{}, errors.New("sched: too many orders for brute force")
	}
	if n == 0 {
		return Solution{}, nil
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var best Solution
	first := true
	var bestSeq []int
	visit := func(seq []int) {
		sol := buildSolution(p, seq)
		if first ||
			sol.MaxLateness < best.MaxLateness ||
			(sol.MaxLateness == best.MaxLateness && sol.TotalSetup < best.TotalSetup) ||
			(sol.MaxLateness == best.MaxLateness && sol.TotalSetup == best.TotalSetup && lexLess(seq, bestSeq)) {
			best = sol
			bestSeq = append([]int(nil), seq...)
			first = false
		}
	}
	permute(perm, 0, visit)
	return best, nil
}

// permute generates all permutations of a in place (Heap's algorithm style
// recursion over prefixes) and calls visit on each.
func permute(a []int, k int, visit func([]int)) {
	if k == len(a) {
		visit(a)
		return
	}
	for i := k; i < len(a); i++ {
		a[k], a[i] = a[i], a[k]
		permute(a, k+1, visit)
		a[k], a[i] = a[i], a[k]
	}
}
