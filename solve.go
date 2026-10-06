package linsolve

import "math/big"

// row is a working row during elimination. combo expresses the row as a
// linear combination of the original equations: combo[e] is the coefficient
// of original equation e.
type row struct {
	coeffs map[int]*big.Rat
	rhs    *big.Rat
	combo  map[int]*big.Rat
}

// addScaled performs dst += factor * src on coeffs, rhs and combo.
// factor must be non-nil. Entries that become zero are dropped.
func (r *row) addScaled(src *row, factor *big.Rat) {
	tmp := new(big.Rat)
	for c, v := range src.coeffs {
		tmp.Mul(factor, v)
		if d, ok := r.coeffs[c]; ok {
			d.Add(d, tmp)
			if d.Sign() == 0 {
				delete(r.coeffs, c)
			}
		} else {
			r.coeffs[c] = new(big.Rat).Set(tmp)
		}
	}
	tmp.Mul(factor, src.rhs)
	r.rhs.Add(r.rhs, tmp)
	for e, v := range src.combo {
		tmp.Mul(factor, v)
		if d, ok := r.combo[e]; ok {
			d.Add(d, tmp)
			if d.Sign() == 0 {
				delete(r.combo, e)
			}
		} else {
			r.combo[e] = new(big.Rat).Set(tmp)
		}
	}
}

// scale multiplies the whole row by factor (factor must be non-zero).
func (r *row) scale(factor *big.Rat) {
	tmp := new(big.Rat)
	for _, v := range r.coeffs {
		v.Mul(v, factor)
	}
	r.rhs.Mul(r.rhs, tmp.Set(factor))
	for _, v := range r.combo {
		v.Mul(v, factor)
	}
}

// Solve solves the sparse rational system A x = b exactly and reports
// infeasibility (with certificate), a unique solution, or a parametric
// family of solutions. It uses exact rational Gauss-Jordan elimination;
// rank is determined by exact zero tests, never by floating point
// thresholds.
func Solve(sys *System) (*Result, error) {
	if err := sys.Validate(); err != nil {
		return nil, err
	}
	n := sys.NumVars

	rows := make([]*row, len(sys.Equations))
	for i, eq := range sys.Equations {
		r := &row{
			coeffs: make(map[int]*big.Rat, len(eq.Coeffs)),
			rhs:    new(big.Rat).Set(eq.RHS),
			combo:  map[int]*big.Rat{i: big.NewRat(1, 1)},
		}
		for c, v := range eq.Coeffs {
			if v.Sign() != 0 {
				r.coeffs[c] = new(big.Rat).Set(v)
			}
		}
		rows[i] = r
	}

	// Gauss-Jordan elimination over the rationals. pivotRow[c] is the row
	// reduced to have its pivot in column c.
	pivotRow := make(map[int]int) // column -> row index
	isPivot := make([]bool, len(rows))

	for c := 0; c < n; c++ {
		// Choose the sparsest non-pivot row having a non-zero entry in
		// column c (keeps fill-in low and is fully deterministic).
		piv := -1
		for i, r := range rows {
			if isPivot[i] {
				continue
			}
			if _, ok := r.coeffs[c]; !ok {
				continue
			}
			if piv < 0 || len(r.coeffs) < len(rows[piv].coeffs) {
				piv = i
			}
		}
		if piv < 0 {
			continue
		}
		pr := rows[piv]

		// Normalize so the pivot entry is exactly 1.
		inv := new(big.Rat).Inv(pr.coeffs[c])
		pr.scale(inv)

		// Eliminate column c from every other row.
		for i, r := range rows {
			if i == piv {
				continue
			}
			a, ok := r.coeffs[c]
			if !ok {
				continue
			}
			factor := new(big.Rat).Neg(a)
			r.addScaled(pr, factor)
		}

		pivotRow[c] = piv
		isPivot[piv] = true
	}

	// Any non-pivot row is now identically zero in its coefficients
	// (otherwise one of its non-zero columns would have pivoted). A zero
	// row with non-zero right-hand side proves infeasibility; its combo
	// is the certificate.
	for i, r := range rows {
		if isPivot[i] {
			continue
		}
		if r.rhs.Sign() != 0 {
			cert := make([]*big.Rat, len(rows))
			for e := range rows {
				if v, ok := r.combo[e]; ok {
					cert[e] = new(big.Rat).Set(v)
				} else {
					cert[e] = big.NewRat(0, 1)
				}
			}
			return &Result{Status: Infeasible, Certificate: cert}, nil
		}
	}

	// Free variables are the columns without a pivot, in increasing order.
	var freeVars []int
	for c := 0; c < n; c++ {
		if _, ok := pivotRow[c]; !ok {
			freeVars = append(freeVars, c)
		}
	}

	zeroVec := func() []*big.Rat {
		v := make([]*big.Rat, n)
		for i := range v {
			v[i] = big.NewRat(0, 1)
		}
		return v
	}

	// Particular solution: free variables = 0, pivot variables = rhs of
	// their pivot row (rows are in reduced row echelon form).
	particular := zeroVec()
	for c, ri := range pivotRow {
		particular[c] = new(big.Rat).Set(rows[ri].rhs)
	}

	if len(freeVars) == 0 {
		return &Result{Status: Unique, Solution: particular}, nil
	}

	// One direction per free variable, ordered by increasing variable
	// index: x_f = 1 for this free variable, 0 for the others, and pivot
	// variables read off the negated RREF coefficients.
	directions := make([][]*big.Rat, len(freeVars))
	for k, f := range freeVars {
		d := zeroVec()
		d[f] = big.NewRat(1, 1)
		for c, ri := range pivotRow {
			if v, ok := rows[ri].coeffs[f]; ok {
				d[c] = new(big.Rat).Neg(v)
			}
		}
		directions[k] = d
	}

	return &Result{
		Status:     Parametric,
		Particular: particular,
		Directions: directions,
		FreeVars:   freeVars,
	}, nil
}
