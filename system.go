package admission

import (
	"fmt"
	"math/big"
	"sort"
)

// ---------------------------------------------------------------------------
// Prime-power decomposition
// ---------------------------------------------------------------------------

type primeRow struct {
	v       int
	modulus *big.Int
	coeffs  []*big.Int // aligned with primeSystem.varIdx
	target  *big.Int
}

type primeSystem struct {
	p      *big.Int
	varIdx []int
	varExp []int
	rows   []primeRow
}

type primeResult struct {
	system     *primeSystem
	invariants []*big.Int
	index      *big.Int
	domain     *big.Int
	codomain   *big.Int
	consistent bool
	kernel     *big.Int
	bmat       *intMatrix
	u          *intMatrix
	v          *intMatrix
	x0         []uint64
}

type systemCount struct {
	consistent bool
	domain     *big.Int
	kernel     *big.Int
	primes     []*primeResult
}

func buildPrimeSystems(rows []internalRow, vars []variableInfo) ([]*primeSystem, error) {
	primeSet := make(map[string]bool)
	for _, v := range vars {
		for p := range v.factors {
			primeSet[p] = true
		}
	}
	for _, r := range rows {
		for p := range r.factors {
			primeSet[p] = true
		}
	}
	primes := sortedPrimeStrings(primeSet)
	out := make([]*primeSystem, 0, len(primes))
	for _, pstr := range primes {
		p, ok := new(big.Int).SetString(pstr, 10)
		if !ok {
			return nil, admissionError(CodeInternal, "crt", "", "bad prime %q", pstr)
		}
		var varIdx []int
		var varExp []int
		for j, v := range vars {
			if e := v.factors[pstr]; e > 0 {
				varIdx = append(varIdx, j)
				varExp = append(varExp, e)
			}
		}
		ps := &primeSystem{p: p, varIdx: varIdx, varExp: varExp}
		for _, r := range rows {
			v := r.factors[pstr]
			if v <= 0 {
				continue
			}
			mod := powInt(p, v)
			coeffMap := make(map[int]*big.Int, len(r.terms))
			for _, t := range r.terms {
				coeffMap[t.varIndex] = t.coeff
			}
			coeffs := make([]*big.Int, len(varIdx))
			for j, orig := range varIdx {
				c := coeffMap[orig]
				if c == nil {
					coeffs[j] = new(big.Int)
				} else {
					coeffs[j] = modInt(c, mod)
				}
			}
			ps.rows = append(ps.rows, primeRow{
				v: v, modulus: mod, coeffs: coeffs, target: modInt(r.target, mod),
			})
		}
		if len(ps.varIdx) == 0 && len(ps.rows) == 0 {
			continue
		}
		out = append(out, ps)
	}
	return out, nil
}

func countSystem(rows []internalRow, vars []variableInfo) (*systemCount, error) {
	systems, err := buildPrimeSystems(rows, vars)
	if err != nil {
		return nil, err
	}
	res := &systemCount{consistent: true, domain: big.NewInt(1), kernel: big.NewInt(1)}
	for _, ps := range systems {
		pr, err := solvePrimeSystem(ps)
		if err != nil {
			return nil, err
		}
		res.primes = append(res.primes, pr)
		res.domain.Mul(res.domain, pr.domain)
		if !pr.consistent {
			res.consistent = false
			res.kernel.SetInt64(0)
		} else if res.consistent {
			res.kernel.Mul(res.kernel, pr.kernel)
		}
	}
	return res, nil
}

func solvePrimeSystem(ps *primeSystem) (*primeResult, error) {
	n := len(ps.varIdx)
	m := len(ps.rows)
	res := &primeResult{system: ps, consistent: true}
	res.domain = big.NewInt(1)
	for _, e := range ps.varExp {
		res.domain.Mul(res.domain, powInt(ps.p, e))
	}
	res.codomain = big.NewInt(1)
	for _, r := range ps.rows {
		res.codomain.Mul(res.codomain, r.modulus)
	}
	if m == 0 {
		res.index = big.NewInt(1)
		res.kernel = new(big.Int).Set(res.domain)
		res.x0 = make([]uint64, n)
		return res, nil
	}

	b := newIntMatrix(m, n+m)
	for i, r := range ps.rows {
		for j := 0; j < n; j++ {
			b.A[i][j].Set(r.coeffs[j])
		}
		b.A[i][n+i].Set(powInt(ps.p, r.v))
	}
	d, u, v, err := smithNormalForm(b)
	if err != nil {
		return nil, admissionError(CodeInternal, "snf", "", "%v", err)
	}
	res.bmat = b
	res.u = u
	res.v = v
	res.index = big.NewInt(1)
	res.invariants = make([]*big.Int, m)
	for i := 0; i < m; i++ {
		res.invariants[i] = cloneInt(d.A[i][i])
		res.index.Mul(res.index, d.A[i][i])
	}

	bvec := newIntMatrix(m, 1)
	for i, r := range ps.rows {
		bvec.A[i][0].Set(r.target)
	}
	y := matMul(u, bvec)
	for i := 0; i < m; i++ {
		if d.A[i][i].Sign() == 0 {
			if y.A[i][0].Sign() != 0 {
				res.consistent = false
				break
			}
			continue
		}
		if new(big.Int).Mod(y.A[i][0], d.A[i][i]).Sign() != 0 {
			res.consistent = false
			break
		}
	}
	if !res.consistent {
		res.kernel = big.NewInt(0)
		return res, nil
	}

	rhs := newIntMatrix(n+m, 1)
	for i := 0; i < m; i++ {
		if d.A[i][i].Sign() != 0 {
			rhs.A[i][0].Quo(y.A[i][0], d.A[i][i])
		}
	}
	z := matMul(v, rhs)
	res.x0 = make([]uint64, n)
	for j := 0; j < n; j++ {
		pe := powInt(ps.p, ps.varExp[j])
		r := modInt(z.A[j][0], pe)
		res.x0[j] = r.Uint64()
	}
	for _, r := range ps.rows {
		acc := new(big.Int)
		for j, c := range r.coeffs {
			acc.Add(acc, new(big.Int).Mul(c, new(big.Int).SetUint64(res.x0[j])))
		}
		if modInt(acc, r.modulus).Cmp(r.target) != 0 {
			return nil, admissionError(CodeInternal, "solve", "", "particular solution failed prime-power check")
		}
	}

	// |ker| = |D| * index / |C|.  This must be an integer.
	num := new(big.Int).Mul(res.domain, res.index)
	q, rem := new(big.Int).QuoRem(num, res.codomain, new(big.Int))
	if rem.Sign() != 0 {
		return nil, admissionError(CodeInternal, "snf", "", "non-integral kernel cardinality for prime %s", ps.p)
	}
	res.kernel = q
	return res, nil
}

// ---------------------------------------------------------------------------
// Exact enumeration of the affine solution set
// ---------------------------------------------------------------------------

type enumCallbacks struct {
	onPoint func(x []uint64) bool
}

type enumResult struct {
	feasible *big.Int
	domain   *big.Int
	kernel   *big.Int
}

type primeKernelBasis struct {
	orders []*big.Int
	gens   [][]uint64 // each generator has len(varIdx)
	mods   []uint64
}

func buildPrimeKernelBasis(pr *primeResult) (*primeKernelBasis, error) {
	ps := pr.system
	n := len(ps.varIdx)
	m := len(ps.rows)
	basis := &primeKernelBasis{}
	basis.mods = make([]uint64, n)
	for j := 0; j < n; j++ {
		basis.mods[j] = powInt(ps.p, ps.varExp[j]).Uint64()
	}
	if n == 0 {
		return basis, nil
	}
	var bx *intMatrix
	if m == 0 {
		bx = identityMatrix(n)
	} else {
		bx = newIntMatrix(n, n)
		for l := 0; l < n; l++ {
			col := m + l
			for j := 0; j < n; j++ {
				bx.A[j][l].Set(pr.v.A[j][col])
			}
		}
	}

	// M = Bx^{-1} * diag(p^{e_j}); the quotient Z^n / M Z^n is the kernel.
	mm := newIntMatrix(n, n)
	for j := 0; j < n; j++ {
		rhs := make([]*big.Int, n)
		for i := range rhs {
			rhs[i] = new(big.Int)
		}
		pe := powInt(ps.p, ps.varExp[j])
		rhs[j].Set(pe)
		col, err := solveExactInteger(bx, rhs)
		if err != nil {
			return nil, admissionError(CodeInternal, "kernel-basis", "", "%v", err)
		}
		for i := 0; i < n; i++ {
			mm.A[i][j].Set(col[i])
		}
	}
	dm, um, _, err := smithNormalForm(mm)
	if err != nil {
		return nil, admissionError(CodeInternal, "kernel-basis", "", "%v", err)
	}
	prod := big.NewInt(1)
	for i := 0; i < n; i++ {
		d := dm.A[i][i]
		if d.Sign() <= 0 {
			return nil, admissionError(CodeInternal, "kernel-basis", "", "singular quotient matrix")
		}
		basis.orders = append(basis.orders, cloneInt(d))
		prod.Mul(prod, d)
		gen := make([]uint64, n)
		for j := 0; j < n; j++ {
			acc := new(big.Int)
			for l := 0; l < n; l++ {
				acc.Add(acc, new(big.Int).Mul(bx.A[j][l], um.A[l][i]))
			}
			pe := powInt(ps.p, ps.varExp[j])
			gen[j] = modInt(acc, pe).Uint64()
		}
		for _, r := range ps.rows {
			acc := new(big.Int)
			for j, c := range r.coeffs {
				acc.Add(acc, new(big.Int).Mul(c, new(big.Int).SetUint64(gen[j])))
			}
			if modInt(acc, r.modulus).Sign() != 0 {
				return nil, admissionError(CodeInternal, "kernel-basis", "", "generator failed homogeneous check")
			}
		}
		basis.gens = append(basis.gens, gen)
	}
	if prod.Cmp(pr.kernel) != 0 {
		return nil, admissionError(CodeInternal, "kernel-basis", "", "kernel basis size %s != analytic %s", prod, pr.kernel)
	}
	if len(basis.orders) != n {
		return nil, admissionError(CodeInternal, "kernel-basis", "", "kernel basis rank mismatch")
	}
	return basis, nil
}

func (b *primeKernelBasis) forEachPoint(fn func(point []uint64) error) error {
	n := len(b.mods)
	if n == 0 {
		return fn(make([]uint64, n))
	}
	if len(b.orders) == 0 {
		return admissionError(CodeInternal, "enumerate", "", "missing kernel generators")
	}
	digits := make([]int, len(b.orders))
	var rec func(k int) error
	rec = func(k int) error {
		if k == len(digits) {
			point := make([]uint64, n)
			for d, c := range digits {
				if c == 0 {
					continue
				}
				cc := uint64(c)
				for j := 0; j < n; j++ {
					prod := new(big.Int).Mul(new(big.Int).SetUint64(cc), new(big.Int).SetUint64(b.gens[d][j]))
					prod.Mod(prod, new(big.Int).SetUint64(b.mods[j]))
					point[j] = (point[j] + prod.Uint64()) % b.mods[j]
				}
			}
			return fn(point)
		}
		if !b.orders[k].IsUint64() {
			return admissionError(CodeEnumerationLimit, "enumerate", "", "prime kernel basis order is too large")
		}
		ord := int(b.orders[k].Uint64())
		for c := 0; c < ord; c++ {
			digits[k] = c
			if err := rec(k + 1); err != nil {
				return err
			}
		}
		return nil
	}
	return rec(0)
}

func solveExactInteger(a *intMatrix, b []*big.Int) ([]*big.Int, error) {
	n := a.Rows
	if a.Cols != n || len(b) != n {
		return nil, fmt.Errorf("solveExactInteger: non-square system")
	}
	aug := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			aug[i][j] = new(big.Rat).SetInt(a.A[i][j])
		}
		aug[i][n] = new(big.Rat).SetInt(b[i])
	}
	for col := 0; col < n; col++ {
		pivot := -1
		for r := col; r < n; r++ {
			if aug[r][col].Sign() != 0 {
				pivot = r
				break
			}
		}
		if pivot < 0 {
			return nil, fmt.Errorf("solveExactInteger: singular matrix")
		}
		aug[col], aug[pivot] = aug[pivot], aug[col]
		p := new(big.Rat).Set(aug[col][col])
		for j := col; j <= n; j++ {
			aug[col][j].Quo(aug[col][j], p)
		}
		for r := 0; r < n; r++ {
			if r == col || aug[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(aug[r][col])
			for j := col; j <= n; j++ {
				aug[r][j].Sub(aug[r][j], new(big.Rat).Mul(f, aug[col][j]))
			}
		}
	}
	out := make([]*big.Int, n)
	for i := 0; i < n; i++ {
		if aug[i][n].Denom().Cmp(big.NewInt(1)) != 0 {
			return nil, fmt.Errorf("solveExactInteger: non-integer solution")
		}
		out[i] = new(big.Int).Set(aug[i][n].Num())
	}
	return out, nil
}

// enumerateSystem enumerates the exact affine solution set, checking the
// callback for every point.  It never estimates: if the exact set is larger
// than the configured bound it returns ErrEnumerationLimit.
func enumerateSystem(rows []internalRow, vars []variableInfo, cap uint64, cb enumCallbacks) (*enumResult, error) {
	count, err := countSystem(rows, vars)
	if err != nil {
		return nil, err
	}
	if !count.consistent {
		return &enumResult{feasible: big.NewInt(0), domain: count.domain, kernel: big.NewInt(0)}, nil
	}
	if cap == 0 {
		cap = defaultMaxEnumeration
	}
	if count.kernel.Cmp(new(big.Int).SetUint64(cap)) > 0 {
		return nil, admissionError(CodeEnumerationLimit, "enumerate", "",
			"exact solution set has %s elements, above limit %d", count.kernel, cap)
	}
	bases := make([]*primeKernelBasis, len(count.primes))
	for i, pr := range count.primes {
		b, err := buildPrimeKernelBasis(pr)
		if err != nil {
			return nil, err
		}
		bases[i] = b
	}
	// Precompute, per original variable, the prime components that make up
	// its CRT decomposition.
	type varPart struct {
		prime int
		pos   int
		pe    *big.Int
	}
	parts := make([][]varPart, len(vars))
	for pi, pr := range count.primes {
		for pos, orig := range pr.system.varIdx {
			parts[orig] = append(parts[orig], varPart{prime: pi, pos: pos, pe: powInt(pr.system.p, pr.system.varExp[pos])})
		}
	}

	chosen := make([][]uint64, len(bases))
	feasible := new(big.Int)
	var walk func(pi int) error
	walk = func(pi int) error {
		if pi == len(bases) {
			x := make([]uint64, len(vars))
			for j := range vars {
				r := new(big.Int)
				mod := big.NewInt(1)
				for _, part := range parts[j] {
					pr := count.primes[part.prime]
					coeff := chosen[part.prime][part.pos]
					residue := (pr.x0[part.pos] + coeff) % part.pe.Uint64()
					rr := new(big.Int).SetUint64(residue)
					nr, nm, err := crt2(r, mod, rr, part.pe)
					if err != nil {
						return admissionError(CodeInternal, "enumerate", "", "CRT: %v", err)
					}
					r, mod = nr, nm
				}
				x[j] = r.Uint64()
			}
			if cb.onPoint == nil || cb.onPoint(x) {
				feasible.Add(feasible, big.NewInt(1))
			}
			return nil
		}
		return bases[pi].forEachPoint(func(pt []uint64) error {
			chosen[pi] = pt
			return walk(pi + 1)
		})
	}
	if err := walk(0); err != nil {
		return nil, err
	}
	return &enumResult{feasible: feasible, domain: count.domain, kernel: count.kernel}, nil
}

// exactProjectionCount computes |P(K)| = |ker Phi| / |ker(Phi,P)| for a pure
// linear system.  It is the joint projection cardinality, not a product of
// marginals.
func exactProjectionCount(rows []internalRow, vars []variableInfo, forms []internalForm) (*big.Int, *systemCount, *systemCount, error) {
	base, err := countSystem(rows, vars)
	if err != nil {
		return nil, nil, nil, err
	}
	if !base.consistent {
		return big.NewInt(0), base, nil, nil
	}
	formRows, err := formsToRows(forms)
	if err != nil {
		return nil, nil, nil, err
	}
	// The projection of an affine solution x0+K is P(x0)+P(K).  Its
	// cardinality is |K|/|K intersect ker P|, which is independent of x0.
	// Therefore the denominator must use the *homogeneous* base system.
	hom := homogenizeRows(rows)
	combined, err := countSystem(append(hom, formRows...), vars)
	if err != nil {
		return nil, nil, nil, err
	}
	if !combined.consistent {
		return big.NewInt(0), base, combined, nil
	}
	q, rem := new(big.Int).QuoRem(base.kernel, combined.kernel, new(big.Int))
	if rem.Sign() != 0 {
		return nil, nil, nil, admissionError(CodeInternal, "projection", "", "non-integral projection cardinality")
	}
	return q, base, combined, nil
}

func homogenizeRows(rows []internalRow) []internalRow {
	out := make([]internalRow, len(rows))
	for i, r := range rows {
		out[i] = r
		out[i].target = new(big.Int)
	}
	return out
}

func formsToRows(forms []internalForm) ([]internalRow, error) {
	out := make([]internalRow, 0, len(forms))
	for _, f := range forms {
		if f.exact {
			return nil, admissionError(CodeEnumerationLimit, "projection", "",
				"exact integer protected form %q requires enumeration", f.name)
		}
		out = append(out, internalRow{
			id: f.name, kind: "protected", modulus: cloneInt(f.modulus),
			factors: f.factors, terms: f.terms, target: new(big.Int),
		})
	}
	return out, nil
}

func canonicalPrimeRows(ps *primeSystem, vars []variableInfo) []canonicalPrimeRow {
	out := make([]canonicalPrimeRow, 0, len(ps.rows))
	for _, r := range ps.rows {
		terms := make([]canonicalTerm, 0, len(r.coeffs))
		for j, c := range r.coeffs {
			if c.Sign() == 0 {
				continue
			}
			terms = append(terms, canonicalTerm{
				Variable: vars[ps.varIdx[j]].name, Coefficient: c.String(),
			})
		}
		sort.Slice(terms, func(i, j int) bool { return terms[i].Variable < terms[j].Variable })
		out = append(out, canonicalPrimeRow{
			Exponent: r.v, Terms: terms, Target: r.target.String(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Exponent != out[j].Exponent {
			return out[i].Exponent < out[j].Exponent
		}
		return canonicalPrimeRowKey(out[i]) < canonicalPrimeRowKey(out[j])
	})
	return out
}

type canonicalPrimeRow struct {
	Exponent int             `json:"exponent"`
	Terms    []canonicalTerm `json:"terms"`
	Target   string          `json:"target"`
}

func canonicalPrimeRowKey(r canonicalPrimeRow) string {
	s := fmt.Sprintf("%d|%s", r.Exponent, r.Target)
	for _, t := range r.Terms {
		s += "|" + t.Variable + ":" + t.Coefficient
	}
	return s
}
