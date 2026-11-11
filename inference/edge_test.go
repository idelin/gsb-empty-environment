package inference_test

import (
	"testing"

	"example.org/netsec/q0445/inference"
)

func TestEmptyUniverse(t *testing.T) {
	r, err := inference.New(nil, 0, inference.DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeExact {
		t.Fatalf("empty universe should be exact, got %v", r.Mode())
	}
	res, err := r.Observe(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Consistent || len(res.ForcedIn) != 0 || len(res.ForcedOut) != 0 {
		t.Fatalf("empty universe result wrong: %+v", res)
	}
	if count, exact := r.PossibleCount(); !exact || count != 1 {
		t.Fatalf("empty universe has exactly one (empty) secret set, got %d,%v", count, exact)
	}
	// Any positive answer is impossible.
	if _, err := r.Observe(nil, 1); err == nil {
		t.Fatal("positive answer on empty universe must error")
	}
}

func TestEmptySubsetQuery(t *testing.T) {
	r, _ := inference.New(names(5), 2, inference.DefaultBudget)
	before := r.NodesVisited()
	res, err := r.Observe(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.NodesVisited() != before {
		t.Fatal("empty subset query should not run a search")
	}
	if len(res.NewlyForcedIn) != 0 || len(res.NewlyForcedOut) != 0 {
		t.Fatal("empty query cannot determine any element")
	}
	if _, err := r.Observe([]string{}, 1); err == nil {
		t.Fatal("count 1 on empty subset must error")
	}
}

func TestRepeatedQueryDedup(t *testing.T) {
	r, _ := inference.New(names(7), 3, inference.DefaultBudget)
	query := []string{"e1", "e3", "e5"}
	r.Observe(query, 2)
	before := r.NodesVisited()
	countBefore, exactBefore := r.PossibleCount()

	// Same subset, same count, different slice/order: recognized as repeat.
	res, err := r.Observe([]string{"e5", "e1", "e3"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.NodesVisited() != before {
		t.Fatal("repeat must not spend search budget")
	}
	countAfter, exactAfter := r.PossibleCount()
	if countAfter != countBefore || exactAfter != exactBefore {
		t.Fatal("repeat changed compatible-set count")
	}
	if len(res.NewlyForcedIn) != 0 || len(res.NewlyForcedOut) != 0 {
		t.Fatal("repeat must not surface new elements")
	}
}

func TestSnapshotDoesNotMutateState(t *testing.T) {
	r, _ := inference.New(names(6), 2, inference.DefaultBudget)
	r.Observe([]string{"e0"}, 1)
	first := r.Snapshot()
	second := r.Snapshot()
	if len(second.NewlyForcedIn) != 0 || len(second.NewlyForcedOut) != 0 {
		t.Fatalf("second snapshot re-reported elements: %+v", second)
	}
	if len(first.ForcedIn) != len(second.ForcedIn) {
		t.Fatal("snapshot altered accumulated forced set")
	}
}

func TestAccessorsAndDefaultConstructor(t *testing.T) {
	candidates := names(5)
	r, err := inference.NewDefault(candidates, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Budget() != inference.DefaultBudget {
		t.Fatalf("budget = %d", r.Budget())
	}
	got := r.Universe()
	if len(got) != len(candidates) {
		t.Fatalf("universe length = %d", len(got))
	}
	got[0] = "mutated"
	if r.Universe()[0] != candidates[0] {
		t.Fatal("Universe must return a defensive copy")
	}
	if r.Mode() != inference.ModeExact || !r.IsConsistent() || r.NodesVisited() == 0 {
		t.Fatalf("unexpected initial state: mode=%v consistent=%v visited=%d",
			r.Mode(), r.IsConsistent(), r.NodesVisited())
	}

	lower, upper := r.Bounds()
	for _, name := range candidates {
		if lower[name] != 0 || upper[name] != 1 {
			t.Fatalf("initial bounds for %s = [%d,%d], want [0,1]", name, lower[name], upper[name])
		}
	}
}

func TestGosperEnumerationAtMaxSize(t *testing.T) {
	const n = inference.MaxElements
	k := n / 2 // 12: densest enumeration, C(24,12) = 2,704,156
	r, err := inference.New(names(n), k, 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeExact {
		t.Fatalf("expected exact mode at max size, got %v", r.Mode())
	}
	count, exact := r.PossibleCount()
	if !exact {
		t.Fatal("count must be exact in ModeExact")
	}
	// C(24,12) computed independently via integer arithmetic.
	num := int64(1)
	for i := 1; i <= k; i++ {
		num = num * int64(n-k+i) / int64(i)
	}
	if int64(count) != num {
		t.Fatalf("enumerated %d sets, want C(%d,%d)=%d", count, n, k, num)
	}
	if len(r.PossibleSets()) != count {
		t.Fatal("PossibleSets length disagrees with PossibleCount")
	}

	// Every enumerated set must have exactly k distinct members.
	for _, set := range r.PossibleSets() {
		if len(set) != k {
			t.Fatalf("set cardinality %d, want %d", len(set), k)
		}
	}
}
