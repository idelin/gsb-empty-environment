package main

import (
	"math"
	"math/rand"
	"testing"
)

// bruteForce enumerates all subsets of the merged candidates and returns the
// lexicographically minimal (count, cost) among feasible ones.
func bruteForce(cands []*candidate, nreq int) (int, int64) {
	bestCnt, bestCost := -1, int64(math.MaxInt64)
	n := len(cands)
	for mask := 0; mask < (1 << n); mask++ {
		cover := newBits(nreq)
		cnt := 0
		var cost int64
		for i := 0; i < n; i++ {
			if mask>>uint(i)&1 == 1 {
				cover = cover.or(cands[i].cover)
				cnt++
				cost += cands[i].cost
			}
		}
		if cover.popcount() != nreq {
			continue
		}
		if bestCnt < 0 || cnt < bestCnt || (cnt == bestCnt && cost < bestCost) {
			bestCnt, bestCost = cnt, cost
		}
	}
	return bestCnt, bestCost
}

func solveRaw(t *testing.T, inputs []rawInput) *result {
	t.Helper()
	res, err := trim(inputs)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	return res
}

func TestBasicMinCountThenCost(t *testing.T) {
	// Greedy-by-coverage would pick t1 (3 edges), but t2+t3 covers everything
	// with the same count and lower cost than t1+t4.
	res := solveRaw(t, []rawInput{
		{ID: "t1", Content: "a", Cost: 10, Edges: []string{"e1", "e2", "e3"}},
		{ID: "t2", Content: "b", Cost: 3, Edges: []string{"e1", "e2"}},
		{ID: "t3", Content: "c", Cost: 3, Edges: []string{"e3", "e4"}},
		{ID: "t4", Content: "d", Cost: 1, Edges: []string{"e4"}},
	})
	if res.NumSelected != 2 || res.TotalCost != 6 {
		t.Fatalf("got count=%d cost=%d, want 2/6", res.NumSelected, res.TotalCost)
	}
	ids := map[string]bool{}
	for _, s := range res.Selected {
		ids[s.ID] = true
	}
	if !ids["t2"] || !ids["t3"] {
		t.Fatalf("want t2+t3, got %v", ids)
	}
}

func TestDuplicatesMergedUnionCoverage(t *testing.T) {
	// Same content observed twice with different edges: merged candidate must
	// retain the union, so a single retained input covers both edges.
	res := solveRaw(t, []rawInput{
		{ID: "r1", Content: "x", Cost: 5, Edges: []string{"e1"}},
		{ID: "r2", Content: "x", Cost: 4, Edges: []string{"e2"}},
		{ID: "r3", Content: "y", Cost: 1, Edges: []string{"e1"}},
		{ID: "r4", Content: "z", Cost: 1, Edges: []string{"e2"}},
	})
	if res.NumSelected != 1 {
		t.Fatalf("got %d selected, want 1 (merged duplicate covers both edges)", res.NumSelected)
	}
	if res.Selected[0].ID != "r1" {
		t.Fatalf("representative = %q, want r1", res.Selected[0].ID)
	}
	if len(res.Selected[0].MergedIDs) != 1 || res.Selected[0].MergedIDs[0] != "r2" {
		t.Fatalf("merged ids = %v, want [r2]", res.Selected[0].MergedIDs)
	}
	if res.Selected[0].Cost != 4 {
		t.Fatalf("merged cost = %d, want min cost 4", res.Selected[0].Cost)
	}
	if len(res.DuplicatesMerged) != 1 {
		t.Fatalf("duplicates = %v, want 1 entry", res.DuplicatesMerged)
	}
}

func TestFailureCategoriesMustBeCovered(t *testing.T) {
	// t1 covers all edges but has no failure; both failure categories must
	// still be retained.
	res := solveRaw(t, []rawInput{
		{ID: "t1", Content: "a", Cost: 1, Edges: []string{"e1", "e2"}},
		{ID: "t2", Content: "b", Cost: 1, Edges: []string{"e1"}, Failure: "crash-A"},
		{ID: "t3", Content: "c", Cost: 1, Edges: []string{"e2"}, Failure: "crash-B"},
	})
	if res.NumSelected != 2 {
		t.Fatalf("got %d selected, want 2 (t2+t3 cover edges and both failures)", res.NumSelected)
	}
	fails := map[string]bool{}
	for _, s := range res.Selected {
		for _, f := range s.Failures {
			fails[f] = true
		}
	}
	if !fails["crash-A"] || !fails["crash-B"] {
		t.Fatalf("retained failures = %v, want both crash-A and crash-B", fails)
	}
	if res.LowerBound.MinInputs < 2 {
		t.Fatalf("lower bound %d too low, want >= 2 (two failure categories)", res.LowerBound.MinInputs)
	}
}

func TestLowerBoundNotAboveOptimum(t *testing.T) {
	res := solveRaw(t, []rawInput{
		{ID: "a", Content: "a", Cost: 5, Edges: []string{"e1"}},
		{ID: "b", Content: "b", Cost: 7, Edges: []string{"e2"}},
		{ID: "c", Content: "c", Cost: 20, Edges: []string{"e1", "e2"}},
	})
	if res.LowerBound.MinInputs > res.NumSelected {
		t.Fatalf("lb %d > optimum %d", res.LowerBound.MinInputs, res.NumSelected)
	}
	if res.LowerBound.MinCost > res.TotalCost {
		t.Fatalf("cost lb %d > optimum %d", res.LowerBound.MinCost, res.TotalCost)
	}
	if res.NumSelected != 1 || res.TotalCost != 20 {
		t.Fatalf("got (%d,%d), want (1,20): c alone covers both edges", res.NumSelected, res.TotalCost)
	}
	if res.LowerBound.MinInputs != 1 {
		t.Fatalf("lb = %d, want 1 (c covers both e1 and e2)", res.LowerBound.MinInputs)
	}
}

func TestTooManyInputs(t *testing.T) {
	data := []byte(`[`)
	for i := 0; i < maxInputs+1; i++ {
		if i > 0 {
			data = append(data, ',')
		}
		data = append(data, []byte(`{"id":"x","content":"c"}`)...)
	}
	data = append(data, ']')
	if _, err := parseInputs(data); err == nil {
		t.Fatal("want error for >30 inputs")
	}
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(12) // merged count stays <= 12 so brute force is cheap
		nEdges := 1 + rng.Intn(8)
		nFails := 1 + rng.Intn(3)
		var inputs []rawInput
		for i := 0; i < n; i++ {
			in := rawInput{ID: "i" + itoa(i), Cost: int64(1 + rng.Intn(9))}
			// Sometimes duplicate an earlier content to exercise merging.
			if i > 0 && rng.Intn(4) == 0 {
				in.Content = "c" + itoa(rng.Intn(i))
			} else {
				in.Content = "c" + itoa(i)
			}
			for e := 0; e < nEdges; e++ {
				if rng.Intn(2) == 0 {
					in.Edges = append(in.Edges, "e"+itoa(e))
				}
			}
			if rng.Intn(3) == 0 {
				in.Failure = "f" + itoa(rng.Intn(nFails))
			}
			inputs = append(inputs, in)
		}
		// Ensure the instance is feasible: union of edges must be coverable
		// (it always is, since requirements come from the inputs themselves).

		cands, _ := mergeDuplicates(inputs)
		nreq, _ := buildUniverse(cands)
		wantCnt, wantCost := bruteForce(cands, nreq)

		res, err := trim(inputs)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if res.NumSelected != wantCnt || res.TotalCost != wantCost {
			t.Fatalf("trial %d: got (%d,%d), want (%d,%d)\ninputs=%+v",
				trial, res.NumSelected, res.TotalCost, wantCnt, wantCost, inputs)
		}
		// Verify the returned selection is actually feasible and consistent.
		verifySelection(t, cands, res, nreq)
		if res.LowerBound.MinInputs > res.NumSelected {
			t.Fatalf("trial %d: lb %d exceeds optimum %d", trial, res.LowerBound.MinInputs, res.NumSelected)
		}
		if res.LowerBound.MinCost > res.TotalCost {
			t.Fatalf("trial %d: cost lb %d exceeds optimum %d", trial, res.LowerBound.MinCost, res.TotalCost)
		}
	}
}

// verifySelection checks that the selected set covers every requirement and
// that the reported cost matches the sum of selected candidate costs.
func verifySelection(t *testing.T, cands []*candidate, res *result, nreq int) {
	t.Helper()
	byID := map[string]*candidate{}
	for _, c := range cands {
		byID[c.ids[0]] = c
	}
	cover := newBits(nreq)
	var cost int64
	for _, s := range res.Selected {
		c, ok := byID[s.ID]
		if !ok {
			t.Fatalf("selected unknown id %q", s.ID)
		}
		cover = cover.or(c.cover)
		cost += c.cost
	}
	if cover.popcount() != nreq {
		t.Fatalf("selection covers %d of %d requirements", cover.popcount(), nreq)
	}
	if cost != res.TotalCost {
		t.Fatalf("reported cost %d != actual %d", res.TotalCost, cost)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
