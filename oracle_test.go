package netsec

import (
	"math/rand"
	"testing"
)

// bruteForce enumerates every assignment of bucket times inside the event
// windows and returns the lexicographic optimum (fewest distinct buckets,
// then smallest total delay). The small time domains in tests keep this
// tractable and make it an independent check of the subset DP.
func bruteForce(p *Problem) (distinctBuckets, totalDelay int, feasible bool) {
	n := len(p.Times)
	choice := make([]int, n)
	lo := make([]int, n)
	hi := make([]int, n)
	domainSize := 1
	for i := range p.Times {
		lo[i] = p.Times[i]
		hi[i] = p.Times[i] + p.MaxDelay[i]
		domainSize *= hi[i] - lo[i] + 1
		if domainSize > 4_000_000 {
			panic("bruteForce domain too large")
		}
	}

	bestBuckets := n + 1
	bestDelay := 0
	found := false
	for code := 0; code < domainSize; code++ {
		remaining := code
		for i := 0; i < n; i++ {
			span := hi[i] - lo[i] + 1
			choice[i] = lo[i] + remaining%span
			remaining /= span
		}
		ok := true
		for _, edge := range p.Edges {
			if choice[edge[0]] > choice[edge[1]] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		seen := map[int]bool{}
		delay := 0
		for i, bucket := range choice {
			seen[bucket] = true
			delay += bucket - p.Times[i]
		}
		buckets := len(seen)
		if !found || buckets < bestBuckets || (buckets == bestBuckets && delay < bestDelay) {
			found = true
			bestBuckets = buckets
			bestDelay = delay
		}
	}
	return bestBuckets, bestDelay, found
}

func randomProblem(rng *rand.Rand, n, timeSpan, maxDelayBound int, edgeProbability float64) *Problem {
	times := make([]int, n)
	delays := make([]int, n)
	for i := range times {
		times[i] = rng.Intn(timeSpan)
		delays[i] = rng.Intn(maxDelayBound + 1)
	}
	edges := make([][2]int, 0)
	for u := 0; u < n; u++ {
		for v := u + 1; v < n; v++ {
			if rng.Float64() < edgeProbability {
				if rng.Intn(2) == 0 {
					edges = append(edges, [2]int{u, v})
				} else {
					edges = append(edges, [2]int{v, u})
				}
			}
		}
	}
	return &Problem{Times: times, MaxDelay: delays, Edges: edges}
}

func verifySolution(t *testing.T, p *Problem, sol *Solution) {
	t.Helper()
	n := len(p.Times)
	if sol == nil || len(sol.Buckets) != n {
		t.Fatalf("solution buckets length mismatch: %+v", sol)
	}
	delay := 0
	seen := map[int]bool{}
	for i, bucket := range sol.Buckets {
		if bucket < p.Times[i] || bucket > p.Times[i]+p.MaxDelay[i] {
			t.Fatalf("event %d bucket %d outside window [%d,%d]",
				i, bucket, p.Times[i], p.Times[i]+p.MaxDelay[i])
		}
		seen[bucket] = true
		delay += bucket - p.Times[i]
	}
	for _, edge := range p.Edges {
		if sol.Buckets[edge[0]] > sol.Buckets[edge[1]] {
			t.Fatalf("edge %v violates causal order: buckets=%v", edge, sol.Buckets)
		}
	}
	if len(seen) != sol.DistinctBuckets {
		t.Fatalf("DistinctBuckets=%d but %d distinct times in %v",
			sol.DistinctBuckets, len(seen), sol.Buckets)
	}
	if delay != sol.TotalDelay {
		t.Fatalf("TotalDelay=%d but recomputed delay is %d", sol.TotalDelay, delay)
	}

	covered := map[int]bool{}
	previousTime := 0
	groupCount := 0
	for groupIndex, group := range sol.Groups {
		if len(group) == 0 {
			t.Fatalf("group %d is empty", groupIndex)
		}
		bucketTime := sol.Buckets[group[0]]
		if groupIndex > 0 && bucketTime <= previousTime {
			t.Fatalf("group %d bucket times not strictly increasing", groupIndex)
		}
		previousTime = bucketTime
		for _, event := range group {
			if covered[event] {
				t.Fatalf("event %d appears in multiple groups", event)
			}
			covered[event] = true
			if sol.Buckets[event] != bucketTime {
				t.Fatalf("group %d mixes bucket times", groupIndex)
			}
		}
		groupCount++
	}
	if groupCount != sol.DistinctBuckets || len(covered) != n {
		t.Fatalf("group coverage mismatch: groups=%d covered=%d n=%d", groupCount, len(covered), n)
	}
}

// verifyWitness independently replays the reported propagation path and
// confirms it proves the deadline violation.
func verifyWitness(t *testing.T, p *Problem, infeasible *InfeasibleError) {
	t.Helper()
	if infeasible == nil {
		t.Fatal("missing infeasibility error")
	}
	event := infeasible.Event
	if event < 0 || event >= len(p.Times) {
		t.Fatalf("witness event out of range: %+v", infeasible)
	}
	if infeasible.Limit != p.Times[event]+p.MaxDelay[event] {
		t.Fatalf("witness limit %d does not match event deadline", infeasible.Limit)
	}
	if len(infeasible.Witness) == 0 || infeasible.Witness[len(infeasible.Witness)-1] != event {
		t.Fatalf("witness path must end at event %d: %v", event, infeasible.Witness)
	}
	edgeSet := map[[2]int]bool{}
	for _, edge := range p.Edges {
		edgeSet[edge] = true
	}
	origin := infeasible.Witness[0]
	propagated := p.Times[origin]
	for pathIndex := 1; pathIndex < len(infeasible.Witness); pathIndex++ {
		from := infeasible.Witness[pathIndex-1]
		to := infeasible.Witness[pathIndex]
		if !edgeSet[[2]int{from, to}] {
			t.Fatalf("witness uses non-existent edge %d->%d", from, to)
		}
	}
	if propagated != infeasible.LowerBound {
		t.Fatalf("witness origin time %d does not explain lower bound %d",
			propagated, infeasible.LowerBound)
	}
	if infeasible.LowerBound <= infeasible.Limit {
		t.Fatalf("witness does not demonstrate a violation: bound %d <= limit %d",
			infeasible.LowerBound, infeasible.Limit)
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
