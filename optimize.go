package viewopt

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	maxFullAssignments = 2_000_000
	maxHalfEntries     = 1_500_000
)

// Optimize finds the minimum-read-cost linear view plan that is equivalent to
// the direct query for every base multiset in the configured domain.
//
// Search is symbolic: interval endpoints partition the filter line into cells,
// and it is enough to make the coefficient sum equal to 1 on every cell inside
// the query and 0 on every cell outside it.  No individual filter value in a
// range such as 2^40 is enumerated.
func Optimize(p Problem) (Solution, error) {
	opt := p.Options.normalized()
	if err := validateDims(p.Query.Dims); err != nil {
		return Solution{}, fmt.Errorf("query: %w", err)
	}
	if _, err := sortedViewIndices(p.Views); err != nil {
		return Solution{}, err
	}

	// Structurally invalid views can never be used for all possible data:
	// a coarser group cannot be split back into the query's groups.
	var usable []int
	for i, v := range p.Views {
		if dimsCover(v.Dims, p.Query.Dims) {
			usable = append(usable, i)
		}
	}

	input := buildCoverageInput(p.Query, p.Views, usable, opt)
	bestSafe, safeCost, hasSafe, bestAny, anyCost, hasAny, err := solveCoverage(p.Query, p.Views, input, opt)
	if err != nil {
		return Solution{}, err
	}
	if hasSafe {
		return Solution{
			Equivalent: true,
			Plan:       bestSafe,
			Cost:       safeCost,
		}, nil
	}

	candidate := Plan{}
	candidateCost := int64(0)
	if hasAny {
		candidate, candidateCost = bestAny, anyCost
	}
	ver, verr := VerifyPlan(p.Query, p.Views, candidate, opt)
	if verr != nil {
		return Solution{}, verr
	}
	reason := "no read-cost-bounded equivalent plan exists"
	if ver.Equivalent {
		// This should not happen: an any-coverage plan that VerifyPlan
		// accepts would also have been accepted as safe.  Keep the
		// result useful if a custom HAVING makes the search conservative.
		return Solution{
			Equivalent: true,
			Plan:       candidate,
			Cost:       candidateCost,
		}, nil
	}
	if ver.Reason != "" {
		reason += ": " + ver.Reason
	}
	return Solution{
		Equivalent:     false,
		Plan:           candidate,
		Cost:           candidateCost,
		Candidate:      candidate,
		CandidateCost:  candidateCost,
		Counterexample: ver.Counterexample,
		Reason:         reason,
	}, nil
}

type coach struct {
	cost  int64
	flag  bool
	coeff []int // coefficients for the half's views, in half order
}

func solveCoverage(q Query, views []View, in coverageInput, opt Options) (
	bestSafe Plan, safeCost int64, hasSafe bool,
	bestAny Plan, anyCost int64, hasAny bool,
	err error,
) {
	m := len(in.viewIdx)
	if len(in.cells) == 0 || allZero(in.want) {
		// Empty input: the empty plan is exact and costs nothing.
		return Plan{}, 0, true, Plan{}, 0, true, nil
	}
	if m == 0 {
		return Plan{}, 0, false, Plan{}, 0, false, nil
	}

	K := opt.MaxAbsCoeff
	if K > (math.MaxInt-1)/2 {
		return bestSafe, safeCost, hasSafe, bestAny, anyCost, hasAny,
			fmt.Errorf("MaxAbsCoeff %d is too large", K)
	}
	full := int64(1)
	overflow := false
	for i := 0; i < m; i++ {
		if full > math.MaxInt64/int64(2*K+1) {
			overflow = true
			break
		}
		full *= int64(2*K + 1)
	}
	if !overflow && full <= maxFullAssignments {
		return solveFull(q, views, in, opt)
	}
	return solveMeetInMiddle(q, views, in, opt)
}

func allZero(v []int8) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}

func solveFull(q Query, views []View, in coverageInput, opt Options) (
	bestSafe Plan, safeCost int64, hasSafe bool,
	bestAny Plan, anyCost int64, hasAny bool,
	err error,
) {
	m := len(in.viewIdx)
	K := opt.MaxAbsCoeff
	got := make([]int32, len(in.cells))
	coeff := make([]int, m)
	emptyVisible := q.Accept(EmptyAgg())

	var rec func(pos int, cost int64, flag bool)
	rec = func(pos int, cost int64, flag bool) {
		if pos == m {
			for i, v := range in.want {
				if got[i] != int32(v) {
					return
				}
			}
			plan, c := planFromCoeffs(views, in.viewIdx, coeff, opt.WeightCostByCoefficient)
			if !hasAny || betterCandidate(plan, c, bestAny, anyCost) {
				bestAny, anyCost, hasAny = plan, c, true
			}
			if !flag || !emptyVisible {
				if !hasSafe || betterCandidate(plan, c, bestSafe, safeCost) {
					bestSafe, safeCost, hasSafe = plan, c, true
				}
			}
			return
		}
		mask := in.masks[pos]
		for c := -K; c <= K; c++ {
			if c == 0 {
				coeff[pos] = 0
				rec(pos+1, cost, flag)
				continue
			}
			for j, on := range mask {
				if on {
					got[j] += int32(c)
				}
			}
			coeff[pos] = c
			rec(pos+1, cost+termCost(views[in.viewIdx[pos]], c, opt.WeightCostByCoefficient), flag || in.outside[pos])
			for j, on := range mask {
				if on {
					got[j] -= int32(c)
				}
			}
		}
	}
	rec(0, 0, false)
	return
}

func solveMeetInMiddle(q Query, views []View, in coverageInput, opt Options) (
	bestSafe Plan, safeCost int64, hasSafe bool,
	bestAny Plan, anyCost int64, hasAny bool,
	err error,
) {
	m := len(in.viewIdx)
	h := m / 2
	leftIdx := in.viewIdx[:h]
	rightIdx := in.viewIdx[h:]
	leftMask := in.masks[:h]
	rightMask := in.masks[h:]
	leftOutside := in.outside[:h]
	rightOutside := in.outside[h:]

	K := opt.MaxAbsCoeff
	leftCombos, ok := powInt(2*K+1, len(leftIdx))
	if !ok || leftCombos > maxHalfEntries {
		return bestSafe, safeCost, hasSafe, bestAny, anyCost, hasAny,
			fmt.Errorf("coefficient search too large (%d^%d); reduce MaxAbsCoeff", 2*K+1, len(leftIdx))
	}
	rightCombos, ok := powInt(2*K+1, len(rightIdx))
	if !ok || rightCombos > maxHalfEntries {
		return bestSafe, safeCost, hasSafe, bestAny, anyCost, hasAny,
			fmt.Errorf("coefficient search too large (%d^%d); reduce MaxAbsCoeff", 2*K+1, len(rightIdx))
	}

	left := make(map[string]coach, leftCombos)
	enumerateHalf(leftIdx, leftMask, leftOutside, views, K, opt.WeightCostByCoefficient, func(vec []int32, cost int64, flag bool, coeff []int) {
		key := vecKey(vec, flag)
		cur, ok := left[key]
		if !ok || betterHalf(cost, coeff, cur.cost, cur.coeff) {
			left[key] = coach{
				cost:  cost,
				flag:  flag,
				coeff: append([]int(nil), coeff...),
			}
		}
	})

	emptyVisible := q.Accept(EmptyAgg())
	enumerateHalf(rightIdx, rightMask, rightOutside, views, K, opt.WeightCostByCoefficient, func(vec []int32, cost int64, flag bool, coeff []int) {
		need := make([]int32, len(in.want))
		for i, w := range in.want {
			need[i] = int32(w) - vec[i]
		}
		key := vecKey(need, false)
		candidates := [2]string{key, keyWithFlag(need, true)}
		for _, k := range candidates {
			lc, ok := left[k]
			if !ok {
				continue
			}
			// The flag bit in the key must match; the second lookup is
			// only needed when the flag byte differs.
			if lc.flag != (k[len(k)-1] == 1) {
				continue
			}
			full := make([]int, 0, m)
			full = append(full, lc.coeff...)
			full = append(full, coeff...)
			plan, c := planFromCoeffs(views, in.viewIdx, full, opt.WeightCostByCoefficient)
			if !hasAny || betterCandidate(plan, c, bestAny, anyCost) {
				bestAny, anyCost, hasAny = plan, c, true
			}
			totalFlag := lc.flag || flag
			if !totalFlag || !emptyVisible {
				if !hasSafe || betterCandidate(plan, c, bestSafe, safeCost) {
					bestSafe, safeCost, hasSafe = plan, c, true
				}
			}
		}
	})
	return
}

func enumerateHalf(idx []int, masks [][]bool, outside []bool, views []View, K int, weightByCoeff bool,
	visit func(vec []int32, cost int64, flag bool, coeff []int)) {
	vec := make([]int32, len(masks[0]))
	coeff := make([]int, len(idx))
	var rec func(pos int, cost int64, flag bool)
	rec = func(pos int, cost int64, flag bool) {
		if pos == len(idx) {
			visit(vec, cost, flag, coeff)
			return
		}
		mask := masks[pos]
		for c := -K; c <= K; c++ {
			if c == 0 {
				coeff[pos] = 0
				rec(pos+1, cost, flag)
				continue
			}
			for j, on := range mask {
				if on {
					vec[j] += int32(c)
				}
			}
			coeff[pos] = c
			rec(pos+1, cost+termCost(views[idx[pos]], c, weightByCoeff), flag || outside[pos])
			for j, on := range mask {
				if on {
					vec[j] -= int32(c)
				}
			}
		}
	}
	rec(0, 0, false)
}

func vecKey(vec []int32, flag bool) string {
	b := make([]byte, 4*len(vec)+1)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(v))
	}
	if flag {
		b[len(b)-1] = 1
	}
	return string(b)
}

func keyWithFlag(vec []int32, flag bool) string { return vecKey(vec, flag) }

func powInt(base, exp int) (int, bool) {
	if base <= 0 {
		return 0, false
	}
	v := 1
	for i := 0; i < exp; i++ {
		if v > math.MaxInt/base {
			return 0, false
		}
		v *= base
	}
	return v, true
}

func planFromCoeffs(views []View, viewIdx []int, coeff []int, weightByCoeff bool) (Plan, int64) {
	p := Plan{}
	var cost int64
	for i, c := range coeff {
		if c == 0 {
			continue
		}
		p.Terms = append(p.Terms, PlanTerm{View: viewIdx[i], Coeff: c})
		add := termCost(views[viewIdx[i]], c, weightByCoeff)
		if cost > math.MaxInt64-add {
			cost = math.MaxInt64
		} else {
			cost += add
		}
	}
	return p.Normalize(), cost
}

func termCost(v View, coeff int, weightByCoeff bool) int64 {
	units := int64(1)
	if weightByCoeff {
		ac := coeff
		if ac < 0 {
			ac = -ac
		}
		units = int64(ac)
	}
	return units * v.ReadCost
}

func betterHalf(cost int64, coeff []int, bestCost int64, bestCoeff []int) bool {
	if cost != bestCost {
		return cost < bestCost
	}
	at := countNonZero(coeff)
	bt := countNonZero(bestCoeff)
	if at != bt {
		return at < bt
	}
	return intsLess(coeff, bestCoeff)
}

func countNonZero(v []int) int {
	n := 0
	for _, x := range v {
		if x != 0 {
			n++
		}
	}
	return n
}

func intsLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func betterCandidate(aPlan Plan, aCost int64, bPlan Plan, bCost int64) bool {
	if aCost != bCost {
		return aCost < bCost
	}
	at := len(aPlan.Normalize().Terms)
	bt := len(bPlan.Normalize().Terms)
	if at != bt {
		return at < bt
	}
	a := aPlan.Normalize()
	b := bPlan.Normalize()
	return planTermsLess(a.Terms, b.Terms)
}

func planTermsLess(a, b []PlanTerm) bool {
	maxView := 0
	for _, t := range a {
		if t.View > maxView {
			maxView = t.View
		}
	}
	for _, t := range b {
		if t.View > maxView {
			maxView = t.View
		}
	}
	ai, bi := 0, 0
	for v := 0; v <= maxView; v++ {
		ac, bc := 0, 0
		if ai < len(a) && a[ai].View == v {
			ac = a[ai].Coeff
			ai++
		}
		if bi < len(b) && b[bi].View == v {
			bc = b[bi].Coeff
			bi++
		}
		if ac != bc {
			return ac < bc
		}
	}
	return false
}
