package inference_test

import (
	"errors"
	"math/bits"
	"math/rand"
	"testing"

	"example.org/netsec/q0445/inference"
)

func names(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = elementName(i)
	}
	return out
}

func elementName(i int) string {
	return "e" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// allKSets enumerates every k-subset mask of an n-element universe: the
// full-subset reference used to validate the reasoner.
func allKSets(n, k int) []uint32 {
	var sets []uint32
	var dfs func(pos, chosen int, mask uint32)
	dfs = func(pos, chosen int, mask uint32) {
		if chosen == k {
			sets = append(sets, mask)
			return
		}
		if pos == n || n-pos < k-chosen {
			return
		}
		dfs(pos+1, chosen+1, mask|(uint32(1)<<uint(pos)))
		dfs(pos+1, chosen, mask)
	}
	dfs(0, 0, 0)
	return sets
}

type observation struct {
	mask  uint32
	count int
}

func referenceFamilies(n, k int, obs []observation) []uint32 {
	sets := allKSets(n, k)
	kept := sets[:0]
	for _, candidate := range sets {
		ok := true
		for _, ob := range obs {
			if bits.OnesCount32(candidate&ob.mask) != ob.count {
				ok = false
				break
			}
		}
		if ok {
			kept = append(kept, candidate)
		}
	}
	return kept
}

func masksOf(names []string, index map[string]int) uint32 {
	var mask uint32
	for _, name := range names {
		mask |= uint32(1) << uint(index[name])
	}
	return mask
}

func subsetNames(candidates []string, mask uint32) []string {
	var out []string
	for i, name := range candidates {
		if mask&(uint32(1)<<uint(i)) != 0 {
			out = append(out, name)
		}
	}
	return out
}

func intersectCount(secret, query uint32) int {
	return bits.OnesCount32(secret & query)
}

func TestValidationErrors(t *testing.T) {
	if _, err := inference.New(names(inference.MaxElements+1), 1, inference.DefaultBudget); !errors.Is(err, inference.ErrTooManyElements) {
		t.Fatalf("MaxElements: got %v", err)
	}
	if _, err := inference.New(names(3), 4, inference.DefaultBudget); !errors.Is(err, inference.ErrBadCardinality) {
		t.Fatalf("cardinality high: got %v", err)
	}
	if _, err := inference.New(names(3), -1, inference.DefaultBudget); !errors.Is(err, inference.ErrBadCardinality) {
		t.Fatalf("cardinality negative: got %v", err)
	}
	if _, err := inference.New(names(3), 1, 0); !errors.Is(err, inference.ErrBadBudget) {
		t.Fatalf("zero budget: got %v", err)
	}
	dup := []string{"a", "b", "a"}
	if _, err := inference.New(dup, 1, inference.DefaultBudget); !errors.Is(err, inference.ErrDuplicateName) {
		t.Fatalf("duplicate name: got %v", err)
	}

	r, err := inference.New(names(4), 2, inference.DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Observe([]string{"e0", "zzz"}, 1); !errors.Is(err, inference.ErrUnknownElement) {
		t.Fatalf("unknown element: got %v", err)
	}
	if _, err := r.Observe([]string{"e0", "e1"}, 3); !errors.Is(err, inference.ErrBadResponse) {
		t.Fatalf("bad response: got %v", err)
	}
}

func TestExactModeReconstruction(t *testing.T) {
	const n, k = 6, 3
	r, err := inference.New(names(n), k, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeExact {
		t.Fatalf("expected exact mode, got %v", r.Mode())
	}
	count, exact := r.PossibleCount()
	wantCount := len(allKSets(n, k))
	if !exact || count != wantCount {
		t.Fatalf("initial count = (%d,%v), want %d", count, exact, wantCount)
	}

	// Singleton queries pin membership directly.
	res, err := r.Observe([]string{"e2"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NewlyForcedIn) != 1 || res.NewlyForcedIn[0] != "e2" {
		t.Fatalf("newly forced in = %v", res.NewlyForcedIn)
	}
	res, err = r.Observe([]string{"e4"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NewlyForcedOut) != 1 || res.NewlyForcedOut[0] != "e4" {
		t.Fatalf("newly forced out = %v", res.NewlyForcedOut)
	}

	// The already-known element must not be reported again.
	res, _ = r.Observe([]string{"e2", "e3"}, 1)
	if len(res.NewlyForcedIn) != 0 || len(res.NewlyForcedOut) != 1 || res.NewlyForcedOut[0] != "e3" {
		t.Fatalf("repeat/first-surface mismatch: %+v", res)
	}
	if got := r.ForcedIn(); len(got) != 1 || got[0] != "e2" {
		t.Fatalf("forced in = %v", got)
	}
	if got := r.ForcedOut(); len(got) != 2 {
		t.Fatalf("forced out = %v", got)
	}

	// Pin the remaining members; the last observation fully identifies S.
	r.Observe([]string{"e0"}, 1)
	res, err = r.Observe([]string{"e1"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if count, exact = r.PossibleCount(); !exact || count != 1 {
		t.Fatalf("final count = (%d,%v)", count, exact)
	}
	sets := r.PossibleSets()
	if len(sets) != 1 || len(sets[0]) != k {
		t.Fatalf("possible sets = %v", sets)
	}
	if len(res.ForcedIn) != k || len(res.ForcedOut) != n-k {
		t.Fatalf("full identification failed: %+v", res)
	}
}

func TestNegativeResponseAndRepeatedQueries(t *testing.T) {
	const n, k = 5, 2
	r, _ := inference.New(names(n), k, 1<<20)
	index := map[string]int{}
	for i := 0; i < n; i++ {
		index[elementName(i)] = i
	}

	// Negative response on {e1,e3} excludes both members.
	res, err := r.Observe([]string{"e1", "e3"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := masksOf(res.ForcedOut, index); got != (uint32(1)<<1 | uint32(1)<<3) {
		t.Fatalf("negative response forced out %v", res.ForcedOut)
	}

	before := r.NodesVisited()
	res2, err := r.Observe([]string{"e1", "e3"}, 0)
	if err != nil || r.NodesVisited() != before {
		t.Fatalf("identical repeat should be a no-op: err=%v visited %d->%d", err, before, r.NodesVisited())
	}
	if len(res2.NewlyForcedIn) != 0 || len(res2.NewlyForcedOut) != 0 {
		t.Fatalf("repeat surfaced new elements: %+v", res2)
	}

	// Same subset, conflicting count is an inconsistency.
	if _, err := r.Observe([]string{"e3", "e1"}, 1); !errors.Is(err, inference.ErrInconsistent) {
		t.Fatalf("conflicting repeat: got %v", err)
	}
	if r.IsConsistent() {
		t.Fatal("reasoner should be marked inconsistent")
	}
}

func TestInconsistentAnswerSequence(t *testing.T) {
	const n, k = 4, 2
	r, _ := inference.New(names(n), k, 1<<20)
	r.Observe([]string{"e0", "e1"}, 2)           // forces e0, e1 in
	_, err := r.Observe([]string{"e0", "e2"}, 0) // forces e0 out
	if !errors.Is(err, inference.ErrInconsistent) {
		t.Fatalf("want ErrInconsistent, got %v", err)
	}
	// Every further observation is rejected.
	if _, err := r.Observe([]string{"e3"}, 0); !errors.Is(err, inference.ErrInconsistent) {
		t.Fatalf("post-death observe: got %v", err)
	}
}

func TestExactAgainstFullSubsetReference(t *testing.T) {
	const n, k = 8, 3
	candidates := names(n)
	index := map[string]int{}
	for i, name := range candidates {
		index[name] = i
	}
	rng := rand.New(rand.NewSource(445))
	secret := uint32(0)
	for bits.OnesCount32(secret) < k {
		secret |= uint32(1) << uint(rng.Intn(n))
	}

	r, err := inference.New(candidates, k, 1<<24)
	if err != nil {
		t.Fatal(err)
	}
	var obs []observation
	for step := 0; step < 30; step++ {
		query := uint32(rng.Intn(1 << n))
		count := intersectCount(secret, query)
		obs = append(obs, observation{query, count})

		res, err := r.Observe(subsetNames(candidates, query), count)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		reference := referenceFamilies(n, k, obs)
		if res.PossibleCount != len(reference) || !res.CountExact {
			t.Fatalf("step %d count = %d, reference %d", step, res.PossibleCount, len(reference))
		}
		gotSets := make(map[uint32]bool)
		for _, set := range r.PossibleSets() {
			gotSets[masksOf(set, index)] = true
		}
		if len(gotSets) != len(reference) {
			t.Fatalf("step %d possible-set size %d, reference %d", step, len(gotSets), len(reference))
		}
		for _, ref := range reference {
			if !gotSets[ref] {
				t.Fatalf("step %d missing compatible set %b", step, ref)
			}
		}
		var refIn, refOut uint32
		if len(reference) > 0 {
			refIn, refOut = reference[0], ^reference[0]
			for _, m := range reference[1:] {
				refIn &= m
				refOut &= ^m
			}
			refOut &= uint32(1)<<n - 1
		}
		if got := masksOf(res.ForcedIn, index); got != refIn {
			t.Fatalf("step %d forced in %v, want mask %b", step, res.ForcedIn, refIn)
		}
		if got := masksOf(res.ForcedOut, index); got != refOut {
			t.Fatalf("step %d forced out %v, want mask %b", step, res.ForcedOut, refOut)
		}
	}
}

// TestBoundedModeSoundness forces ModeBounded with a tiny budget and
// checks every claim against the full-subset reference: bounded-mode
// forced sets must be subsets of the reference forced sets, and bounds
// must stay valid on every observation.
func TestBoundedModeSoundness(t *testing.T) {
	const n, k = 12, 4
	candidates := names(n)
	index := map[string]int{}
	for i, name := range candidates {
		index[name] = i
	}
	rng := rand.New(rand.NewSource(446))
	secret := uint32(0)
	for bits.OnesCount32(secret) < k {
		secret |= uint32(1) << uint(rng.Intn(n))
	}

	r, err := inference.New(candidates, k, 64)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeBounded {
		t.Fatalf("expected bounded mode, got %v", r.Mode())
	}

	var obs []observation
	for step := 0; step < 40; step++ {
		query := uint32(rng.Intn(1 << n))
		count := intersectCount(secret, query)
		obs = append(obs, observation{query, count})

		res, err := r.Observe(subsetNames(candidates, query), count)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if !res.Consistent {
			t.Fatalf("step %d unexpectedly inconsistent", step)
		}
		reference := referenceFamilies(n, k, obs)
		var refIn, refOut uint32
		refIn = reference[0]
		refOut = ^reference[0]
		for _, m := range reference[1:] {
			refIn &= m
			refOut &= ^m
		}
		refOut &= uint32(1)<<n - 1

		gotIn := masksOf(res.ForcedIn, index)
		gotOut := masksOf(res.ForcedOut, index)
		if gotIn&^refIn != 0 {
			t.Fatalf("step %d unsound forced-in %b (reference %b)", step, gotIn, refIn)
		}
		if gotOut&^refOut != 0 {
			t.Fatalf("step %d unsound forced-out %b (reference %b)", step, gotOut, refOut)
		}
		// Newly reported elements must always be members of the
		// accumulated forced sets.
		if masksOf(res.NewlyForcedIn, index)&^gotIn != 0 {
			t.Fatalf("step %d newly forced in not contained", step)
		}
		if masksOf(res.NewlyForcedOut, index)&^gotOut != 0 {
			t.Fatalf("step %d newly forced out not contained", step)
		}
		for name, lo := range res.LowerBound {
			up := res.UpperBound[name]
			if lo > up || lo < 0 || up > 1 {
				t.Fatalf("step %d invalid bounds for %s: [%d,%d]", step, name, lo, up)
			}
		}
	}
}

// TestBoundedDetectsInconsistency uses constraints whose unsatisfiability
// is simple enough for a small bounded search to prove.
func TestBoundedDetectsInconsistency(t *testing.T) {
	const n, k = 10, 3
	// Below C(10,3)=120 so construction stays in bounded mode, yet large
	// enough for the bounded searches to prove each forced singleton.
	r, err := inference.New(names(n), k, 40)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeBounded {
		t.Fatalf("expected bounded mode, got %v", r.Mode())
	}
	// Four disjoint singletons each claimed present, but k == 3.
	r.Observe([]string{"e0"}, 1)
	r.Observe([]string{"e2"}, 1)
	r.Observe([]string{"e4"}, 1)
	_, err = r.Observe([]string{"e6"}, 1)
	if !errors.Is(err, inference.ErrInconsistent) {
		t.Fatalf("want ErrInconsistent, got %v", err)
	}
	if r.IsConsistent() {
		t.Fatal("IsConsistent mismatch")
	}
}

func TestMaxElementsAndCardinalityEdges(t *testing.T) {
	const n = inference.MaxElements

	// Full universe fits ModeExact only with a generous budget; the
	// reasoner must stay sound either way at the size limit.
	r, err := inference.New(names(n), n/2, 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeExact {
		t.Fatalf("expected exact mode at n=%d, got %v", n, r.Mode())
	}

	// k == 0: every element is forced out from construction.
	empty, err := inference.New(names(n), 0, inference.DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	res, err := empty.Observe(names(n), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ForcedOut) != n || len(res.ForcedIn) != 0 {
		t.Fatalf("k=0 bounds wrong: in=%v out=%d", res.ForcedIn, len(res.ForcedOut))
	}

	// k == n: every element is forced in.
	full, err := inference.New(names(n), n, inference.DefaultBudget)
	if err != nil {
		t.Fatal(err)
	}
	res, err = full.Observe(names(n), n)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ForcedIn) != n || len(res.ForcedOut) != 0 {
		t.Fatalf("k=n bounds wrong: in=%d out=%v", len(res.ForcedIn), res.ForcedOut)
	}

	// k == 0 rejecting any positive answer.
	_, err = empty.Observe([]string{"e1"}, 1)
	if !errors.Is(err, inference.ErrBadResponse) {
		t.Fatalf("k=0 positive answer: got %v", err)
	}
}

// TestBoundedAtMaxElements exercises the 24-element bounded path,
// including a negative response that must exclude its whole subset.
func TestBoundedAtMaxElements(t *testing.T) {
	const n = inference.MaxElements
	r, err := inference.New(names(n), 5, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeBounded {
		t.Fatalf("expected bounded mode, got %v", r.Mode())
	}
	res, err := r.Observe([]string{"e7", "e11", "e23"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"e7", "e11", "e23"} {
		found := false
		for _, got := range res.ForcedOut {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("negative response did not force out %q: %v", want, res.ForcedOut)
		}
	}

	lower, upper := r.Bounds()
	for name, lo := range lower {
		if lo > upper[name] {
			t.Fatalf("invalid bounds for %s", name)
		}
	}
	if count, exact := r.PossibleCount(); exact || count != -1 {
		t.Fatalf("bounded count = (%d,%v)", count, exact)
	}
	if r.PossibleSets() != nil {
		t.Fatal("bounded mode must not expose fabricated sets")
	}
}

// TestBoundedRandomizedDifferential runs many random answer streams under
// several tight budgets and asserts bounded-mode claims never exceed the
// full-subset reference (soundness), while exact mode matches it exactly.
func TestBoundedRandomizedDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(447))
	for trial := 0; trial < 40; trial++ {
		n := 6 + rng.Intn(7) // 6..12
		k := 1 + rng.Intn(n)
		candidates := names(n)
		index := map[string]int{}
		for i, name := range candidates {
			index[name] = i
		}
		secret := uint32(0)
		for bits.OnesCount32(secret) < k {
			secret |= uint32(1) << uint(rng.Intn(n))
		}
		budget := uint64(8 + rng.Intn(200))
		r, err := inference.New(candidates, k, budget)
		if err != nil {
			t.Fatal(err)
		}

		var obs []observation
		for step := 0; step < 15; step++ {
			query := uint32(rng.Intn(1 << n))
			count := intersectCount(secret, query)
			obs = append(obs, observation{query, count})
			res, err := r.Observe(subsetNames(candidates, query), count)
			if err != nil {
				t.Fatalf("trial %d step %d: %v", trial, step, err)
			}
			reference := referenceFamilies(n, k, obs)
			var refIn, refOut uint32
			refIn = reference[0]
			refOut = ^reference[0]
			for _, m := range reference[1:] {
				refIn &= m
				refOut &= ^m
			}
			refOut &= uint32(1)<<n - 1

			gotIn := masksOf(res.ForcedIn, index)
			gotOut := masksOf(res.ForcedOut, index)
			if gotIn&^refIn != 0 || gotOut&^refOut != 0 {
				t.Fatalf("trial %d step %d unsound: in=%b/%b out=%b/%b",
					trial, step, gotIn, refIn, gotOut, refOut)
			}
			if gotIn&gotOut != 0 {
				t.Fatalf("trial %d step %d element both in and out", trial, step)
			}
			if r.Mode() == inference.ModeExact && res.PossibleCount != len(reference) {
				t.Fatalf("trial %d exact count %d != reference %d",
					trial, res.PossibleCount, len(reference))
			}
		}
	}
}

// TestBudgetExhaustionKeepsUnknown verifies that a too-small budget
// yields unknowns rather than fabricated forced classifications.
func TestBudgetExhaustionKeepsUnknown(t *testing.T) {
	const n, k = 12, 5
	r, err := inference.New(names(n), k, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != inference.ModeBounded {
		t.Fatalf("expected bounded mode, got %v", r.Mode())
	}
	res, err := r.Observe(names(n)[:6], 3)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Consistent {
		t.Fatal("stream should remain consistent")
	}
	// With only two candidate checks per search, nothing is provable.
	if len(res.ForcedIn) != 0 || len(res.ForcedOut) != 0 {
		t.Fatalf("budget of 2 must not prove membership: %+v", res)
	}
	if len(res.Unknown) != n {
		t.Fatalf("unknown = %d, want %d", len(res.Unknown), n)
	}
	for name := range res.LowerBound {
		if res.LowerBound[name] != 0 || res.UpperBound[name] != 1 {
			t.Fatalf("bounds for %s not [0,1]", name)
		}
	}
}
