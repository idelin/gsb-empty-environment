package netsec

import (
	"math/big"
)

// DefaultMaxOutputs bounds the number of observable outputs per program.
const DefaultMaxOutputs = 20

// DefaultBudget bounds how many output subsets the solver examines.
const DefaultBudget int64 = 1 << 20

// DefaultSampleTrials is the number of small-integer sample evaluations
// used to cross-check every reported combination.
const DefaultSampleTrials = 8

// Options tunes the solver. Zero fields take defaults.
type Options struct {
	// MaxOutputs caps the number of program outputs (default 20).
	MaxOutputs int
	// Budget caps how many output subsets are examined (default 1<<20).
	Budget int64
	// SampleTrials sets the sample-verification rounds (default 8).
	SampleTrials int
}

func (o *Options) withDefaults() Options {
	out := Options{MaxOutputs: DefaultMaxOutputs, Budget: DefaultBudget, SampleTrials: DefaultSampleTrials}
	if o != nil {
		if o.MaxOutputs > 0 {
			out.MaxOutputs = o.MaxOutputs
		}
		if o.Budget > 0 {
			out.Budget = o.Budget
		}
		if o.SampleTrials > 0 {
			out.SampleTrials = o.SampleTrials
		}
	}
	return out
}

// Combination is a verified witness: a set of outputs and rational
// coefficients whose linear combination cancels every random term while
// preserving secret terms.
type Combination struct {
	// Outputs lists the participating output names, in program order.
	Outputs []string
	// Coeffs maps each participating output name to its rational
	// coefficient, normalized to coprime integers.
	Coeffs map[string]*big.Rat
	// Secret is the resulting secret part of the combined expression.
	Secret Expr
}

// Result summarizes one Solve run.
type Result struct {
	// Combinations holds every verified combination found at the smallest
	// feasible size. Empty when no combination exists (or none was reached
	// within budget).
	Combinations []Combination
	// MinimalProven is true only when the search space was fully explored
	// up to and including the reported size, so the reported combinations
	// are proven minimum-size and the list is complete at that size.
	MinimalProven bool
	// BudgetExhausted is true when the search stopped early because the
	// subset budget ran out; any returned combinations are still verified
	// but their minimality is unproven.
	BudgetExhausted bool
	// SubsetsExamined counts the output subsets actually tested.
	SubsetsExamined int64
}

// Solve searches for minimum-size output combinations whose rational linear
// combination eliminates all random terms and preserves at least one secret
// term. Subsets are enumerated by increasing size, so the first feasible
// size is minimal; every candidate is verified symbolically (elimination
// certificate) and numerically (small-integer sampling) before being
// reported.
func Solve(p *Program, opts *Options) (*Result, error) {
	o := opts.withDefaults()
	kind, err := p.validate(o.MaxOutputs)
	if err != nil {
		return nil, err
	}

	secrets := p.Secrets
	randoms := p.Randoms
	n := len(p.Outputs)

	res := &Result{}

	examine := func(subset []int) (bool, error) {
		// The boolean result signals early stop on budget exhaustion.
		if res.SubsetsExamined >= o.Budget {
			return true, nil
		}
		res.SubsetsExamined++
		coeffs, ok := eliminate(p, subset, secrets, randoms)
		if !ok {
			return false, nil
		}
		comb := Combination{
			Outputs: make([]string, len(subset)),
			Coeffs:  make(map[string]*big.Rat, len(subset)),
		}
		for i, j := range subset {
			name := p.Outputs[j].Name
			comb.Outputs[i] = name
			comb.Coeffs[name] = new(big.Rat).Set(coeffs[i])
		}
		normalizeCoeffs(comb.Coeffs, comb.Outputs)
		secret, err := certify(p, kind, &comb)
		if err != nil {
			return false, err
		}
		comb.Secret = secret
		if err := SampleVerify(p, &comb, o.SampleTrials, nil); err != nil {
			return false, err
		}
		res.Combinations = append(res.Combinations, comb)
		return false, nil
	}

	// Enumerate subsets by increasing size; the first feasible size is
	// minimal. Once a size yields solutions, finish that size (budget
	// permitting) to collect all minimum-size witnesses.
	for size := 1; size <= n; size++ {
		stop, err := enumerateSubsets(n, size, nil, examine)
		if err != nil {
			return nil, err
		}
		if stop {
			res.BudgetExhausted = true
		}
		if len(res.Combinations) > 0 {
			break
		}
	}
	res.MinimalProven = len(res.Combinations) > 0 && !res.BudgetExhausted
	return res, nil
}

// enumerateSubsets calls fn for every size-k subset of [0,n). The callback
// signals early termination by returning true; enumeration stops and true
// is returned.
func enumerateSubsets(n, k int, prefix []int, fn func([]int) (bool, error)) (bool, error) {
	if len(prefix) == k {
		return fn(prefix)
	}
	lo := 0
	if len(prefix) > 0 {
		lo = prefix[len(prefix)-1] + 1
	}
	for i := lo; i <= n-(k-len(prefix)); i++ {
		stop, err := enumerateSubsets(n, k, append(prefix, i), fn)
		if err != nil || stop {
			return stop, err
		}
	}
	return false, nil
}

// eliminate decides whether some rational combination of the given output
// subset cancels all random terms while keeping a nonzero secret part, and
// if so returns one such coefficient vector.
//
// Let R (|randoms| x k) and S (|secrets| x k) be the random and secret
// coefficient matrices of the subset. A witness is c in null(R) with
// S·c != 0; it exists iff some null-space basis vector of R is not
// annihilated by S.
func eliminate(p *Program, subset []int, secrets, randoms []string) ([]*big.Rat, bool) {
	k := len(subset)
	rmat := make([][]*big.Rat, len(randoms))
	for i, id := range randoms {
		row := make([]*big.Rat, k)
		for j, o := range subset {
			row[j] = p.Outputs[o].Expr.coeff(id)
		}
		rmat[i] = row
	}
	basis := nullspace(rmat, k)
	for _, b := range basis {
		if secretNonzero(p, subset, secrets, b) {
			return b, true
		}
	}
	return nil, false
}

// secretNonzero reports whether the combination of subset outputs with
// coefficients c has a nonzero secret part.
func secretNonzero(p *Program, subset []int, secrets []string, c []*big.Rat) bool {
	for _, id := range secrets {
		acc := new(big.Rat)
		for j, o := range subset {
			acc.Add(acc, new(big.Rat).Mul(c[j], p.Outputs[o].Expr.coeff(id)))
		}
		if acc.Sign() != 0 {
			return true
		}
	}
	return false
}

// rref reduces mat (rational, modified in place) to reduced row echelon
// form and returns the pivot column indices.
func rref(mat [][]*big.Rat, cols int) []int {
	pivots := []int{}
	row := 0
	for col := 0; col < cols && row < len(mat); col++ {
		piv := -1
		for r := row; r < len(mat); r++ {
			if mat[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			continue
		}
		mat[row], mat[piv] = mat[piv], mat[row]
		inv := new(big.Rat).Inv(mat[row][col])
		for c := col; c < cols; c++ {
			mat[row][c].Mul(mat[row][c], inv)
		}
		for r := 0; r < len(mat); r++ {
			if r == row || mat[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(mat[r][col])
			for c := col; c < cols; c++ {
				mat[r][c].Sub(mat[r][c], new(big.Rat).Mul(f, mat[row][c]))
			}
		}
		pivots = append(pivots, col)
		row++
	}
	return pivots
}

// nullspace returns a basis of {x in Q^cols : mat·x = 0} over the
// rationals. mat is consumed.
func nullspace(mat [][]*big.Rat, cols int) [][]*big.Rat {
	pivots := rref(mat, cols)
	isPivot := make([]bool, cols)
	for _, c := range pivots {
		isPivot[c] = true
	}
	var basis [][]*big.Rat
	for free := 0; free < cols; free++ {
		if isPivot[free] {
			continue
		}
		b := make([]*big.Rat, cols)
		for i := range b {
			b[i] = new(big.Rat)
		}
		b[free].SetInt64(1)
		for i, pc := range pivots {
			// Row i of the RREF: x_pc + sum_free mat[i][f]·x_f = 0.
			b[pc] = new(big.Rat).Neg(mat[i][free])
		}
		basis = append(basis, b)
	}
	return basis
}

// normalizeCoeffs rescales rational coefficients to coprime integers for
// readable certificates.
func normalizeCoeffs(coeffs map[string]*big.Rat, names []string) {
	lcm := big.NewInt(1)
	for _, n := range names {
		d := coeffs[n].Denom()
		lcm.Mul(lcm, new(big.Int).Quo(d, new(big.Int).GCD(nil, nil, lcm, d)))
	}
	gcd := new(big.Int)
	for _, n := range names {
		num := new(big.Int).Mul(coeffs[n].Num(), new(big.Int).Quo(lcm, coeffs[n].Denom()))
		coeffs[n].SetInt(num)
		gcd.GCD(nil, nil, gcd, num)
	}
	if gcd.Sign() != 0 {
		for _, n := range names {
			num := new(big.Int).Quo(coeffs[n].Num(), gcd)
			coeffs[n].SetInt(num)
		}
	}
	// Fix the sign so the first coefficient is positive.
	if coeffs[names[0]].Sign() < 0 {
		for _, n := range names {
			coeffs[n].Neg(coeffs[n])
		}
	}
}
