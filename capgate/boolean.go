package capgate

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type universe struct {
	features    []string
	legalSource string
	legalExpr   Expr
	n           int
	count       int
	assignments []Assignment
	legal       []Assignment
	legalIndex  map[int]Assignment
	legalSet    *big.Int
	allSet      *big.Int
	litMasks    [][2]*big.Int
}

func buildUniverse(graphs []*Graph, policy Policy) (*universe, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var features []string
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		features = append(features, name)
	}
	for _, name := range policy.Features {
		add(name)
	}

	legalExpr, err := compileCondition(policy.LegalCondition)
	if err != nil {
		return nil, err
	}
	for _, name := range legalExpr.variables() {
		add(name)
	}

	var conditionErrors []string
	for _, g := range graphs {
		if g == nil {
			continue
		}
		for _, e := range g.Edges {
			if e == nil {
				continue
			}
			expr, err := compileCondition(e.EffectiveCondition())
			if err != nil {
				conditionErrors = append(conditionErrors, fmt.Sprintf("graph %q edge %q: %v", g.Name, e.ID, err))
				continue
			}
			for _, name := range expr.variables() {
				add(name)
			}
		}
	}
	if len(conditionErrors) > 0 {
		return nil, fmt.Errorf("capgate: invalid conditions: %s", strings.Join(conditionErrors, "; "))
	}

	sort.Strings(features)
	maxFeatures := policy.MaxFeatures
	if maxFeatures == 0 {
		maxFeatures = 12
	}
	if len(features) > maxFeatures {
		return nil, fmt.Errorf("capgate: %d feature variables exceed exhaustive limit %d; reduce the small platform universe or raise Policy.MaxFeatures (hard limit 12)", len(features), maxFeatures)
	}

	n := len(features)
	count := 1 << n
	u := &universe{
		features:    features,
		legalSource: strings.TrimSpace(policy.LegalCondition),
		legalExpr:   legalExpr,
		n:           n,
		count:       count,
		legalIndex:  map[int]Assignment{},
	}

	u.allSet = new(big.Int).Lsh(big.NewInt(1), uint(count))
	u.allSet.Sub(u.allSet, big.NewInt(1))
	u.litMasks = make([][2]*big.Int, n)
	for i := 0; i < n; i++ {
		bit := 1 << i
		m0 := new(big.Int)
		m1 := new(big.Int)
		for idx := 0; idx < count; idx++ {
			if idx&bit == 0 {
				m0.SetBit(m0, idx, 1)
			} else {
				m1.SetBit(m1, idx, 1)
			}
		}
		u.litMasks[i] = [2]*big.Int{m0, m1}
	}

	u.legalSet = new(big.Int)
	for idx := 0; idx < count; idx++ {
		a := u.assignmentForIndex(idx)
		u.assignments = append(u.assignments, a)
		if legalExpr.Eval(a) {
			u.legal = append(u.legal, a)
			u.legalIndex[idx] = a
			u.legalSet.SetBit(u.legalSet, idx, 1)
		}
	}
	if len(u.legal) == 0 {
		return nil, fmt.Errorf("capgate: LegalCondition admits no feature combination")
	}
	return u, nil
}

func (u *universe) index(a Assignment) int {
	idx := 0
	for i, name := range u.features {
		if a.value(name) {
			idx |= 1 << i
		}
	}
	return idx
}

func (u *universe) assignmentForIndex(idx int) Assignment {
	a := make(Assignment, u.n)
	for i, name := range u.features {
		a[name] = idx&(1<<i) != 0
	}
	return a
}

func (u *universe) cubeSet(fixedMask, valueMask int) *big.Int {
	set := new(big.Int).Set(u.allSet)
	for i := 0; i < u.n; i++ {
		bit := 1 << i
		if fixedMask&bit == 0 {
			continue
		}
		value := 0
		if valueMask&bit != 0 {
			value = 1
		}
		set.And(set, u.litMasks[i][value])
	}
	return set
}

func (u *universe) cubeFromCode(code string) Cube {
	var literals []Literal
	for i, ch := range code {
		if ch == '*' {
			continue
		}
		literals = append(literals, Literal{Feature: u.features[i], Value: ch == '1'})
	}
	return Cube{Literals: literals}
}

// primeCubes exhaustively enumerates exact prime implicants of onset. Illegal
// assignments are don't-cares. onset is expected to be a subset of legalSet.
func (u *universe) primeCubes(onset *big.Int) []Cube {
	type cubeInfo struct {
		code string
		cube Cube
	}
	implicants := map[string]cubeInfo{}
	code := make([]byte, u.n)

	var visit func(i int, set *big.Int)
	visit = func(i int, set *big.Int) {
		if i == u.n {
			legalCube := new(big.Int).And(set, u.legalSet)
			if legalCube.BitLen() == 0 {
				return
			}
			bad := new(big.Int).AndNot(legalCube, onset)
			if bad.BitLen() != 0 {
				return
			}
			key := string(code)
			implicants[key] = cubeInfo{code: key, cube: u.cubeFromCode(key)}
			return
		}

		code[i] = '0'
		visit(i+1, new(big.Int).And(set, u.litMasks[i][0]))
		code[i] = '1'
		visit(i+1, new(big.Int).And(set, u.litMasks[i][1]))
		code[i] = '*'
		visit(i+1, set)
	}
	visit(0, new(big.Int).Set(u.allSet))

	var primes []Cube
	for code := range implicants {
		isPrime := true
		codeBytes := []byte(code)
		for i, ch := range codeBytes {
			if ch == '*' {
				continue
			}
			old := codeBytes[i]
			codeBytes[i] = '*'
			if _, ok := implicants[string(codeBytes)]; ok {
				isPrime = false
				codeBytes[i] = old
				break
			}
			codeBytes[i] = old
		}
		if isPrime {
			primes = append(primes, implicants[code].cube)
		}
	}

	sort.Slice(primes, func(i, j int) bool {
		if len(primes[i].Literals) != len(primes[j].Literals) {
			return len(primes[i].Literals) < len(primes[j].Literals)
		}
		return primes[i].Expr() < primes[j].Expr()
	})
	return primes
}

type disjointCube struct {
	cube      Cube
	primeCube Cube
	seed      int
	indices   []int
}

// disjointCubes returns syntactically disjoint inclusion-minimal residual cubes.
// Already emitted raw cubes are added to the forbidden care set, so later cubes
// cannot overlap them even on illegal assignments.
func (u *universe) disjointCubes(onset *big.Int, primes []Cube) []disjointCube {
	forbidden := new(big.Int).AndNot(u.legalSet, onset)
	covered := new(big.Int)
	var out []disjointCube

	for seed := 0; seed < u.count; seed++ {
		if onset.Bit(seed) == 0 || covered.Bit(seed) != 0 {
			continue
		}

		freeMask := 0
		for i := 0; i < u.n; i++ {
			bit := 1 << i
			if freeMask&bit != 0 {
				continue
			}
			candidateFree := freeMask | bit
			fixedMask := (1<<u.n - 1) ^ candidateFree
			candidate := u.cubeSet(fixedMask, seed&fixedMask)
			hit := new(big.Int).And(candidate, forbidden)
			if hit.BitLen() == 0 {
				freeMask = candidateFree
			}
		}

		fixedMask := (1<<u.n - 1) ^ freeMask
		valueMask := seed & fixedMask
		set := u.cubeSet(fixedMask, valueMask)
		cube := cubeFromMask(u.features, fixedMask, valueMask)

		legalInCube := new(big.Int).And(set, u.legalSet)
		covered.Or(covered, legalInCube)
		forbidden.Or(forbidden, set)

		var indices []int
		for idx := 0; idx < u.count; idx++ {
			if legalInCube.Bit(idx) != 0 {
				indices = append(indices, idx)
			}
		}

		out = append(out, disjointCube{
			cube:      cube,
			primeCube: containingPrime(cube, primes),
			seed:      seed,
			indices:   indices,
		})
	}
	return out
}

func containingPrime(block Cube, primes []Cube) Cube {
	// Prefer the shortest prime that contains the block's legal cube. A block
	// fixes a superset of the prime's literals when it is a guarded restriction.
	var best Cube
	found := false
	for _, p := range primes {
		if cubeExtends(p, block) {
			if !found || len(p.Literals) < len(best.Literals) ||
				(len(p.Literals) == len(best.Literals) && p.Expr() < best.Expr()) {
				best = p
				found = true
			}
		}
	}
	if !found {
		return Cube{}
	}
	return best
}

// cubeExtends reports whether block fixes every literal of prime.
func cubeExtends(prime Cube, block Cube) bool {
	blockValues := map[string]bool{}
	for _, lit := range block.Literals {
		blockValues[lit.Feature] = lit.Value
	}
	for _, lit := range prime.Literals {
		v, ok := blockValues[lit.Feature]
		if !ok || v != lit.Value {
			return false
		}
	}
	return true
}

func bitsetIndices(set *big.Int) []int {
	var out []int
	for i := 0; i < set.BitLen(); i++ {
		if set.Bit(i) != 0 {
			out = append(out, i)
		}
	}
	return out
}
