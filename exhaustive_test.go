package netsec

import (
	"math/rand"
	"testing"
)

// bruteForceResult is the ground truth computed by enumerating every one of
// the nd^nr possible assignments.
type bruteForceResult struct {
	feasible    bool
	minDomains  int
	minCost     int
	minimalSets [][]int
}

func bruteForce(in Input) bruteForceResult {
	nr, nd := len(in.Requests), len(in.Domains)
	s := newSolver(in)

	type cand struct {
		assign     []int
		domainMask int
		domains    int
		cost       int
	}
	var optimal *cand
	assignment := make([]int, nr)

	used := make([][]int, nd)
	for d := range used {
		used[d] = append([]int(nil), s.capacities[d]...)
	}

	var enumerate func(pos int)
	enumerate = func(pos int) {
		if pos == nr {
			mask := 0
			for _, d := range assignment {
				mask |= 1 << d
			}
			count := popcount(mask)
			cost := 0
			for i, d := range assignment {
				cost += in.Cost[i][d]
			}
			if optimal == nil || count < optimal.domains ||
				(count == optimal.domains && cost < optimal.cost) {
				optimal = &cand{
					assign:     append([]int(nil), assignment...),
					domainMask: mask,
					domains:    count,
					cost:       cost,
				}
			}
			return
		}

		req := pos
		need := in.Requests[req].Capacity
		pIdx := s.purposeIndex[in.Requests[req].KeyPurpose]
		for d := 0; d < nd; d++ {
			if s.feasibleMask[req]&(1<<d) == 0 || used[d][pIdx] < need {
				continue
			}
			if !bruteClear(s, used, assignment, req, d) {
				continue
			}
			assignment[req] = d
			used[d][pIdx] -= need
			enumerate(pos + 1)
			used[d][pIdx] += need
			assignment[req] = -1
		}
	}
	enumerate(0)

	res := bruteForceResult{feasible: optimal != nil}
	if optimal != nil {
		res.minDomains = optimal.domains
		res.minCost = optimal.cost
		return res
	}
	res.minimalSets = bruteForceMinimalSubsets(in, s)
	return res
}

func bruteClear(s *solver, used [][]int, assignment []int, req, d int) bool {
	for i := 0; i < req; i++ {
		if assignment[i] == d && s.conflict[req]&(1<<uint(i)) != 0 {
			return false
		}
	}
	return true
}

// bruteForceMinimalSubsets enumerates all request subsets and keeps the
// infeasible ones whose every strict subset is feasible.
func bruteForceMinimalSubsets(in Input, s *solver) [][]int {
	nr := len(in.Requests)
	feasibleCache := make(map[uint32]bool)

	feasible := func(mask uint32) bool {
		if v, ok := feasibleCache[mask]; ok {
			return v
		}
		indices := make([]int, 0, nr)
		for i := 0; i < nr; i++ {
			if mask&(1<<uint(i)) != 0 {
				indices = append(indices, i)
			}
		}
		ok := s.subsetFeasible(indices)
		feasibleCache[mask] = ok
		return ok
	}

	var out [][]int
	for mask := uint32(1); mask < 1<<uint(nr); mask++ {
		if feasible(mask) {
			continue
		}
		minimal := true
		bits := mask
		for bits != 0 {
			low := bits & -bits
			if feasible(mask ^ low) {
				bits ^= low
				continue
			}
			minimal = false
			break
		}
		if minimal {
			indices := make([]int, 0, nr)
			for i := 0; i < nr; i++ {
				if mask&(1<<uint(i)) != 0 {
					indices = append(indices, i)
				}
			}
			out = append(out, indices)
		}
	}
	return out
}

func popcount(mask int) int {
	count := 0
	for mask != 0 {
		mask &= mask - 1
		count++
	}
	return count
}

// TestExhaustiveCrossCheck generates small random instances (the nd^nr
// enumeration stays manageable) and compares Solve against brute force,
// including shared-key capacity and approval-role conflicts.
func TestExhaustiveCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	const iterations = 400

	for iter := 0; iter < iterations; iter++ {
		in := randomInput(rng)
		truth := bruteForce(in)
		plan, cs, err := Solve(in)

		if truth.feasible {
			if err != nil {
				t.Fatalf("iter %d: brute force feasible but Solve failed: %v", iter, err)
			}
			if plan.EnabledCount != truth.minDomains {
				t.Fatalf("iter %d: domain count mismatch solve=%d brute=%d",
					iter, plan.EnabledCount, truth.minDomains)
			}
			if plan.TotalCost != truth.minCost {
				t.Fatalf("iter %d: cost mismatch solve=%d brute=%d",
					iter, plan.TotalCost, truth.minCost)
			}
			if !planSatisfiesConstraints(t, in, plan) {
				t.Fatalf("iter %d: plan violates constraints: %+v", iter, plan)
			}
			continue
		}

		if !errorIs(err, ErrInfeasible) || cs == nil {
			t.Fatalf("iter %d: expected infeasible, err=%v cs=%+v", iter, err, cs)
		}
		if !isOneOfMinimalSubsets(in, cs.RequestSubset, truth.minimalSets) {
			t.Fatalf("iter %d: conflict subset %v is not inclusion-minimal (brute sets=%v)",
				iter, cs.RequestSubset, truth.minimalSets)
		}
	}
}

func randomInput(rng *rand.Rand) Input {
	nr := 1 + rng.Intn(6)
	nd := 1 + rng.Intn(3)
	purposes := []string{"k0", "k1", "k2"}
	tenants := []string{"t0", "t1", "t2"}
	roles := []string{"initiator", "approver"}

	requests := make([]Request, nr)
	for i := range requests {
		requests[i] = Request{
			ID:         "r" + itoa(i),
			TenantID:   tenants[rng.Intn(len(tenants))],
			KeyPurpose: purposes[rng.Intn(len(purposes))],
			Capacity:   1 + rng.Intn(3),
			Sensitive:  rng.Intn(2) == 0,
		}
		if requests[i].Sensitive {
			requests[i].ApprovalRole = roles[rng.Intn(len(roles))]
		}
	}

	domains := make([]Domain, nd)
	for d := range domains {
		pools := make(map[string]int)
		for _, p := range purposes {
			// Occasionally omit a purpose to create unsupported cases.
			if rng.Intn(5) == 0 {
				continue
			}
			pools[p] = rng.Intn(5)
		}
		domains[d] = Domain{ID: "d" + itoa(d), Purposes: pools}
	}

	cost := make([][]int, nr)
	for i := range cost {
		row := make([]int, nd)
		for d := range row {
			row[d] = rng.Intn(10)
		}
		cost[i] = row
	}

	in := Input{Requests: requests, Domains: domains, Cost: cost}
	// Sprinkle up to two explicit incompatibility edges.
	for edge := 0; edge < 2 && nr >= 2; edge++ {
		if rng.Intn(2) == 0 {
			a := rng.Intn(nr)
			b := rng.Intn(nr)
			if a != b {
				in.MustSeparate = append(in.MustSeparate,
					Pair{A: requests[a].ID, B: requests[b].ID}.canonical())
			}
		}
	}
	return in
}

func planSatisfiesConstraints(t *testing.T, in Input, plan *Plan) bool {
	t.Helper()
	s := newSolver(in)
	used := make([][]int, len(in.Domains))
	for d := range used {
		used[d] = append([]int(nil), s.capacities[d]...)
	}

	domains := make(map[int]bool)
	for i, d := range plan.Assignment {
		if d < 0 || d >= len(in.Domains) {
			return false
		}
		domains[d] = true
		pIdx := s.purposeIndex[in.Requests[i].KeyPurpose]
		used[d][pIdx] -= in.Requests[i].Capacity
		if used[d][pIdx] < 0 {
			return false
		}
		for j := 0; j < i; j++ {
			if plan.Assignment[j] == d && s.conflict[i]&(1<<uint(j)) != 0 {
				return false
			}
		}
	}
	return len(domains) == plan.EnabledCount
}

func isOneOfMinimalSubsets(in Input, ids []string, sets [][]int) bool {
	idIndex := make(map[string]int, len(in.Requests))
	for i := range in.Requests {
		idIndex[in.Requests[i].ID] = i
	}
	want := uint32(0)
	for _, id := range ids {
		want |= 1 << uint(idIndex[id])
	}
	for _, set := range sets {
		got := uint32(0)
		for _, i := range set {
			got |= 1 << uint(i)
		}
		if got == want {
			return true
		}
	}
	return false
}

func errorIs(got, target error) bool {
	return got != nil && got.Error() == target.Error()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
