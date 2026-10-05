package solver

import "math/big"

// This file implements a two-phase simplex method over exact rational
// arithmetic (math/big.Rat) with Bland's pivoting rule, which guarantees
// termination. Exactness matters: gain-cycle detection must not produce
// false positives or negatives from floating-point rounding, and the
// branch-and-bound bounds must be sound.

type sense int

const (
	senseLe sense = iota
	senseGe
	senseEq
)

type lpConstraint struct {
	coeffs []*big.Rat
	sense  sense
	rhs    *big.Rat
}

type lpStatus int

const (
	lpOptimal lpStatus = iota
	lpInfeasible
	lpUnbounded
)

func ratInt(v int64) *big.Rat      { return new(big.Rat).SetInt64(v) }
func ratClone(v *big.Rat) *big.Rat { return new(big.Rat).Set(v) }

func flipSense(s sense) sense {
	switch s {
	case senseLe:
		return senseGe
	case senseGe:
		return senseLe
	default:
		return senseEq
	}
}

// simplex solves minimize cost^T x subject to cons and x >= 0.
// On lpOptimal it returns the objective value and a primal solution for the
// len(cost) structural variables.
func simplex(cost []*big.Rat, cons []lpConstraint) (lpStatus, *big.Rat, []*big.Rat) {
	n := len(cost)
	m := len(cons)

	// Tableau with m rows; column count grows as slacks/artificials are added.
	tab := make([][]*big.Rat, m)
	rhs := make([]*big.Rat, m)
	senses := make([]sense, m)
	for i, c := range cons {
		row := make([]*big.Rat, n)
		for j := range row {
			row[j] = ratClone(c.coeffs[j])
		}
		r := ratClone(c.rhs)
		s := c.sense
		if r.Sign() < 0 {
			for j := range row {
				row[j].Neg(row[j])
			}
			r.Neg(r)
			s = flipSense(s)
		}
		tab[i] = row
		rhs[i] = r
		senses[i] = s
	}

	width := n
	isArtificial := make([]bool, n)
	phase2Cost := make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		phase2Cost[j] = ratClone(cost[j])
	}
	addColumn := func(artificial bool) int {
		for i := 0; i < m; i++ {
			tab[i] = append(tab[i], ratInt(0))
		}
		isArtificial = append(isArtificial, artificial)
		phase2Cost = append(phase2Cost, ratInt(0))
		width++
		return width - 1
	}

	basis := make([]int, m)
	for i := 0; i < m; i++ {
		switch senses[i] {
		case senseLe:
			col := addColumn(false)
			tab[i][col] = ratInt(1)
			basis[i] = col
		case senseGe:
			slack := addColumn(false)
			tab[i][slack] = ratInt(-1)
			art := addColumn(true)
			tab[i][art] = ratInt(1)
			basis[i] = art
		case senseEq:
			art := addColumn(true)
			tab[i][art] = ratInt(1)
			basis[i] = art
		}
	}

	pivot := func(r, j int) {
		inv := new(big.Rat).Inv(tab[r][j])
		for k := range tab[r] {
			tab[r][k].Mul(tab[r][k], inv)
		}
		rhs[r].Mul(rhs[r], inv)
		for i := 0; i < m; i++ {
			if i == r {
				continue
			}
			f := tab[i][j]
			if f.Sign() == 0 {
				continue
			}
			fc := ratClone(f)
			for k := range tab[i] {
				tab[i][k].Sub(tab[i][k], new(big.Rat).Mul(fc, tab[r][k]))
			}
			rhs[i].Sub(rhs[i], new(big.Rat).Mul(fc, rhs[r]))
		}
		basis[r] = j
	}

	reducedCost := func(j int, costVec []*big.Rat) *big.Rat {
		rc := ratClone(costVec[j])
		for i := 0; i < m; i++ {
			cb := costVec[basis[i]]
			if cb.Sign() != 0 {
				rc.Sub(rc, new(big.Rat).Mul(cb, tab[i][j]))
			}
		}
		return rc
	}

	objective := func(costVec []*big.Rat) *big.Rat {
		obj := ratInt(0)
		for i := 0; i < m; i++ {
			if cb := costVec[basis[i]]; cb.Sign() != 0 {
				obj.Add(obj, new(big.Rat).Mul(cb, rhs[i]))
			}
		}
		return obj
	}

	// run executes the simplex loop with Bland's rule for the given
	// objective; columns with allowed[j] == false never enter the basis.
	run := func(costVec []*big.Rat, allowed []bool) lpStatus {
		isBasic := make([]bool, width)
		for i := 0; i < m; i++ {
			isBasic[basis[i]] = true
		}
		for {
			entering := -1
			for j := 0; j < width; j++ {
				if isBasic[j] || !allowed[j] {
					continue
				}
				if reducedCost(j, costVec).Sign() < 0 {
					entering = j
					break
				}
			}
			if entering < 0 {
				return lpOptimal
			}
			leave := -1
			var bestRatio *big.Rat
			for i := 0; i < m; i++ {
				a := tab[i][entering]
				if a.Sign() <= 0 {
					continue
				}
				ratio := new(big.Rat).Quo(rhs[i], a)
				if leave == -1 || ratio.Cmp(bestRatio) < 0 ||
					(ratio.Cmp(bestRatio) == 0 && basis[i] < basis[leave]) {
					leave = i
					bestRatio = ratio
				}
			}
			if leave == -1 {
				return lpUnbounded
			}
			oldBasis := basis[leave]
			pivot(leave, entering)
			isBasic[oldBasis] = false
			isBasic[entering] = true
		}
	}

	// Phase 1: minimize the sum of artificials to find a feasible basis.
	hasArtificial := false
	for _, a := range isArtificial {
		if a {
			hasArtificial = true
			break
		}
	}
	if hasArtificial {
		phase1Cost := make([]*big.Rat, width)
		allowed := make([]bool, width)
		for j := 0; j < width; j++ {
			allowed[j] = !isArtificial[j]
			if isArtificial[j] {
				phase1Cost[j] = ratInt(1)
			} else {
				phase1Cost[j] = ratInt(0)
			}
		}
		if st := run(phase1Cost, allowed); st != lpOptimal {
			return lpInfeasible, nil, nil
		}
		if objective(phase1Cost).Sign() > 0 {
			return lpInfeasible, nil, nil
		}
		// Drive any remaining basic artificials (necessarily at value 0)
		// out of the basis; rows where this is impossible are redundant.
		for i := 0; i < m; i++ {
			if !isArtificial[basis[i]] {
				continue
			}
			for j := 0; j < width; j++ {
				if !isArtificial[j] && tab[i][j].Sign() != 0 {
					pivot(i, j)
					break
				}
			}
		}
	}

	// Phase 2: optimize the real objective. Artificial columns never enter.
	allowed := make([]bool, width)
	for j := range allowed {
		allowed[j] = !isArtificial[j]
	}
	if st := run(phase2Cost, allowed); st == lpUnbounded {
		return lpUnbounded, nil, nil
	}

	x := make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		x[j] = ratInt(0)
	}
	for i := 0; i < m; i++ {
		if basis[i] < n {
			x[basis[i]] = ratClone(rhs[i])
		}
	}
	return lpOptimal, objective(phase2Cost), x
}
