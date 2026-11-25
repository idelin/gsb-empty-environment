package admission

import (
	"math/rand"
	"sort"
	"testing"
)

func admissionTestCheckpointAt(tree *Tree, size uint64) Checkpoint {
	if size == 0 {
		return NewCheckpoint(0, EmptyRoot())
	}
	n := int(size)
	// Rebuild the prefix root from the tree's leaf hashes, since Tree does not
	// expose raw leaves. A fresh tree is sufficient for these deterministic
	// test leaves.
	return NewCheckpoint(size, MerkleRoot(admissionTestLeaves(n)))
}

func TestImplementationPlanUsesSharedTargetEdgeForMultipleRoots(t *testing.T) {
	pub, _ := admissionTestPublication(t, 8, 6)
	cp2 := admissionTestCheckpointAt(nil, 2)
	cp3 := admissionTestCheckpointAt(nil, 3)
	cp6 := admissionTestCheckpointAt(nil, 6)
	cp8 := pub.Target

	offers := []ProofOffer{
		{ID: "a", Provider: "p1", Cost: 2, Covers: []EdgeRef{{From: cp2, To: cp6}}},
		{ID: "b", Provider: "p2", Cost: 2, Covers: []EdgeRef{{From: cp3, To: cp6}}},
		{ID: "c", Provider: "p3", Cost: 1, Covers: []EdgeRef{{From: cp6, To: cp8}}},
		{ID: "d", Provider: "p4", Cost: 10, Covers: []EdgeRef{{From: cp2, To: cp8}}},
		{ID: "e", Provider: "p5", Cost: 10, Covers: []EdgeRef{{From: cp3, To: cp8}}},
	}

	e := NewEvaluator([]Checkpoint{cp2, cp3}, Policy{Threshold: 0})
	result := e.Evaluate(pub, nil, offers)
	if result.Status != StatusUnknown {
		t.Fatalf("status=%s want unknown while plan is only conditional", result.Status)
	}
	if result.Plan == nil {
		t.Fatal("expected a supplement plan")
	}
	if result.Plan.Cost != 5 {
		t.Fatalf("plan cost=%d want 5 (shared edge must serve both roots)", result.Plan.Cost)
	}
	wantIDs := []string{"a", "b", "c"}
	if !admissionTestEqualStrings(result.Plan.OfferIDs, wantIDs) {
		t.Fatalf("plan offers=%v want %v", result.Plan.OfferIDs, wantIDs)
	}

	// Exhaustive small-scale check against every subset.
	bruteCost, bruteIDs, ok := admissionTestBruteMinimum([]Checkpoint{cp2, cp3}, cp8, offers, PlanLimits{})
	if !ok || bruteCost != result.Plan.Cost || !admissionTestEqualStrings(bruteIDs, result.Plan.OfferIDs) {
		t.Fatalf("exhaustive plan=(%d,%v) package plan=(%d,%v)", bruteCost, bruteIDs, result.Plan.Cost, result.Plan.OfferIDs)
	}
}

func TestImplementationPlanRespectsPerProviderAcquisitionCap(t *testing.T) {
	pub, _ := admissionTestPublication(t, 8, 6)
	cp2 := admissionTestCheckpointAt(nil, 2)
	cp3 := admissionTestCheckpointAt(nil, 3)
	cp6 := admissionTestCheckpointAt(nil, 6)
	cp8 := pub.Target
	offers := []ProofOffer{
		{ID: "a", Provider: "shared", Cost: 1, Covers: []EdgeRef{{From: cp2, To: cp6}}},
		{ID: "b", Provider: "shared", Cost: 1, Covers: []EdgeRef{{From: cp3, To: cp6}}},
		{ID: "c", Provider: "backup-1", Cost: 5, Covers: []EdgeRef{{From: cp2, To: cp6}}},
		{ID: "d", Provider: "backup-2", Cost: 5, Covers: []EdgeRef{{From: cp3, To: cp6}}},
		{ID: "e", Provider: "tail", Cost: 1, Covers: []EdgeRef{{From: cp6, To: cp8}}},
	}
	limits := PlanLimits{MaxAcquisitionsPerProvider: map[string]int{"shared": 1}}
	e := NewEvaluator([]Checkpoint{cp2, cp3}, Policy{Threshold: 0})
	plan, ok := e.SupplementPlan(cp8, nil, offers, limits)
	if !ok {
		t.Fatal("expected a plan under provider cap")
	}
	if plan.Cost != 7 {
		t.Fatalf("cost=%d want 7 after shared provider cap", plan.Cost)
	}
	if plan.ProviderCounts["shared"] != 1 {
		t.Fatalf("shared provider used %d times", plan.ProviderCounts["shared"])
	}

	bruteCost, bruteIDs, ok := admissionTestBruteMinimum([]Checkpoint{cp2, cp3}, cp8, offers, limits)
	if !ok || bruteCost != plan.Cost || !admissionTestEqualStrings(bruteIDs, plan.OfferIDs) {
		t.Fatalf("exhaustive plan=(%d,%v) package plan=(%d,%v)", bruteCost, bruteIDs, plan.Cost, plan.OfferIDs)
	}
}

func TestImplementationPlanMatchesExhaustiveSubsetsRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(20261109))
	checkpoints := make([]Checkpoint, 8)
	for n := 1; n <= 8; n++ {
		checkpoints[n-1] = admissionTestCheckpointAt(nil, uint64(n))
	}
	for trial := 0; trial < 100; trial++ {
		target := checkpoints[7]
		anchors := []Checkpoint{checkpoints[rng.Intn(6)]}
		if rng.Intn(2) == 0 {
			anchors = append(anchors, checkpoints[rng.Intn(6)])
		}
		providers := []string{"p1", "p2", "p3"}
		offers := make([]ProofOffer, 6)
		for i := range offers {
			from := rng.Intn(7)
			to := from + 1 + rng.Intn(8-(from+1))
			offers[i] = ProofOffer{
				ID:       string(rune('a' + i)),
				Provider: providers[rng.Intn(len(providers))],
				Cost:     int64(rng.Intn(6) + 1),
				Covers: []EdgeRef{{
					From: checkpoints[from],
					To:   checkpoints[to-1],
				}},
			}
		}
		limits := PlanLimits{MaxAcquisitionsPerProvider: map[string]int{"p1": rng.Intn(3), "p2": rng.Intn(3), "p3": rng.Intn(3)}}
		e := NewEvaluator(anchors, Policy{Threshold: 0})
		plan, ok := e.SupplementPlan(target, nil, offers, limits)
		bruteCost, bruteIDs, bruteOK := admissionTestBruteMinimum(anchors, target, offers, limits)
		if ok != bruteOK {
			t.Fatalf("trial %d: plan ok=%v brute ok=%v", trial, ok, bruteOK)
		}
		if ok && (plan.Cost != bruteCost || !admissionTestEqualStrings(plan.OfferIDs, bruteIDs)) {
			t.Fatalf("trial %d: plan=(%d,%v) brute=(%d,%v)", trial, plan.Cost, plan.OfferIDs, bruteCost, bruteIDs)
		}
	}
}

func admissionTestEqualStrings(a, b []string) bool {
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

// admissionTestBruteMinimum is intentionally simple: it enumerates every
// subset and is used only as an oracle for small tests.
func admissionTestBruteMinimum(anchors []Checkpoint, target Checkpoint, offers []ProofOffer, limits PlanLimits) (int64, []string, bool) {
	n := len(offers)
	if n > 20 {
		panic("subset oracle is for small inputs")
	}
	var bestCost int64
	var bestIDs []string
	ok := false
	for mask := 1; mask < 1<<n; mask++ {
		counts := make(map[string]int)
		feasible := true
		var cost int64
		var ids []string
		g := newEvidenceGraph()
		for _, anchor := range anchors {
			g.addNode(anchor)
		}
		for i, offer := range offers {
			if mask&(1<<i) == 0 {
				continue
			}
			counts[offer.Provider]++
			if max, capped := limits.limit(offer.Provider); capped && counts[offer.Provider] > max {
				feasible = false
				break
			}
			cost += offer.cost()
			ids = append(ids, offer.ID)
			for _, ref := range offer.edges() {
				g.addEdge(ref.From, ref.To)
			}
		}
		if !feasible || !connectsAll(g, anchors, target) {
			continue
		}
		sort.Strings(ids)
		better := false
		if ok {
			if cost < bestCost {
				better = true
			} else if cost == bestCost {
				better = len(ids) < len(bestIDs) ||
					(len(ids) == len(bestIDs) && admissionTestLexLess(ids, bestIDs))
			}
		}
		if !ok || better {
			ok = true
			bestCost = cost
			bestIDs = ids
		}
	}
	return bestCost, bestIDs, ok
}

func admissionTestLexLess(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return len(a) < len(b)
}
