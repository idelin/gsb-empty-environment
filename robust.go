package facilitycover

import (
	"fmt"
	"sort"
	"strings"
)

// RobustResult is the outcome of SolveRobust.
type RobustResult struct {
	Opened    []int         // stage-1 openings (ancestor-closed), fixed before observation
	Emergency map[int][]int // observation group -> emergency facility IDs (same within a group)

	WorstRevenue  int64   // minimum total covered revenue over all scenarios
	ExpectedCost  float64 // stage-1 cost + probability-weighted emergency cost
	ScenarioRev   []int64 // covered revenue per scenario (each demand counted once)
	ScenarioCost  []int64 // stage-1 + emergency cost per scenario (<= Budget)
	ScenarioGroup []int   // scenario index -> observation group
}

// SolveRobust solves the two-stage problem:
//
//  1. Stage 1: choose an ancestor-closed open set (fixed across scenarios).
//  2. Stage 2: after the scenario is observed, enable up to EmergencyLimit
//     emergency facilities, but only those whose ancestors are all open in
//     stage 1. Scenarios in the same observation group must use the same
//     emergency action.
//
// The objective maximises the worst-case covered revenue over scenarios;
// ties are broken by minimal expected cost (stage-1 cost plus the
// probability-weighted emergency cost). Every scenario must satisfy
// stage1Cost + emergencyCost <= Budget.
func SolveRobust(inst *RobustInstance) (*RobustResult, error) {
	if len(inst.Facilities) > MaxRobustFacilities {
		return nil, fmt.Errorf("facilitycover: %d facilities exceeds limit %d",
			len(inst.Facilities), MaxRobustFacilities)
	}
	if len(inst.Scenarios) == 0 {
		return nil, fmt.Errorf("facilitycover: no scenarios")
	}
	if len(inst.Scenarios) > MaxRobustScenarios {
		return nil, fmt.Errorf("facilitycover: %d scenarios exceeds limit %d",
			len(inst.Scenarios), MaxRobustScenarios)
	}
	if inst.EmergencyLimit < 0 {
		return nil, fmt.Errorf("facilitycover: negative emergency limit")
	}
	p, err := prepare(&inst.Instance, inst.Instance.Coverage)
	if err != nil {
		return nil, err
	}

	ns := len(inst.Scenarios)
	// Per-scenario coverage bitsets.
	scenCov := make([][]bitset, ns)
	groups := make([]int, ns)
	probs := make([]float64, ns)
	groupSet := map[int]bool{}
	for s, sc := range inst.Scenarios {
		if sc.Probability < 0 {
			return nil, fmt.Errorf("facilitycover: scenario %d has negative probability", s)
		}
		groups[s] = sc.Group
		probs[s] = sc.Probability
		groupSet[sc.Group] = true
		scenCov[s] = make([]bitset, p.n)
		for i, f := range inst.Facilities {
			scenCov[s][i] = newBitset(len(inst.DemandRevenue))
			for _, d := range sc.Coverage[f.ID] {
				if d < 0 || d >= len(inst.DemandRevenue) {
					return nil, fmt.Errorf("facilitycover: scenario %d facility %d covers unknown demand %d", s, f.ID, d)
				}
				scenCov[s][i].set(d)
			}
		}
	}
	groupList := make([]int, 0, len(groupSet))
	for g := range groupSet {
		groupList = append(groupList, g)
	}
	sort.Ints(groupList)
	groupProb := make(map[int]float64, len(groupList))
	for s, g := range groups {
		groupProb[g] += probs[s]
	}

	// Enumerate stage-1 closures (parents-first order, parent check as in the
	// deterministic solver), pruning on the stage-1 cost alone.
	open := make([]bool, p.n)
	var best *RobustResult
	better := func(cand *RobustResult) bool {
		if best == nil {
			return true
		}
		if cand.WorstRevenue != best.WorstRevenue {
			return cand.WorstRevenue > best.WorstRevenue
		}
		return cand.ExpectedCost < best.ExpectedCost
	}

	var rec func(pos int, cost int64)
	rec = func(pos int, cost int64) {
		if pos == p.n {
			if cand := solveSecondStage(p, inst, open, cost, scenCov, groups, probs, groupList, groupProb); cand != nil && better(cand) {
				best = cand
			}
			return
		}
		i := p.order[pos]
		rec(pos+1, cost)
		if (p.parentIdx[i] == -1 || open[p.parentIdx[i]]) && cost+p.costs[i] <= inst.Budget {
			open[i] = true
			rec(pos+1, cost+p.costs[i])
			open[i] = false
		}
	}
	rec(0, 0)
	if best == nil {
		return nil, fmt.Errorf("facilitycover: infeasible: no stage-1 closure admits a budget-feasible emergency plan")
	}
	return best, nil
}

// solveSecondStage evaluates one fixed stage-1 open set: for every observation
// group it picks the emergency action maximising the group's worst-scenario
// revenue (tie: minimal emergency cost), subject to the per-scenario budget.
// Because the overall worst case is the minimum of the group minima and the
// expected cost is separable across groups, per-group optimisation is exact.
func solveSecondStage(p *prepared, inst *RobustInstance, open []bool, stageCost int64,
	scenCov [][]bitset, groups []int, probs []float64, groupList []int, groupProb map[int]float64) *RobustResult {

	// Emergency candidates: not open, but all ancestors open. Because the
	// parent of a candidate is open, any subset of candidates is itself a
	// valid emergency extension.
	var candIdx []int
	for i := 0; i < p.n; i++ {
		if !open[i] && (p.parentIdx[i] == -1 || open[p.parentIdx[i]]) {
			candIdx = append(candIdx, i)
		}
	}
	limit := inst.EmergencyLimit
	if limit > len(candIdx) {
		limit = len(candIdx)
	}

	// Base (stage-1 only) coverage per scenario.
	ns := len(inst.Scenarios)
	base := make([]bitset, ns)
	for s := 0; s < ns; s++ {
		base[s] = p.unionCoverage(scenCov[s], open)
	}

	res := &RobustResult{
		Opened:        p.openIDs(open),
		Emergency:     map[int][]int{},
		ScenarioRev:   make([]int64, ns),
		ScenarioCost:  make([]int64, ns),
		ScenarioGroup: append([]int(nil), groups...),
	}
	res.WorstRevenue = -1

	for _, g := range groupList {
		var scenInGroup []int
		for s := 0; s < ns; s++ {
			if groups[s] == g {
				scenInGroup = append(scenInGroup, s)
			}
		}
		// Enumerate emergency subsets of candidates with size <= limit.
		bestGroupWorst := int64(-1)
		var bestGroupCost int64
		var bestSubset []int
		var bestRev []int64
		found := false
		chosen := make([]int, 0, limit)
		var enum func(start int, eCost int64)
		enum = func(start int, eCost int64) {
			if stageCost+eCost > inst.Budget {
				return // per-scenario budget (same action => same cost within group)
			}
			// Evaluate the current subset.
			worst := int64(-1)
			revs := make([]int64, len(scenInGroup))
			for k, s := range scenInGroup {
				u := base[s].clone()
				for _, ci := range chosen {
					u.orInto(scenCov[s][candIdx[ci]])
				}
				revs[k] = revenueOf(u, inst.DemandRevenue)
				if worst == -1 || revs[k] < worst {
					worst = revs[k]
				}
			}
			if !found || worst > bestGroupWorst || (worst == bestGroupWorst && eCost < bestGroupCost) {
				found = true
				bestGroupWorst = worst
				bestGroupCost = eCost
				bestSubset = append([]int(nil), chosen...)
				bestRev = revs
			}
			if len(chosen) == limit {
				return
			}
			for j := start; j < len(candIdx); j++ {
				chosen = append(chosen, j)
				enum(j+1, eCost+p.costs[candIdx[j]])
				chosen = chosen[:len(chosen)-1]
			}
		}
		enum(0, 0)
		if !found {
			return nil // no budget-feasible emergency action for this group
		}

		emIDs := make([]int, 0, len(bestSubset))
		for _, ci := range bestSubset {
			emIDs = append(emIDs, p.inst.Facilities[candIdx[ci]].ID)
		}
		sort.Ints(emIDs)
		res.Emergency[g] = emIDs
		for k, s := range scenInGroup {
			res.ScenarioRev[s] = bestRev[k]
			res.ScenarioCost[s] = stageCost + bestGroupCost
		}
		res.ExpectedCost += groupProb[g] * float64(bestGroupCost)
		if res.WorstRevenue == -1 || bestGroupWorst < res.WorstRevenue {
			res.WorstRevenue = bestGroupWorst
		}
	}
	res.ExpectedCost += float64(stageCost)
	return res
}

// PolicyTree renders the first-stage decision and the per-group emergency
// actions as an indented tree.
func (r *RobustResult) PolicyTree() string {
	var b strings.Builder
	fmt.Fprintf(&b, "stage1 open %v (worst-case revenue %d, expected cost %.4g)\n",
		r.Opened, r.WorstRevenue, r.ExpectedCost)
	groups := make([]int, 0, len(r.Emergency))
	for g := range r.Emergency {
		groups = append(groups, g)
	}
	sort.Ints(groups)
	for i, g := range groups {
		branch := "├─"
		if i == len(groups)-1 {
			branch = "└─"
		}
		var scen []int
		for s, sg := range r.ScenarioGroup {
			if sg == g {
				scen = append(scen, s)
			}
		}
		fmt.Fprintf(&b, "%s group %d (scenarios %v): emergency %v\n", branch, g, scen, r.Emergency[g])
	}
	return b.String()
}

// VerifyRobust independently checks a RobustResult:
//  1. the stage-1 set is an ancestor-closed closure;
//  2. every emergency facility has all ancestors open in stage 1, and each
//     group uses at most EmergencyLimit of them;
//  3. scenarios in the same group use identical emergency actions;
//  4. per-scenario budget stage1Cost + emergencyCost <= Budget holds;
//  5. per-scenario revenue recomputed as a union matches, and the reported
//     worst-case revenue and expected cost are consistent.
func VerifyRobust(inst *RobustInstance, res *RobustResult) error {
	if res == nil {
		return fmt.Errorf("facilitycover: nil result")
	}
	p, err := prepare(&inst.Instance, inst.Instance.Coverage)
	if err != nil {
		return err
	}
	open := make([]bool, p.n)
	for _, id := range res.Opened {
		i, ok := indexOfID(p, id)
		if !ok {
			return fmt.Errorf("facilitycover: stage-1 opens unknown facility %d", id)
		}
		open[i] = true
	}
	if !p.isClosure(open) {
		return fmt.Errorf("facilitycover: stage-1 set %v is not ancestor-closed", res.Opened)
	}
	stageCost := p.costOf(open)

	emByGroup := map[int][]bool{}
	for g, ids := range res.Emergency {
		em := make([]bool, p.n)
		if len(ids) > inst.EmergencyLimit {
			return fmt.Errorf("facilitycover: group %d enables %d emergency facilities, limit is %d",
				g, len(ids), inst.EmergencyLimit)
		}
		for _, id := range ids {
			i, ok := indexOfID(p, id)
			if !ok {
				return fmt.Errorf("facilitycover: group %d enables unknown facility %d", g, id)
			}
			if open[i] {
				return fmt.Errorf("facilitycover: group %d re-enables stage-1 facility %d", g, id)
			}
			// Ancestors must be open in stage 1.
			for j := i; p.parentIdx[j] != -1; j = p.parentIdx[j] {
				if !open[p.parentIdx[j]] {
					return fmt.Errorf("facilitycover: group %d enables %d without open ancestor %d",
						g, id, p.inst.Facilities[p.parentIdx[j]].ID)
				}
			}
			em[i] = true
		}
		emByGroup[g] = em
	}

	ns := len(inst.Scenarios)
	if len(res.ScenarioRev) != ns || len(res.ScenarioCost) != ns {
		return fmt.Errorf("facilitycover: per-scenario slices have wrong length")
	}
	worst := int64(-1)
	expected := float64(stageCost)
	for s, sc := range inst.Scenarios {
		em, ok := emByGroup[sc.Group]
		if !ok {
			return fmt.Errorf("facilitycover: no emergency action for scenario %d group %d", s, sc.Group)
		}
		// Same group => same action: compare with the first scenario seen in the group.
		for t := 0; t < s; t++ {
			if inst.Scenarios[t].Group == sc.Group {
				other := emByGroup[inst.Scenarios[t].Group]
				for i := range em {
					if em[i] != other[i] {
						return fmt.Errorf("facilitycover: scenarios %d and %d share group %d but differ in emergency action", t, s, sc.Group)
					}
				}
			}
		}
		// Per-scenario budget check.
		cost := stageCost + p.costOf(em)
		if cost > inst.Budget {
			return fmt.Errorf("facilitycover: scenario %d exceeds budget: cost %d > budget %d", s, cost, inst.Budget)
		}
		if cost != res.ScenarioCost[s] {
			return fmt.Errorf("facilitycover: scenario %d cost mismatch: reported %d, recomputed %d", s, res.ScenarioCost[s], cost)
		}
		// Revenue recomputed demand-by-demand (no double counting).
		scenPrep, err := prepare(&inst.Instance, sc.Coverage)
		if err != nil {
			return err
		}
		var rev int64
		for d := range inst.DemandRevenue {
			for i := 0; i < p.n; i++ {
				if (open[i] || em[i]) && scenPrep.cov[i].has(d) {
					rev += inst.DemandRevenue[d]
					break
				}
			}
		}
		if rev != res.ScenarioRev[s] {
			return fmt.Errorf("facilitycover: scenario %d revenue mismatch: reported %d, recomputed %d", s, res.ScenarioRev[s], rev)
		}
		if worst == -1 || rev < worst {
			worst = rev
		}
		var emCost int64
		for i := 0; i < p.n; i++ {
			if em[i] {
				emCost += p.costs[i]
			}
		}
		expected += sc.Probability * float64(emCost)
	}
	if worst != res.WorstRevenue {
		return fmt.Errorf("facilitycover: worst revenue mismatch: reported %d, recomputed %d", res.WorstRevenue, worst)
	}
	if diff := expected - res.ExpectedCost; diff > 1e-9 || diff < -1e-9 {
		return fmt.Errorf("facilitycover: expected cost mismatch: reported %g, recomputed %g", res.ExpectedCost, expected)
	}
	return nil
}
