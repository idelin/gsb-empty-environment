package main

import "math/big"

type LPStatus int

const (
	LPOptimal LPStatus = iota
	LPUnbounded
	LPInfeasible
)

func (s LPStatus) String() string {
	switch s {
	case LPOptimal:
		return "optimal"
	case LPUnbounded:
		return "unbounded"
	default:
		return "infeasible"
	}
}

type LPResult struct {
	Status LPStatus
	Value  *big.Rat
	Point  []*big.Rat
}

// lpMaximizeFree solves max c.x subject to A.x <= b with x free, exactly
// over the rationals, by splitting x = xp - xm.
func lpMaximizeFree(A [][]*big.Rat, b []*big.Rat, c []*big.Rat) LPResult {
	n := len(c)
	if n == 0 {
		for _, bi := range b {
			if bi.Sign() < 0 {
				return LPResult{Status: LPInfeasible}
			}
		}
		return LPResult{Status: LPOptimal, Value: new(big.Rat), Point: []*big.Rat{}}
	}
	n2 := 2 * n
	A2 := make([][]*big.Rat, len(A))
	for i, row := range A {
		r := make([]*big.Rat, n2)
		for j := 0; j < n; j++ {
			r[2*j] = row[j]
			r[2*j+1] = new(big.Rat).Neg(row[j])
		}
		A2[i] = r
	}
	c2 := make([]*big.Rat, n2)
	for j := 0; j < n; j++ {
		c2[2*j] = c[j]
		c2[2*j+1] = new(big.Rat).Neg(c[j])
	}
	res := simplexMax(A2, b, c2)
	if res.Status != LPOptimal {
		return res
	}
	pt := make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		pt[j] = new(big.Rat).Sub(res.Point[2*j], res.Point[2*j+1])
	}
	return LPResult{Status: LPOptimal, Value: res.Value, Point: pt}
}

// simplexMax solves max c.x subject to A.x <= b, x >= 0, with Bland's rule.
func simplexMax(A [][]*big.Rat, b []*big.Rat, c []*big.Rat) LPResult {
	m := len(A)
	n := len(c)
	if m == 0 {
		for _, cj := range c {
			if cj.Sign() > 0 {
				return LPResult{Status: LPUnbounded}
			}
		}
		return LPResult{Status: LPOptimal, Value: new(big.Rat), Point: zeroVector(n)}
	}
	d := newDictionary(A, b, c)
	minRow := 0
	for i := 1; i < m; i++ {
		if d.b[i].Cmp(d.b[minRow]) < 0 {
			minRow = i
		}
	}
	if d.b[minRow].Sign() < 0 {
		d.addArtificial()
		d.pivot(minRow, len(d.nonbasic)-1)
		if d.solve() != LPOptimal {
			return LPResult{Status: LPInfeasible}
		}
		if d.v.Sign() < 0 {
			return LPResult{Status: LPInfeasible}
		}
		d.removeArtificial()
		d.setObjective(c)
	}
	if d.solve() == LPUnbounded {
		return LPResult{Status: LPUnbounded}
	}
	pt := zeroVector(n)
	for i := 0; i < d.m; i++ {
		if d.basic[i] < n {
			pt[d.basic[i]] = new(big.Rat).Set(d.b[i])
		}
	}
	return LPResult{Status: LPOptimal, Value: new(big.Rat).Set(d.v), Point: pt}
}

// dictionary stores rows as x_B[i] = b[i] - sum_j a[i][j] * x_N[j] and the
// objective as z = v + sum_j c[j] * x_N[j].
type dictionary struct {
	m          int
	basic      []int
	nonbasic   []int
	a          [][]*big.Rat
	b          []*big.Rat
	c          []*big.Rat
	v          *big.Rat
	artificial int
}

func newDictionary(A [][]*big.Rat, b []*big.Rat, c []*big.Rat) *dictionary {
	m := len(A)
	n := len(c)
	d := &dictionary{m: m, artificial: -1}
	d.basic = make([]int, m)
	for i := range d.basic {
		d.basic[i] = n + i
	}
	d.nonbasic = make([]int, n)
	for j := range d.nonbasic {
		d.nonbasic[j] = j
	}
	d.a = make([][]*big.Rat, m)
	for i := range A {
		row := make([]*big.Rat, n)
		for j := range row {
			row[j] = new(big.Rat).Set(A[i][j])
		}
		d.a[i] = row
	}
	d.b = make([]*big.Rat, m)
	for i := range b {
		d.b[i] = new(big.Rat).Set(b[i])
	}
	d.c = make([]*big.Rat, n)
	for j := range c {
		d.c[j] = new(big.Rat).Set(c[j])
	}
	d.v = new(big.Rat)
	return d
}

func (d *dictionary) maxVar() int {
	max := -1
	for _, v := range d.basic {
		if v > max {
			max = v
		}
	}
	for _, v := range d.nonbasic {
		if v > max {
			max = v
		}
	}
	return max
}

func (d *dictionary) addArtificial() {
	idx := d.maxVar() + 1
	d.artificial = idx
	for i := 0; i < d.m; i++ {
		d.a[i] = append(d.a[i], big.NewRat(-1, 1))
	}
	d.nonbasic = append(d.nonbasic, idx)
	c := make([]*big.Rat, len(d.nonbasic))
	for j := range c {
		c[j] = new(big.Rat)
	}
	c[len(c)-1] = big.NewRat(-1, 1)
	d.c = c
	d.v = new(big.Rat)
}

func (d *dictionary) removeArtificial() {
	for i := 0; i < d.m; i++ {
		if d.basic[i] == d.artificial {
			pivoted := false
			for j := 0; j < len(d.nonbasic); j++ {
				if d.nonbasic[j] != d.artificial && d.a[i][j].Sign() != 0 {
					d.pivot(i, j)
					pivoted = true
					break
				}
			}
			if !pivoted {
				d.a = append(d.a[:i], d.a[i+1:]...)
				d.b = append(d.b[:i], d.b[i+1:]...)
				d.basic = append(d.basic[:i], d.basic[i+1:]...)
				d.m--
			}
			break
		}
	}
	col := -1
	for j, v := range d.nonbasic {
		if v == d.artificial {
			col = j
			break
		}
	}
	if col >= 0 {
		d.nonbasic = append(d.nonbasic[:col], d.nonbasic[col+1:]...)
		for i := 0; i < d.m; i++ {
			d.a[i] = append(d.a[i][:col], d.a[i][col+1:]...)
		}
		d.c = append(d.c[:col], d.c[col+1:]...)
	}
	d.artificial = -1
}

// setObjective re-expresses z = c.x in terms of the current nonbasic vars.
func (d *dictionary) setObjective(c []*big.Rat) {
	nc := len(d.nonbasic)
	newC := make([]*big.Rat, nc)
	for j := range newC {
		newC[j] = new(big.Rat)
	}
	newV := new(big.Rat)
	for j := 0; j < len(c); j++ {
		if c[j].Sign() == 0 {
			continue
		}
		if col := indexOf(d.nonbasic, j); col >= 0 {
			newC[col].Add(newC[col], c[j])
		} else if row := indexOf(d.basic, j); row >= 0 {
			newV.Add(newV, new(big.Rat).Mul(c[j], d.b[row]))
			for k := 0; k < nc; k++ {
				newC[k].Sub(newC[k], new(big.Rat).Mul(c[j], d.a[row][k]))
			}
		}
	}
	d.c = newC
	d.v = newV
}

func indexOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func (d *dictionary) solve() LPStatus {
	for {
		enter := -1
		for j, v := range d.nonbasic {
			if d.c[j].Sign() > 0 && (enter == -1 || v < d.nonbasic[enter]) {
				enter = j
			}
		}
		if enter == -1 {
			return LPOptimal
		}
		leave := -1
		var best *big.Rat
		for i := 0; i < d.m; i++ {
			if d.a[i][enter].Sign() > 0 {
				r := new(big.Rat).Quo(d.b[i], d.a[i][enter])
				if leave == -1 || r.Cmp(best) < 0 ||
					(r.Cmp(best) == 0 && d.basic[i] < d.basic[leave]) {
					leave = i
					best = r
				}
			}
		}
		if leave == -1 {
			return LPUnbounded
		}
		d.pivot(leave, enter)
	}
}

func (d *dictionary) pivot(leaveRow, enterCol int) {
	e := d.nonbasic[enterCol]
	l := d.basic[leaveRow]
	aie := d.a[leaveRow][enterCol]
	nc := len(d.nonbasic)
	newRow := make([]*big.Rat, nc)
	newB := new(big.Rat).Quo(d.b[leaveRow], aie)
	for j := 0; j < nc; j++ {
		if j == enterCol {
			continue
		}
		newRow[j] = new(big.Rat).Quo(d.a[leaveRow][j], aie)
	}
	newRow[enterCol] = new(big.Rat).Inv(aie)
	for i := 0; i < d.m; i++ {
		if i == leaveRow {
			continue
		}
		ake := d.a[i][enterCol]
		if ake.Sign() == 0 {
			continue
		}
		d.b[i] = new(big.Rat).Sub(d.b[i], new(big.Rat).Mul(ake, newB))
		for j := 0; j < nc; j++ {
			if j == enterCol {
				continue
			}
			d.a[i][j] = new(big.Rat).Sub(d.a[i][j], new(big.Rat).Mul(ake, newRow[j]))
		}
		d.a[i][enterCol] = new(big.Rat).Neg(new(big.Rat).Mul(ake, newRow[enterCol]))
	}
	ce := d.c[enterCol]
	if ce.Sign() != 0 {
		d.v = new(big.Rat).Add(d.v, new(big.Rat).Mul(ce, newB))
		for j := 0; j < nc; j++ {
			if j == enterCol {
				continue
			}
			d.c[j] = new(big.Rat).Sub(d.c[j], new(big.Rat).Mul(ce, newRow[j]))
		}
		d.c[enterCol] = new(big.Rat).Neg(new(big.Rat).Mul(ce, newRow[enterCol]))
	}
	d.a[leaveRow] = newRow
	d.b[leaveRow] = newB
	d.basic[leaveRow] = e
	d.nonbasic[enterCol] = l
}

// feasibleStrict checks {A x <= b, C x < d} for feasibility over the
// rationals by maximizing a slack variable delta with C x + delta <= d.
// It returns a witness point when feasible.
func feasibleStrict(leA [][]*big.Rat, leB []*big.Rat, ltA [][]*big.Rat, ltB []*big.Rat, nvars int) (bool, []*big.Rat) {
	if len(ltA) == 0 {
		res := lpMaximizeFree(leA, leB, zeroVector(nvars))
		if res.Status == LPInfeasible {
			return false, nil
		}
		return true, res.Point
	}
	n1 := nvars + 1
	var A [][]*big.Rat
	var b []*big.Rat
	for i, row := range leA {
		r := make([]*big.Rat, n1)
		copy(r, row)
		r[nvars] = new(big.Rat)
		A = append(A, r)
		b = append(b, leB[i])
	}
	for i, row := range ltA {
		r := make([]*big.Rat, n1)
		copy(r, row)
		r[nvars] = big.NewRat(1, 1)
		A = append(A, r)
		b = append(b, ltB[i])
	}
	capRow := zeroVector(n1)
	capRow[nvars] = big.NewRat(1, 1)
	A = append(A, capRow)
	b = append(b, big.NewRat(1, 1))
	c := zeroVector(n1)
	c[nvars] = big.NewRat(1, 1)
	res := lpMaximizeFree(A, b, c)
	if res.Status != LPOptimal || res.Value.Sign() <= 0 {
		return false, nil
	}
	return true, res.Point[:nvars]
}

// isUnbounded reports whether the feasible region {A x <= b, C x < d} has a
// nonzero recession direction, checked coordinate by coordinate.
func isUnbounded(leA [][]*big.Rat, ltA [][]*big.Rat, nvars int) bool {
	if nvars == 0 {
		return false
	}
	var A [][]*big.Rat
	var b []*big.Rat
	addRows := func(rows [][]*big.Rat) {
		for _, r := range rows {
			A = append(A, r)
			b = append(b, new(big.Rat))
		}
	}
	addRows(leA)
	addRows(ltA)
	for j := 0; j < nvars; j++ {
		e := unitVector(nvars, j)
		A = append(A, e)
		b = append(b, big.NewRat(1, 1))
		A = append(A, negCoeffs(e))
		b = append(b, big.NewRat(1, 1))
	}
	for j := 0; j < nvars; j++ {
		for _, sign := range []int64{1, -1} {
			c := zeroVector(nvars)
			c[j] = big.NewRat(sign, 1)
			res := lpMaximizeFree(A, b, c)
			if res.Status == LPOptimal && res.Value.Sign() > 0 {
				return true
			}
		}
	}
	return false
}
