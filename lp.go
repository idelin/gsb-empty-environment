package main

import "math/big"

// LP solver: minimize objective . x subject to linear constraints, x >= 0.
// All arithmetic is exact rational (math/big.Rat), so results are exact.

type sense int

const (
	senseLeq sense = -1
	senseEq  sense = 0
	senseGeq sense = 1
)

type lpConstraint struct {
	coeffs map[int]*big.Rat // sparse row over variable indices
	sense  sense
	rhs    *big.Rat
}

type lpStatus int

const (
	lpOptimal lpStatus = iota
	lpInfeasible
	lpUnbounded
)

func zeroRat() *big.Rat { return new(big.Rat) }

// pivot performs a Gauss-Jordan pivot on tableau row p, column q.
// tab[0] is the reduced-cost row; constraint rows start at index 1.
// basis[i-1] is the basic variable of constraint row i.
func pivot(tab [][]*big.Rat, basis []int, p, q int) {
	row := tab[p]
	inv := new(big.Rat).Inv(new(big.Rat).Set(row[q]))
	for j := range row {
		row[j].Mul(row[j], inv)
	}
	for i := range tab {
		if i == p {
			continue
		}
		f := new(big.Rat).Set(tab[i][q])
		if f.Sign() == 0 {
			continue
		}
		for j := range tab[i] {
			t := new(big.Rat).Mul(f, row[j])
			tab[i][j].Sub(tab[i][j], t)
		}
	}
	basis[p-1] = q
}

// simplex runs the primal simplex method with Bland's rule (guaranteed to
// terminate). tab[0] must hold reduced costs with tab[0][last] = -z.
func simplex(tab [][]*big.Rat, basis []int, eligible []bool) lpStatus {
	n := len(tab[0]) - 1
	for {
		// Bland: smallest-index eligible entering variable with negative reduced cost.
		q := -1
		for j := 0; j < n; j++ {
			if eligible[j] && tab[0][j].Sign() < 0 {
				q = j
				break
			}
		}
		if q < 0 {
			return lpOptimal
		}
		// Ratio test; ties broken by smallest basic-variable index (Bland).
		p := -1
		var bestRatio *big.Rat
		for i := 1; i < len(tab); i++ {
			if tab[i][q].Sign() > 0 {
				ratio := new(big.Rat).Quo(tab[i][n], tab[i][q])
				if p < 0 || ratio.Cmp(bestRatio) < 0 ||
					(ratio.Cmp(bestRatio) == 0 && basis[i-1] < basis[p-1]) {
					p = i
					bestRatio = ratio
				}
			}
		}
		if p < 0 {
			return lpUnbounded
		}
		pivot(tab, basis, p, q)
	}
}

// solveLP minimizes objective . x, x >= 0, subject to cons.
// Returns status, optimal objective value, and an optimal primal solution.
func solveLP(numVars int, cons []lpConstraint, objective []*big.Rat) (lpStatus, *big.Rat, []*big.Rat) {
	type nrow struct {
		coeffs map[int]*big.Rat
		sense  sense
		rhs    *big.Rat
	}
	rows := make([]nrow, 0, len(cons))
	for _, c := range cons {
		coeffs := make(map[int]*big.Rat, len(c.coeffs))
		for j, v := range c.coeffs {
			if v.Sign() != 0 {
				coeffs[j] = new(big.Rat).Set(v)
			}
		}
		rhs := new(big.Rat).Set(c.rhs)
		s := c.sense
		if rhs.Sign() < 0 { // normalize to non-negative RHS
			for j := range coeffs {
				coeffs[j].Neg(coeffs[j])
			}
			rhs.Neg(rhs)
			s = -s
		}
		rows = append(rows, nrow{coeffs, s, rhs})
	}

	numSlack, numArt := 0, 0
	for _, r := range rows {
		if r.sense == senseEq {
			numArt++
		} else {
			numSlack++
			if r.sense == senseGeq {
				numArt++
			}
		}
	}
	numStruct := numVars + numSlack // structural + slack columns; artificials follow
	n := numStruct + numArt
	width := n + 1 // last column is RHS

	slackCol := make([]int, len(rows))
	artCol := make([]int, len(rows))
	nextSlack, nextArt := numVars, numStruct
	for i, r := range rows {
		slackCol[i], artCol[i] = -1, -1
		if r.sense == senseEq {
			artCol[i] = nextArt
			nextArt++
		} else {
			slackCol[i] = nextSlack
			nextSlack++
			if r.sense == senseGeq {
				artCol[i] = nextArt
				nextArt++
			}
		}
	}

	tab := make([][]*big.Rat, len(rows)+1)
	for i := range tab {
		tab[i] = make([]*big.Rat, width)
		for j := range tab[i] {
			tab[i][j] = zeroRat()
		}
	}
	basis := make([]int, len(rows))
	for i, r := range rows {
		for j, v := range r.coeffs {
			tab[i+1][j].Set(v)
		}
		tab[i+1][n].Set(r.rhs)
		switch r.sense {
		case senseLeq:
			tab[i+1][slackCol[i]].SetInt64(1)
			basis[i] = slackCol[i]
		case senseGeq:
			tab[i+1][slackCol[i]].SetInt64(-1)
			tab[i+1][artCol[i]].SetInt64(1)
			basis[i] = artCol[i]
		case senseEq:
			tab[i+1][artCol[i]].SetInt64(1)
			basis[i] = artCol[i]
		}
	}

	isArtificial := func(col int) bool { return col >= numStruct }

	eligAll := make([]bool, n)
	for j := range eligAll {
		eligAll[j] = true
	}
	eligNoArt := make([]bool, n)
	for j := 0; j < numStruct; j++ {
		eligNoArt[j] = true
	}

	if numArt > 0 {
		// Phase 1: minimize sum of artificials.
		for j := numStruct; j < n; j++ {
			tab[0][j].SetInt64(1)
		}
		for i := 0; i < len(rows); i++ {
			if isArtificial(basis[i]) {
				for j := 0; j < width; j++ {
					tab[0][j].Sub(tab[0][j], tab[i+1][j])
				}
			}
		}
		simplex(tab, basis, eligAll)
		if tab[0][n].Sign() < 0 { // phase-1 optimum (= -tab[0][n]) > 0
			return lpInfeasible, nil, nil
		}
		// Drive remaining basic artificials out; drop redundant rows.
		for i := 0; i < len(basis); i++ {
			if !isArtificial(basis[i]) {
				continue
			}
			piv := -1
			for j := 0; j < numStruct; j++ {
				if tab[i+1][j].Sign() != 0 {
					piv = j
					break
				}
			}
			if piv >= 0 {
				pivot(tab, basis, i+1, piv)
				continue
			}
			// Row is redundant (all zeros in structural/slack columns, rhs 0).
			tab = append(tab[:i+1], tab[i+2:]...)
			basis = append(basis[:i], basis[i+1:]...)
			i--
		}
	}

	// Phase 2: original objective.
	for j := 0; j < width; j++ {
		tab[0][j].SetInt64(0)
	}
	for j := 0; j < numVars; j++ {
		tab[0][j].Set(objective[j])
	}
	for i := 0; i < len(basis); i++ {
		b := basis[i]
		if b < numVars && objective[b].Sign() != 0 {
			for j := 0; j < width; j++ {
				t := new(big.Rat).Mul(objective[b], tab[i+1][j])
				tab[0][j].Sub(tab[0][j], t)
			}
		}
	}
	if st := simplex(tab, basis, eligNoArt); st == lpUnbounded {
		return lpUnbounded, nil, nil
	}

	obj := new(big.Rat).Neg(tab[0][n])
	x := make([]*big.Rat, numVars)
	for j := range x {
		x[j] = zeroRat()
	}
	for i, b := range basis {
		if b < numVars {
			x[b].Set(tab[i+1][n])
		}
	}
	return lpOptimal, obj, x
}
