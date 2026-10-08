package facilitycover

import "fmt"

// DeterministicResult is the outcome of SolveDeterministic.
type DeterministicResult struct {
	Opened  []int // sorted IDs of opened facilities (an ancestor-closed closure)
	Revenue int64 // total covered demand revenue, each demand counted once
	Cost    int64 // total opening cost, <= budget
}

// SolveDeterministic finds the ancestor-closed open set with cost <= Budget
// that maximises covered revenue, breaking ties by minimal cost (then by
// lexicographically smaller open set for determinism).
//
// It enumerates closures of the facility forest directly: facilities are
// visited parents-first, and a facility may only be taken when its parent was
// taken, so only valid closures are generated. Coverage is accumulated
// incrementally in a bitset, which counts every demand point at most once.
func SolveDeterministic(inst *Instance) (*DeterministicResult, error) {
	if len(inst.Facilities) > MaxDeterministicFacilities {
		return nil, fmt.Errorf("facilitycover: %d facilities exceeds limit %d",
			len(inst.Facilities), MaxDeterministicFacilities)
	}
	p, err := prepare(inst, inst.Coverage)
	if err != nil {
		return nil, err
	}

	open := make([]bool, p.n)
	best := &DeterministicResult{Opened: []int{}}
	found := false

	var rec func(pos int, cost int64, cur bitset)
	rec = func(pos int, cost int64, cur bitset) {
		if pos == p.n {
			rev := revenueOf(cur, inst.DemandRevenue)
			if !found || rev > best.Revenue ||
				(rev == best.Revenue && cost < best.Cost) ||
				(rev == best.Revenue && cost == best.Cost && lessIDs(p.openIDs(open), best.Opened)) {
				found = true
				best.Revenue = rev
				best.Cost = cost
				best.Opened = p.openIDs(open)
			}
			return
		}
		i := p.order[pos]
		// Branch 1: skip facility i (descendants will be blocked by the parent check).
		rec(pos+1, cost, cur)
		// Branch 2: take facility i, only if its parent is open and budget allows.
		if (p.parentIdx[i] == -1 || open[p.parentIdx[i]]) && cost+p.costs[i] <= inst.Budget {
			open[i] = true
			next := cur.clone()
			next.orInto(p.cov[i])
			rec(pos+1, cost+p.costs[i], next)
			open[i] = false
		}
	}
	rec(0, 0, newBitset(len(inst.DemandRevenue)))
	return best, nil
}

// VerifyDeterministic independently checks a DeterministicResult:
//  1. the returned set is an ancestor-closed closure within budget;
//  2. its revenue recomputed as a union (no double counting) matches;
//  3. an exhaustive enumeration of all 2^n subsets confirms optimality of
//     both the revenue and the tie-breaking cost.
func VerifyDeterministic(inst *Instance, res *DeterministicResult) error {
	if res == nil {
		return fmt.Errorf("facilitycover: nil result")
	}
	p, err := prepare(inst, inst.Coverage)
	if err != nil {
		return err
	}
	open := make([]bool, p.n)
	for _, id := range res.Opened {
		i, ok := indexOfID(p, id)
		if !ok {
			return fmt.Errorf("facilitycover: result opens unknown facility %d", id)
		}
		open[i] = true
	}
	if !p.isClosure(open) {
		return fmt.Errorf("facilitycover: result %v is not ancestor-closed", res.Opened)
	}
	if cost := p.costOf(open); cost != res.Cost || cost > inst.Budget {
		return fmt.Errorf("facilitycover: cost mismatch: reported %d, recomputed %d, budget %d",
			res.Cost, cost, inst.Budget)
	}
	// Recompute revenue demand-by-demand: a demand counts once iff at least
	// one open facility covers it. This is the no-double-counting check.
	var rev int64
	for d := range inst.DemandRevenue {
		for i := 0; i < p.n; i++ {
			if open[i] && p.cov[i].has(d) {
				rev += inst.DemandRevenue[d]
				break
			}
		}
	}
	if rev != res.Revenue {
		return fmt.Errorf("facilitycover: revenue mismatch: reported %d, recomputed %d",
			res.Revenue, rev)
	}

	// Exhaustive subset enumeration as ground truth for optimality.
	bestRev, bestCost := int64(-1), int64(0)
	n := p.n
	for mask := 0; mask < (1 << n); mask++ {
		sub := make([]bool, n)
		for i := 0; i < n; i++ {
			sub[i] = mask&(1<<i) != 0
		}
		if !p.isClosure(sub) {
			continue
		}
		cost := p.costOf(sub)
		if cost > inst.Budget {
			continue
		}
		var r int64
		for d := range inst.DemandRevenue {
			for i := 0; i < n; i++ {
				if sub[i] && p.cov[i].has(d) {
					r += inst.DemandRevenue[d]
					break
				}
			}
		}
		if r > bestRev || (r == bestRev && cost < bestCost) {
			bestRev, bestCost = r, cost
		}
	}
	if res.Revenue != bestRev || res.Cost != bestCost {
		return fmt.Errorf("facilitycover: not optimal: got (rev %d, cost %d), exhaustive optimum is (rev %d, cost %d)",
			res.Revenue, res.Cost, bestRev, bestCost)
	}
	return nil
}

func indexOfID(p *prepared, id int) (int, bool) {
	for i, f := range p.inst.Facilities {
		if f.ID == id {
			return i, true
		}
	}
	return 0, false
}

func lessIDs(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
