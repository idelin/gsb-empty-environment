package main

import "math/big"

// solveLinear solves A x = b exactly over the rationals using Gauss-Jordan
// elimination. If A is singular it reports singular=true instead of
// fabricating a finite solution.
func solveLinear(A [][]*big.Rat, b []*big.Rat) (x []*big.Rat, singular bool) {
	n := len(A)
	if n == 0 {
		return nil, false
	}
	M := make([][]*big.Rat, n)
	for i := range M {
		M[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			M[i][j] = new(big.Rat).Set(A[i][j])
		}
		M[i][n] = new(big.Rat).Set(b[i])
	}
	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if M[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv == -1 {
			return nil, true
		}
		M[col], M[piv] = M[piv], M[col]
		inv := new(big.Rat).Inv(M[col][col])
		for j := col; j <= n; j++ {
			M[col][j].Mul(M[col][j], inv)
		}
		for r := 0; r < n; r++ {
			if r == col || M[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(M[r][col])
			for j := col; j <= n; j++ {
				M[r][j].Sub(M[r][j], new(big.Rat).Mul(f, M[col][j]))
			}
		}
	}
	x = make([]*big.Rat, n)
	for i := range x {
		x[i] = M[i][n]
	}
	return x, false
}

// stronglyConnectedComponents runs Tarjan's algorithm on a directed graph.
func stronglyConnectedComponents(g [][]int) [][]int {
	n := len(g)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	counter := 0
	var comps [][]int
	var visit func(v int)
	visit = func(v int) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range g[v] {
			if index[w] == -1 {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			comps = append(comps, comp)
		}
	}
	for v := 0; v < n; v++ {
		if index[v] == -1 {
			visit(v)
		}
	}
	return comps
}

// shortestPathToSet returns a shortest positive-probability path from src to
// any state in target, or nil if the target set is unreachable.
func shortestPathToSet(g [][]int, src int, target map[int]bool) []int {
	if target[src] {
		return []int{src}
	}
	prev := make([]int, len(g))
	for i := range prev {
		prev[i] = -2
	}
	prev[src] = -1
	queue := []int{src}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range g[v] {
			if prev[w] != -2 {
				continue
			}
			prev[w] = v
			if target[w] {
				var rev []int
				for x := w; x != -1; x = prev[x] {
					rev = append(rev, x)
				}
				path := make([]int, len(rev))
				for i, x := range rev {
					path[len(rev)-1-i] = x
				}
				return path
			}
			queue = append(queue, w)
		}
	}
	return nil
}
