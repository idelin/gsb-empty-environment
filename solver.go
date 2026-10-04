package main

import (
	"math/big"
	"sort"
)

// Problem describes an integer signal recovery instance:
// find integer x with lo[i] <= x[i] <= hi[i] and A x = b.
type Problem struct {
	n  int          // number of signal components (<= 12)
	m  int          // number of observations
	lo []*big.Int   // tightened lower bounds (ceil of input)
	hi []*big.Int   // tightened upper bounds (floor of input)
	A  [][]*big.Rat // m x n sampling matrix
	b  []*big.Rat   // m observation values
}

// pivotExpr expresses a pivot variable as c + sum coef[col] * x[col]
// over the free columns.
type pivotExpr struct {
	pivot int
	c     *big.Rat
	coef  map[int]*big.Rat
}

// rref reduces the augmented matrix rows (each of length n+1) to reduced row
// echelon form using exact rational arithmetic. It returns the pivot rows,
// the pivot column for each pivot row, and whether an inconsistent row
// (0 ... 0 | c) with c != 0 was found.
func rref(rows [][]*big.Rat, n int) (pivotRows [][]*big.Rat, pivots []int, inconsistent bool) {
	m := len(rows)
	mat := make([][]*big.Rat, m)
	for i, r := range rows {
		mat[i] = make([]*big.Rat, n+1)
		for j := range r {
			mat[i][j] = new(big.Rat).Set(r[j])
		}
	}
	rank := 0
	for col := 0; col < n && rank < m; col++ {
		piv := -1
		for i := rank; i < m; i++ {
			if mat[i][col].Sign() != 0 {
				piv = i
				break
			}
		}
		if piv == -1 {
			continue
		}
		mat[rank], mat[piv] = mat[piv], mat[rank]
		inv := new(big.Rat).Inv(mat[rank][col])
		for j := col; j <= n; j++ {
			mat[rank][j].Mul(mat[rank][j], inv)
		}
		for i := 0; i < m; i++ {
			if i == rank || mat[i][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(mat[i][col])
			for j := col; j <= n; j++ {
				term := new(big.Rat).Mul(f, mat[rank][j])
				mat[i][j].Sub(mat[i][j], term)
			}
		}
		pivots = append(pivots, col)
		rank++
	}
	for i := rank; i < m; i++ {
		if mat[i][n].Sign() != 0 {
			return nil, nil, true
		}
	}
	return mat[:rank], pivots, false
}

// findSolutions searches for integer solutions of A x = b restricted to the
// given observation rows, within the component bounds. It stops after
// collecting limit solutions. An empty result means infeasible.
//
// The system is first reduced exactly over the rationals; only the free
// variables are enumerated, with interval pruning from the pivot
// expressions. Rank deficiency over the reals is therefore NOT treated as
// proof of multiple integer solutions: the exact integer search decides.
func findSolutions(p *Problem, rowIdx []int, limit int) [][]*big.Int {
	n := p.n
	rows := make([][]*big.Rat, len(rowIdx))
	for i, ri := range rowIdx {
		row := make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			row[j] = new(big.Rat).Set(p.A[ri][j])
		}
		row[n] = new(big.Rat).Set(p.b[ri])
		rows[i] = row
	}
	pivotRows, pivots, inconsistent := rref(rows, n)
	if inconsistent {
		return nil
	}

	isPivot := make([]bool, n)
	for _, c := range pivots {
		isPivot[c] = true
	}
	var free []int
	for j := 0; j < n; j++ {
		if !isPivot[j] {
			free = append(free, j)
		}
	}
	// Enumerate free variables with the smallest domains first.
	order := make([]int, len(free))
	copy(order, free)
	sort.Slice(order, func(a, b int) bool {
		wa := new(big.Int).Sub(p.hi[order[a]], p.lo[order[a]])
		wb := new(big.Int).Sub(p.hi[order[b]], p.lo[order[b]])
		return wa.Cmp(wb) < 0
	})

	exprs := make([]pivotExpr, len(pivotRows))
	for i, row := range pivotRows {
		e := pivotExpr{
			pivot: pivots[i],
			c:     new(big.Rat).Set(row[n]),
			coef:  make(map[int]*big.Rat),
		}
		for _, col := range free {
			if row[col].Sign() != 0 {
				e.coef[col] = new(big.Rat).Neg(row[col])
			}
		}
		exprs[i] = e
	}

	var sols [][]*big.Int
	val := make([]*big.Int, n) // assigned values for free columns

	// prune reports whether the partial assignment (first `level` variables
	// of `order` fixed) cannot be extended: some pivot expression's
	// reachable interval misses its own bounds.
	prune := func(level int) bool {
		for _, e := range exprs {
			lo := new(big.Rat).Set(e.c)
			hi := new(big.Rat).Set(e.c)
			for k := 0; k < len(order); k++ {
				col := order[k]
				cf, ok := e.coef[col]
				if !ok {
					continue
				}
				if k < level {
					t := new(big.Rat).Mul(cf, new(big.Rat).SetInt(val[col]))
					lo.Add(lo, t)
					hi.Add(hi, t)
				} else {
					loB := new(big.Rat).SetInt(p.lo[col])
					hiB := new(big.Rat).SetInt(p.hi[col])
					if cf.Sign() >= 0 {
						lo.Add(lo, new(big.Rat).Mul(cf, loB))
						hi.Add(hi, new(big.Rat).Mul(cf, hiB))
					} else {
						lo.Add(lo, new(big.Rat).Mul(cf, hiB))
						hi.Add(hi, new(big.Rat).Mul(cf, loB))
					}
				}
			}
			if hi.Cmp(new(big.Rat).SetInt(p.lo[e.pivot])) < 0 ||
				lo.Cmp(new(big.Rat).SetInt(p.hi[e.pivot])) > 0 {
				return true
			}
		}
		return false
	}

	var dfs func(level int) bool // returns true when the search should stop
	dfs = func(level int) bool {
		if prune(level) {
			return false
		}
		if level == len(order) {
			sol := make([]*big.Int, n)
			for _, col := range free {
				sol[col] = new(big.Int).Set(val[col])
			}
			for _, e := range exprs {
				v := new(big.Rat).Set(e.c)
				for col, cf := range e.coef {
					v.Add(v, new(big.Rat).Mul(cf, new(big.Rat).SetInt(val[col])))
				}
				if v.Denom().Cmp(big.NewInt(1)) != 0 {
					return false // pivot not integral: reject
				}
				iv := v.Num()
				if iv.Cmp(p.lo[e.pivot]) < 0 || iv.Cmp(p.hi[e.pivot]) > 0 {
					return false
				}
				sol[e.pivot] = new(big.Int).Set(iv)
			}
			sols = append(sols, sol)
			return len(sols) >= limit
		}
		col := order[level]
		v := new(big.Int).Set(p.lo[col])
		one := big.NewInt(1)
		for ; v.Cmp(p.hi[col]) <= 0; v.Add(v, one) {
			val[col] = new(big.Int).Set(v)
			if dfs(level + 1) {
				return true
			}
		}
		return false
	}
	dfs(0)
	return sols
}

// conflictCore finds a minimal (under deletion) subset of observations that
// is already infeasible together with the component bounds.
func conflictCore(p *Problem) []int {
	core := make([]int, p.m)
	for i := range core {
		core[i] = i
	}
	i := 0
	for i < len(core) {
		cand := make([]int, 0, len(core)-1)
		cand = append(cand, core[:i]...)
		cand = append(cand, core[i+1:]...)
		if len(findSolutions(p, cand, 1)) == 0 {
			core = cand // still infeasible without observation core[i]
		} else {
			i++
		}
	}
	return core
}
