package corpustrim

import (
	"math/rand"
	"sort"
	"testing"
)

// bruteForce merges duplicates the same way Trim does, then enumerates all
// subsets of merged groups to find the lexicographic (count, cost) optimum.
func bruteForce(inputs []Input) (int, int64) {
	type grp struct {
		cost  int64
		cover map[string]bool
	}
	byContent := map[string]*grp{}
	order := []string{}
	for _, in := range inputs {
		g, ok := byContent[in.Content]
		if !ok {
			g = &grp{cost: in.Cost, cover: map[string]bool{}}
			byContent[in.Content] = g
			order = append(order, in.Content)
		} else if in.Cost < g.cost {
			g.cost = in.Cost
		}
		for _, e := range in.Coverage {
			g.cover["cov:"+e] = true
		}
		if in.FailureCategory != "" {
			g.cover["fail:"+in.FailureCategory] = true
		}
		for _, c := range in.FailureCategories {
			g.cover["fail:"+c] = true
		}
	}
	// Drop empty / dominated groups the same way Trim may; enumeration over
	// all groups is still correct without it, so keep it simple.
	groups := make([]*grp, 0, len(order))
	for _, k := range order {
		groups = append(groups, byContent[k])
	}
	universe := map[string]bool{}
	for _, g := range groups {
		for e := range g.cover {
			universe[e] = true
		}
	}
	bestCount := len(groups) + 1
	var bestCost int64
	m := len(groups)
	for mask := 0; mask < 1<<uint(m); mask++ {
		covered := map[string]bool{}
		var cost int64
		cnt := 0
		for i := 0; i < m; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			cnt++
			cost += groups[i].cost
			for e := range groups[i].cover {
				covered[e] = true
			}
		}
		if len(covered) != len(universe) {
			continue
		}
		if cnt < bestCount || (cnt == bestCount && cost < bestCost) || bestCount == len(groups)+1 {
			if cnt < bestCount || cost < bestCost || bestCount == len(groups)+1 {
				bestCount, bestCost = cnt, cost
			}
		}
	}
	return bestCount, bestCost
}

func checkCoversUniverse(t *testing.T, inputs []Input, res *Result) {
	t.Helper()
	full := map[string]bool{}
	for _, in := range inputs {
		for _, e := range in.Coverage {
			full["cov:"+e] = true
		}
		if in.FailureCategory != "" {
			full["fail:"+in.FailureCategory] = true
		}
		for _, c := range in.FailureCategories {
			full["fail:"+c] = true
		}
	}
	got := map[string]bool{}
	var cost int64
	for _, k := range res.Kept {
		cost += k.Cost
		for _, e := range k.Coverage {
			got["cov:"+e] = true
		}
		for _, c := range k.FailureCategories {
			got["fail:"+c] = true
		}
	}
	for e := range full {
		if !got[e] {
			t.Fatalf("kept set lost element %s", e)
		}
	}
	if cost != res.TotalCost {
		t.Fatalf("total cost mismatch: kept sums to %d, result says %d", cost, res.TotalCost)
	}
	if len(res.Kept) != res.Samples {
		t.Fatalf("samples=%d but %d kept items", res.Samples, len(res.Kept))
	}
	if res.LowerBound.Samples > res.Samples {
		t.Fatalf("lower bound on samples %d exceeds optimum %d", res.LowerBound.Samples, res.Samples)
	}
	if res.LowerBound.Cost > res.TotalCost {
		t.Fatalf("lower bound on cost %d exceeds optimum %d", res.LowerBound.Cost, res.TotalCost)
	}
}

func TestEmpty(t *testing.T) {
	res, err := Trim(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 0 || res.TotalCost != 0 || len(res.Kept) != 0 {
		t.Fatalf("expected empty result, got %+v", res)
	}
}

func TestDuplicatesMergedCoveragePreserved(t *testing.T) {
	inputs := []Input{
		{ID: "a1", Content: "same", Cost: 9, Coverage: []string{"e1"}},
		{ID: "a2", Content: "same", Cost: 3, Coverage: []string{"e2"}, FailureCategories: []string{"crash"}},
		{ID: "b", Content: "other", Cost: 1, Coverage: []string{"e3"}},
	}
	res, err := Trim(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if res.DuplicatesMerged != 1 {
		t.Fatalf("expected 1 merged duplicate, got %d", res.DuplicatesMerged)
	}
	if res.Samples != 2 {
		t.Fatalf("expected 2 samples, got %d", res.Samples)
	}
	var merged *KeptItem
	for i := range res.Kept {
		if res.Kept[i].Content == "same" {
			merged = &res.Kept[i]
		}
	}
	if merged == nil {
		t.Fatal("merged group missing from kept set")
	}
	if merged.Cost != 3 {
		t.Fatalf("merged cost should be the cheapest (3), got %d", merged.Cost)
	}
	sort.Strings(merged.Coverage)
	if len(merged.Coverage) != 2 || merged.Coverage[0] != "e1" || merged.Coverage[1] != "e2" {
		t.Fatalf("merged coverage lost an observation: %v", merged.Coverage)
	}
	if len(merged.FailureCategories) != 1 || merged.FailureCategories[0] != "crash" {
		t.Fatalf("merged failure categories lost: %v", merged.FailureCategories)
	}
	if len(merged.IDs) != 2 {
		t.Fatalf("merged group should reference both ids, got %v", merged.IDs)
	}
	checkCoversUniverse(t, inputs, res)
}

func TestCountBeatsCost(t *testing.T) {
	// One expensive input covers everything; two cheap ones cover it jointly.
	// Fewer samples must win even though it costs more.
	inputs := []Input{
		{ID: "expensive", Content: "x", Cost: 100, Coverage: []string{"e1", "e2"}},
		{ID: "cheap1", Content: "y", Cost: 1, Coverage: []string{"e1"}},
		{ID: "cheap2", Content: "z", Cost: 1, Coverage: []string{"e2"}},
	}
	res, err := Trim(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 1 || res.TotalCost != 100 {
		t.Fatalf("expected 1 sample costing 100, got %d samples costing %d", res.Samples, res.TotalCost)
	}
}

func TestCostBreaksTies(t *testing.T) {
	inputs := []Input{
		{ID: "dear", Content: "x", Cost: 10, Coverage: []string{"e1"}},
		{ID: "cheap", Content: "y", Cost: 2, Coverage: []string{"e1"}},
	}
	res, err := Trim(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 1 || res.TotalCost != 2 || res.Kept[0].IDs[0] != "cheap" {
		t.Fatalf("expected the cheap input, got %+v", res.Kept)
	}
}

func TestFailureCategoriesMustBeCovered(t *testing.T) {
	inputs := []Input{
		{ID: "covers-all", Content: "x", Cost: 1, Coverage: []string{"e1", "e2"}},
		{ID: "fails", Content: "y", Cost: 5, FailureCategories: []string{"timeout"}},
	}
	res, err := Trim(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 2 {
		t.Fatalf("failure category must force keeping the second input, got %d samples", res.Samples)
	}
	checkCoversUniverse(t, inputs, res)
}

func TestSingularFailureCategoryField(t *testing.T) {
	inputs := []Input{
		{ID: "a", Content: "x", Cost: 1, Coverage: []string{"e1"}},
		{ID: "b", Content: "y", Cost: 1, FailureCategory: "crash"},
	}
	res, err := Trim(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 2 || res.FailureCategories != 1 {
		t.Fatalf("singular failure_category not honored: %+v", res)
	}
}

func TestTooManyInputs(t *testing.T) {
	inputs := make([]Input, 31)
	if _, err := Trim(inputs); err == nil {
		t.Fatal("expected an error for 31 inputs")
	}
}

func TestNegativeCostRejected(t *testing.T) {
	if _, err := Trim([]Input{{Content: "x", Cost: -1}}); err == nil {
		t.Fatal("expected an error for negative cost")
	}
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(14) // 1..14 raw inputs
		numEdges := 1 + rng.Intn(8)
		numCats := rng.Intn(3)
		inputs := make([]Input, n)
		for i := range inputs {
			in := Input{
				ID:      string(rune('a' + i)),
				Content: string(rune('A'+rng.Intn(n))) + "-content",
				Cost:    int64(rng.Intn(20)),
			}
			for _, e := range rng.Perm(numEdges)[:1+rng.Intn(numEdges)] {
				in.Coverage = append(in.Coverage, string(rune('0'+e)))
			}
			if numCats > 0 && rng.Intn(2) == 0 {
				in.FailureCategories = []string{string(rune('C' + rng.Intn(numCats)))}
			}
			inputs[i] = in
		}
		res, err := Trim(inputs)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		wantCount, wantCost := bruteForce(inputs)
		if res.Samples != wantCount || res.TotalCost != wantCost {
			t.Fatalf("trial %d: got (%d,%d), brute force says (%d,%d)\ninputs=%+v",
				trial, res.Samples, res.TotalCost, wantCount, wantCost, inputs)
		}
		checkCoversUniverse(t, inputs, res)
	}
}

func TestThirtyInputsSolves(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	inputs := make([]Input, 30)
	for i := range inputs {
		in := Input{
			ID:      string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Content: string(rune('A' + i)),
			Cost:    int64(1 + rng.Intn(50)),
		}
		for _, e := range rng.Perm(40)[:4] {
			in.Coverage = append(in.Coverage, string(rune('a'+e%26))+string(rune('0'+e/26)))
		}
		if i%5 == 0 {
			in.FailureCategories = []string{string(rune('C' + i/5))}
		}
		inputs[i] = in
	}
	res, err := Trim(inputs)
	if err != nil {
		t.Fatal(err)
	}
	checkCoversUniverse(t, inputs, res)
	t.Logf("30 inputs: %d samples, cost %d, %d search nodes", res.Samples, res.TotalCost, res.SearchNodes)
}
