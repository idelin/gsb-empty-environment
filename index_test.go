package indexvis

import (
	"errors"
	"math/rand"
	"sort"
	"testing"
)

func snapshot(upper TxID, known map[TxID]Status, active ...TxID) Snapshot {
	s := Snapshot{UpperBound: upper, Known: known}
	if len(active) > 0 {
		s.Active = make(map[TxID]struct{}, len(active))
		for _, id := range active {
			s.Active[id] = struct{}{}
		}
	}
	return s
}

func mustSummary(t *testing.T, entries ...PairCount) *PageSummary {
	t.Helper()
	p := NewPageSummary()
	for _, e := range entries {
		if err := p.Add(e.Pair.Create, e.Pair.Delete, e.Count); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func visibleCountBrute(snap Snapshot, page *PageSummary, final map[TxID]Status) int64 {
	var total int64
	for _, pc := range page.Pairs() {
		if !snap.Eligible(pc.Pair.Create) || final[pc.Pair.Create] != Committed {
			continue
		}
		d := pc.Pair.Delete
		if d != NoTransaction && snap.Eligible(d) && final[d] == Committed {
			continue
		}
		total += pc.Count
	}
	return total
}

func bruteBounds(t *testing.T, snap Snapshot, deps []Edge, page *PageSummary) (min, max int64, feasible bool) {
	t.Helper()
	universe, err := collectUniverse(snap, deps, page)
	if err != nil {
		t.Fatal(err)
	}
	var free []TxID
	fixed := make(map[TxID]Status)
	for _, id := range universe {
		if st := snap.InitialStatus(id); st == Unknown {
			free = append(free, id)
		} else {
			fixed[id] = st
		}
	}
	if len(free) > 20 {
		t.Fatalf("brute force would enumerate %d transactions", len(free))
	}
	first := true
	for mask := 0; mask < 1<<uint(len(free)); mask++ {
		final := make(map[TxID]Status, len(universe))
		for id, st := range fixed {
			final[id] = st
		}
		for i, id := range free {
			if mask&(1<<uint(i)) != 0 {
				final[id] = Committed
			} else {
				final[id] = Aborted
			}
		}
		legal := true
		for _, e := range deps {
			if final[e.From] == Committed && final[e.To] != Committed {
				legal = false
				break
			}
		}
		if !legal {
			continue
		}
		count := visibleCountBrute(snap, page, final)
		if first {
			min, max, feasible = count, count, true
			first = false
		} else {
			if count < min {
				min = count
			}
			if count > max {
				max = count
			}
		}
	}
	return min, max, feasible
}

func checkWitness(t *testing.T, ix *Index, w Witness, want int64) {
	t.Helper()
	got, err := ix.Replay(w)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got != want || w.VisibleVersions != want {
		t.Fatalf("witness count = %d / %d, want %d", got, w.VisibleVersions, want)
	}
	ind := visibleCountBrute(ix.Snapshot(), ix.Summary(), w.Final)
	if ind != want {
		t.Fatalf("independent witness recount = %d, want %d", ind, want)
	}
}

func TestBasicVisibilityAndBounds(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{1: Committed, 2: Aborted})
	page := mustSummary(t,
		PairCount{Pair{1, NoTransaction}, 2},
		PairCount{Pair{2, NoTransaction}, 5},
		PairCount{Pair{1, 2}, 3},
	)
	ix, err := NewIndex(snap, nil, page)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 5 || res.Max != 5 {
		t.Fatalf("bounds = %d..%d, want 5..5", res.Min, res.Max)
	}
	checkWitness(t, ix, res.MinWitness, 5)
	checkWitness(t, ix, res.MaxWitness, 5)
}

func TestUnknownBoundsDiffer(t *testing.T) {
	page := mustSummary(t, PairCount{Pair{7, NoTransaction}, 4})
	ix, err := NewIndex(snapshot(10, nil), nil, page)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 0 || res.Max != 4 {
		t.Fatalf("unknown bounds = %d..%d, want 0..4", res.Min, res.Max)
	}
	checkWitness(t, ix, res.MinWitness, 0)
	checkWitness(t, ix, res.MaxWitness, 4)
	if res.MinWitness.Final[7] != Aborted || res.MaxWitness.Final[7] != Committed {
		t.Fatalf("bounds did not choose the expected witnesses: min=%v max=%v",
			res.MinWitness.Final, res.MaxWitness.Final)
	}
}

func TestUnknownSharedAcrossRows(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{1: Committed})
	page := mustSummary(t,
		PairCount{Pair{2, NoTransaction}, 1},
		PairCount{Pair{1, 2}, 1},
	)
	ix, err := NewIndex(snap, nil, page)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 1 || res.Max != 1 {
		t.Fatalf("joint bounds = %d..%d, want 1..1 (row-wise bounds would be 0..2)", res.Min, res.Max)
	}
	checkWitness(t, ix, res.MinWitness, 1)
	checkWitness(t, ix, res.MaxWitness, 1)
}

func TestSameTransactionAcrossTwoSubtrees(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{1: Committed})
	page := mustSummary(t,
		PairCount{Pair{2, NoTransaction}, 1},
		PairCount{Pair{1, 2}, 1},
	)
	td := &TreeDecomposition{
		Bags:     [][]TxID{{2}, {1, 2}},
		Parent:   []int{-1, 0},
		Children: [][]int{{1}, {}},
		Root:     0,
	}
	ix, err := NewIndexWithDecomposition(snap, nil, page, td)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 1 || res.Max != 1 {
		t.Fatalf("bounds = %d..%d, want 1..1", res.Min, res.Max)
	}
	// Transaction 2 is owned by bag 0 and also participates in bag 1, so it
	// really does occur on both sides of the decomposition edge.
	if got := ix.ownerBag[2]; got != 0 {
		t.Fatalf("owner bag = %d, want 0", got)
	}
	checkWitness(t, ix, res.MinWitness, 1)
	checkWitness(t, ix, res.MaxWitness, 1)
}

func TestSameTransactionCancelsAcrossTwoSubtrees(t *testing.T) {
	// X=2 occurs in the root and in both child subtrees. One version is
	// visible exactly when X commits, the other exactly when X aborts, so the
	// joint count is 1 even though each row's individual bound is 0..1.
	snap := snapshot(10, map[TxID]Status{1: Committed, 3: Aborted})
	page := mustSummary(t,
		PairCount{Pair{2, 3}, 1},
		PairCount{Pair{1, 2}, 1},
	)
	td := &TreeDecomposition{
		Bags:     [][]TxID{{2}, {2, 3}, {1, 2}},
		Parent:   []int{-1, 0, 0},
		Children: [][]int{{1, 2}, {}, {}},
		Root:     0,
	}
	ix, err := NewIndexWithDecomposition(snap, nil, page, td)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 1 || res.Max != 1 {
		t.Fatalf("branch cancellation bounds = %d..%d, want 1..1", res.Min, res.Max)
	}
	checkWitness(t, ix, res.MinWitness, 1)
	checkWitness(t, ix, res.MaxWitness, 1)

	// X's domain owner is the root bag, while both version pairs live in child
	// bags. Fixing X must update only the root path, and the DP must still be
	// exact because the owner constraint propagates through bag intersections.
	proof, err := ix.SetStatus(2, Committed)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.AffectedBags) != 1 || proof.AffectedBags[0] != 0 {
		t.Fatalf("X should be owned by the root bag: %+v", proof)
	}
	if !proof.CheckOnlyRootPaths(ix.Decomposition()) {
		t.Fatalf("status update escaped the root path: %+v", proof)
	}
	fixed, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Min != 1 || fixed.Max != 1 {
		t.Fatalf("fixed-X bounds = %d..%d, want 1..1", fixed.Min, fixed.Max)
	}
	checkWitness(t, ix, fixed.MinWitness, 1)
}

func TestSnapshotCrossesBothEndsOfDependency(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{1: Committed})
	deps := []Edge{{From: 1, To: 20}, {From: 20, To: 2}}
	page := mustSummary(t,
		PairCount{Pair{20, NoTransaction}, 5},
		PairCount{Pair{2, NoTransaction}, 7},
		PairCount{Pair{1, 20}, 3},
	)
	ix, err := NewIndex(snap, deps, page)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	// 1 commits, hence 20 commits, hence 2 commits. 20 is after the snapshot,
	// so its own versions are invisible; 2's versions are visible.
	if res.Min != 10 || res.Max != 10 {
		t.Fatalf("bounds = %d..%d, want 10..10", res.Min, res.Max)
	}
	for _, w := range []Witness{res.MinWitness, res.MaxWitness} {
		if w.Final[20] != Committed || w.Final[2] != Committed {
			t.Fatalf("witness did not propagate commits: %v", w.Final)
		}
		checkWitness(t, ix, w, 10)
	}
}

func TestDependencyCycleSpanningBags(t *testing.T) {
	n := TxID(6)
	var deps []Edge
	for i := TxID(0); i < n; i++ {
		deps = append(deps, Edge{From: i, To: (i + 1) % n})
	}
	page := mustSummary(t, PairCount{Pair{0, NoTransaction}, 1})
	snap := snapshot(10, nil)
	ix, err := NewIndex(snap, deps, page)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 0 || res.Max != 1 {
		t.Fatalf("cycle bounds = %d..%d, want 0..1", res.Min, res.Max)
	}
	checkWitness(t, ix, res.MinWitness, 0)
	checkWitness(t, ix, res.MaxWitness, 1)
	for _, w := range []Witness{res.MinWitness, res.MaxWitness} {
		all := w.Final[0]
		for i := TxID(1); i < n; i++ {
			if w.Final[i] != all {
				t.Fatalf("cycle witness is not uniform: %v", w.Final)
			}
		}
	}
	if len(ix.Decomposition().Bags) <= 2 {
		t.Fatalf("cycle should span multiple bags")
	}

	// A fixed abort anywhere forces the whole cycle to abort.
	proof, err := ix.SetStatus(3, Aborted)
	if err != nil {
		t.Fatal(err)
	}
	if !proof.CheckOnlyRootPaths(ix.Decomposition()) {
		t.Fatalf("cycle update touched a bag outside the root path")
	}
	res, err = ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 0 || res.Max != 0 {
		t.Fatalf("aborted cycle bounds = %d..%d, want 0..0", res.Min, res.Max)
	}
}

func TestContradictionRollback(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{1: Aborted})
	deps := []Edge{{From: 0, To: 1}}
	page := mustSummary(t, PairCount{Pair{0, NoTransaction}, 1})
	ix, err := NewIndex(snap, deps, page)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if before.Min != 0 || before.Max != 0 {
		t.Fatalf("initial bounds = %d..%d, want 0..0", before.Min, before.Max)
	}

	proof, err := ix.SetStatus(0, Committed)
	var ce *ContradictionError
	if !errors.As(err, &ce) || !errors.Is(err, ErrContradiction) {
		t.Fatalf("SetStatus error = %v, want ContradictionError", err)
	}
	if !proof.RolledBack || !proof.CheckOnlyRootPaths(ix.Decomposition()) {
		t.Fatalf("contradictory update was not rolled back along root paths: %+v", proof)
	}
	if ce.From != 0 || ce.To != 1 || len(ce.Path) < 2 {
		t.Fatalf("bad contradiction path: %+v", ce)
	}
	after, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if after.Min != before.Min || after.Max != before.Max {
		t.Fatalf("bounds changed after rollback: %d..%d -> %d..%d", before.Min, before.Max, after.Min, after.Max)
	}
	if _, err := ix.SetStatus(0, Aborted); err != nil {
		t.Fatalf("abort after rollback: %v", err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	checkWitness(t, ix, res.MinWitness, 0)
}

func TestInitialKnownContradiction(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{0: Committed, 1: Aborted})
	deps := []Edge{{From: 0, To: 1}}
	_, err := NewIndex(snap, deps, NewPageSummary())
	var ce *ContradictionError
	if !errors.As(err, &ce) {
		t.Fatalf("NewIndex error = %v, want contradiction", err)
	}
}

func TestCycleContradictionRollbackAcrossBags(t *testing.T) {
	n := TxID(6)
	var deps []Edge
	for i := TxID(0); i < n; i++ {
		deps = append(deps, Edge{From: i, To: (i + 1) % n})
	}
	page := mustSummary(t, PairCount{Pair{0, NoTransaction}, 2})
	ix, err := NewIndex(snapshot(10, map[TxID]Status{3: Aborted}), deps, page)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if before.Min != 0 || before.Max != 0 {
		t.Fatalf("fixed abort in cycle: bounds = %d..%d, want 0..0", before.Min, before.Max)
	}
	proof, err := ix.SetStatus(0, Committed)
	var ce *ContradictionError
	if !errors.As(err, &ce) {
		t.Fatalf("expected contradiction, got %v", err)
	}
	if len(ce.Path) < 4 {
		t.Fatalf("cycle contradiction path should span several bags: %v", ce.Path)
	}
	if !proof.RolledBack || !proof.CheckOnlyRootPaths(ix.Decomposition()) {
		t.Fatalf("cycle rollback proof is invalid: %+v", proof)
	}
	after, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if after.Min != 0 || after.Max != 0 {
		t.Fatalf("cycle rollback changed bounds to %d..%d", after.Min, after.Max)
	}
}

func TestInitialCycleContradiction(t *testing.T) {
	n := TxID(6)
	var deps []Edge
	for i := TxID(0); i < n; i++ {
		deps = append(deps, Edge{From: i, To: (i + 1) % n})
	}
	_, err := NewIndex(snapshot(10, map[TxID]Status{0: Committed, 3: Aborted}), deps, mustSummary(t,
		PairCount{Pair{0, NoTransaction}, 1},
	))
	var ce *ContradictionError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want cycle contradiction", err)
	}
	if len(ce.Path) < 4 {
		t.Fatalf("cycle contradiction should span bags: %v", ce.Path)
	}
}

func TestEmptyPage(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{1: Committed})
	ix, err := NewIndex(snap, []Edge{{From: 1, To: 2}}, NewPageSummary())
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 0 || res.Max != 0 {
		t.Fatalf("empty-page bounds = %d..%d, want 0..0", res.Min, res.Max)
	}
	if len(res.MinWitness.Final) == 0 || len(res.MaxWitness.Final) == 0 {
		t.Fatalf("empty page should still have a completion witness")
	}
	checkWitness(t, ix, res.MinWitness, 0)
	checkWitness(t, ix, res.MaxWitness, 0)
}

func TestSummarySplitMerge(t *testing.T) {
	a := NewPageSummary()
	if err := a.Add(1, NoTransaction, 2); err != nil {
		t.Fatal(err)
	}
	if err := a.Add(1, NoTransaction, 3); err != nil {
		t.Fatal(err)
	}
	if err := a.Add(1, 2, 4); err != nil {
		t.Fatal(err)
	}
	if got := a.Count(1, NoTransaction); got != 5 {
		t.Fatalf("compressed multiplicity = %d, want 5", got)
	}
	if len(a.Pairs()) != 2 {
		t.Fatalf("summary should have two transaction pairs")
	}
	left, right := a.Split(func(p Pair) bool { return p.Delete == NoTransaction })
	merged, err := MergePageSummaries(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if merged.TotalVersions() != a.TotalVersions() || len(merged.Pairs()) != len(a.Pairs()) {
		t.Fatalf("split/merge lost multiplicity: %v vs %v", merged.Pairs(), a.Pairs())
	}
}

func TestMultiPageUnknownsShared(t *testing.T) {
	snap := snapshot(10, map[TxID]Status{1: Committed})
	pageA := mustSummary(t, PairCount{Pair{2, NoTransaction}, 1})
	pageB := mustSummary(t, PairCount{Pair{1, 2}, 1})

	ixA, err := NewIndex(snap, nil, pageA)
	if err != nil {
		t.Fatal(err)
	}
	ixB, err := NewIndex(snap, nil, pageB)
	if err != nil {
		t.Fatal(err)
	}
	resA, _ := ixA.Evaluate()
	resB, _ := ixB.Evaluate()
	if resA.Min+resB.Min == 1 && resA.Max+resB.Max == 1 {
		t.Fatalf("test setup is not discriminating")
	}

	merged, err := MergePageSummaries(pageA, pageB)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := NewIndex(snap, nil, merged)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 1 || res.Max != 1 {
		t.Fatalf("merged-page bounds = %d..%d, want 1..1", res.Min, res.Max)
	}
}

func TestIncrementalUpdateOnlyRootPath(t *testing.T) {
	const n = 12
	var deps []Edge
	for i := TxID(0); i+1 < n; i++ {
		deps = append(deps, Edge{From: i, To: i + 1})
	}
	page := mustSummary(t,
		PairCount{Pair{0, NoTransaction}, 1},
		PairCount{Pair{n - 1, NoTransaction}, 1},
	)
	ix, err := NewIndex(snapshot(100, nil), deps, page)
	if err != nil {
		t.Fatal(err)
	}
	td := ix.Decomposition()

	proof, err := ix.SetStatus(0, Committed)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.AffectedBags) != 1 || len(proof.UpdatedBags) <= 1 {
		t.Fatalf("expected a non-trivial root path, got %+v", proof)
	}
	if !proof.CheckOnlyRootPaths(td) {
		t.Fatalf("updated bags are not exactly the affected bag's root path")
	}
	wantPath := td.PathToRoot(proof.AffectedBags[0])
	if len(wantPath) != len(proof.UpdatedBags) {
		t.Fatalf("path length = %d, updated = %d", len(wantPath), len(proof.UpdatedBags))
	}
	for i := range wantPath {
		if wantPath[i] != proof.UpdatedBags[i] {
			t.Fatalf("updated path = %v, want %v", proof.UpdatedBags, wantPath)
		}
	}
	if proof.TableEntries <= 0 || proof.AuxEntries <= 0 {
		t.Fatalf("missing counting evidence: %+v", proof)
	}
	if proof.UntouchedBags != proof.TotalBags-len(proof.UpdatedBags) {
		t.Fatalf("untouched/total counting evidence is inconsistent: %+v", proof)
	}
	sumEntries := 0
	for _, bc := range proof.Bags {
		sumEntries += bc.TableEntries
	}
	if sumEntries != proof.TableEntries {
		t.Fatalf("per-bag table counts %d != total %d", sumEntries, proof.TableEntries)
	}

	allCommit, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if allCommit.Min != 2 || allCommit.Max != 2 {
		t.Fatalf("path commit bounds = %d..%d, want 2..2", allCommit.Min, allCommit.Max)
	}

	verProof, err := ix.AddVersions(n-1, NoTransaction, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !verProof.CheckOnlyRootPaths(td) {
		t.Fatalf("multiplicity update escaped the root path: %+v", verProof)
	}
	after, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if after.Min != 5 || after.Max != 5 {
		t.Fatalf("after multiplicity update bounds = %d..%d, want 5..5", after.Min, after.Max)
	}

	// Repeated completion updates keep working on the same decomposition.
	if _, err := ix.SetStatus(n-1, Aborted); err == nil {
		t.Fatalf("expected contradiction because 0 commits down the chain")
	}
	last, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if last.Min != 5 || last.Max != 5 {
		t.Fatalf("rollback changed bounds: %d..%d", last.Min, last.Max)
	}
}

func TestIncrementalMatchesBruteForce(t *testing.T) {
	const n = 7
	var deps []Edge
	page := NewPageSummary()
	for i := TxID(0); i < n; i++ {
		if i+1 < n {
			deps = append(deps, Edge{From: i, To: i + 1})
		}
		if err := page.Add(i, NoTransaction, 1); err != nil {
			t.Fatal(err)
		}
	}
	upper := TxID(100)
	ix, err := NewIndex(snapshot(upper, nil), deps, page)
	if err != nil {
		t.Fatal(err)
	}
	known := make(map[TxID]Status)
	wantMin, wantMax, feasible := bruteBounds(t, snapshot(upper, known), deps, page)
	if !feasible {
		t.Fatal("initial instance should be feasible")
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != wantMin || res.Max != wantMax {
		t.Fatalf("initial bounds = %d..%d, brute = %d..%d", res.Min, res.Max, wantMin, wantMax)
	}

	ops := []struct {
		tx TxID
		st Status
	}{
		{3, Committed},
		{6, Aborted},
		{1, Committed},
		{4, Aborted},
		{0, Aborted},
		{5, Committed},
		{2, Aborted},
		{3, Unknown},
		{6, Committed},
		{1, Unknown},
	}
	for step, op := range ops {
		candidate := cloneKnown(known)
		if op.st == Unknown {
			delete(candidate, op.tx)
		} else {
			candidate[op.tx] = op.st
		}
		cMin, cMax, cFeasible := bruteBounds(t, snapshot(upper, candidate), deps, page)
		proof, setErr := ix.SetStatus(op.tx, op.st)
		if !proof.CheckOnlyRootPaths(ix.Decomposition()) {
			t.Fatalf("step %d: update proof escaped root paths", step)
		}
		after, err := ix.Evaluate()
		if err != nil {
			t.Fatalf("step %d: Evaluate: %v", step, err)
		}
		if !cFeasible {
			if setErr == nil {
				t.Fatalf("step %d: expected contradiction", step)
			}
			oldMin, oldMax, _ := bruteBounds(t, snapshot(upper, known), deps, page)
			if after.Min != oldMin || after.Max != oldMax {
				t.Fatalf("step %d: rollback bounds = %d..%d, want %d..%d",
					step, after.Min, after.Max, oldMin, oldMax)
			}
			continue
		}
		if setErr != nil {
			t.Fatalf("step %d: SetStatus: %v", step, setErr)
		}
		known = candidate
		if after.Min != cMin || after.Max != cMax {
			t.Fatalf("step %d: bounds = %d..%d, brute = %d..%d (known=%v)",
				step, after.Min, after.Max, cMin, cMax, known)
		}
		checkWitness(t, ix, after.MinWitness, cMin)
		checkWitness(t, ix, after.MaxWitness, cMax)
	}
}

func TestRandomSmallExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261121))
	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(9)
		upper := TxID(rng.Intn(n + 1))
		known := make(map[TxID]Status)
		var active []TxID
		for i := 0; i < n; i++ {
			id := TxID(i)
			switch rng.Intn(4) {
			case 0:
				known[id] = Committed
			case 1:
				known[id] = Aborted
			case 2:
				active = append(active, id)
			}
		}
		snap := snapshot(upper, known, active...)

		var deps []Edge
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i != j && rng.Intn(5) == 0 {
					deps = append(deps, Edge{From: TxID(i), To: TxID(j)})
				}
			}
		}

		page := NewPageSummary()
		for i := 0; i < n; i++ {
			c := TxID(i)
			if rng.Intn(3) == 0 {
				if err := page.Add(c, NoTransaction, int64(1+rng.Intn(3))); err != nil {
					t.Fatal(err)
				}
			}
			if rng.Intn(3) == 0 {
				d := TxID(rng.Intn(n))
				if err := page.Add(c, d, int64(1+rng.Intn(3))); err != nil {
					t.Fatal(err)
				}
			}
		}

		wantMin, wantMax, feasible := bruteBounds(t, snap, deps, page)
		ix, err := NewIndex(snap, deps, page)
		if !feasible {
			var ce *ContradictionError
			if !errors.As(err, &ce) {
				t.Fatalf("iter %d: infeasible input returned %v", iter, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: NewIndex: %v (input n=%d upper=%d known=%v active=%v deps=%v page=%v)",
				iter, err, n, upper, known, active, deps, page.Pairs())
		}
		if err := ix.Decomposition().Validate(); err != nil {
			t.Fatalf("iter %d: decomposition: %v", iter, err)
		}
		if err := ix.Validate(); err != nil {
			t.Fatalf("iter %d: index validation: %v", iter, err)
		}
		for _, bag := range ix.Decomposition().Bags {
			if len(bag) > MaxBagSize {
				t.Fatalf("iter %d: bag too large: %v", iter, bag)
			}
		}
		res, err := ix.Evaluate()
		if err != nil {
			t.Fatalf("iter %d: Evaluate: %v", iter, err)
		}
		if res.Min != wantMin || res.Max != wantMax {
			t.Fatalf("iter %d: bounds = %d..%d, brute = %d..%d (deps=%v page=%v known=%v upper=%d active=%v)",
				iter, res.Min, res.Max, wantMin, wantMax, deps, page.Pairs(), known, upper, active)
		}
		checkWitness(t, ix, res.MinWitness, wantMin)
		checkWitness(t, ix, res.MaxWitness, wantMax)

		// Exhaustively computed bounds must also survive incremental replay.
		for _, id := range ix.Universe() {
			if ix.status[id] != Unknown {
				continue
			}
			modified := snapshot(upper, cloneKnown(known), active...)
			modified.Known[id] = Committed
			mMin, mMax, mFeasible := bruteBounds(t, modified, deps, page)
			_, setErr := ix.SetStatus(id, Committed)
			if !mFeasible {
				if setErr == nil {
					t.Fatalf("iter %d: update should have contradicted", iter)
				}
				continue
			}
			if setErr != nil {
				t.Fatalf("iter %d: feasible status update failed: %v", iter, setErr)
			}
			after, err := ix.Evaluate()
			if err != nil {
				t.Fatal(err)
			}
			if after.Min != mMin || after.Max != mMax {
				t.Fatalf("iter %d: incremental bounds = %d..%d, brute = %d..%d", iter, after.Min, after.Max, mMin, mMax)
			}
			break
		}
	}
}

func cloneKnown(in map[TxID]Status) map[TxID]Status {
	out := make(map[TxID]Status, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestRejectsOversizedBag(t *testing.T) {
	bag := make([]TxID, MaxBagSize+1)
	for i := range bag {
		bag[i] = TxID(i + 1)
	}
	td := &TreeDecomposition{
		Bags:     [][]TxID{bag},
		Parent:   []int{-1},
		Children: [][]int{{}},
		Root:     0,
	}
	_, err := NewIndexWithDecomposition(snapshot(100, nil), nil, NewPageSummary(), td)
	if !errors.Is(err, ErrBagTooLarge) {
		t.Fatalf("error = %v, want ErrBagTooLarge", err)
	}
}

func TestRejectsGraphWiderThanLimit(t *testing.T) {
	const n = 12
	var deps []Edge
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			deps = append(deps, Edge{From: TxID(i), To: TxID(j)})
		}
	}
	_, err := NewIndex(snapshot(100, nil), deps, NewPageSummary())
	if !errors.Is(err, ErrTreewidth) {
		t.Fatalf("error = %v, want ErrTreewidth", err)
	}
}

func TestEmptyUniverse(t *testing.T) {
	ix, err := NewIndex(Snapshot{}, nil, NewPageSummary())
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 0 || res.Max != 0 {
		t.Fatalf("empty universe bounds = %d..%d", res.Min, res.Max)
	}
}

func TestDependencyCoverage(t *testing.T) {
	deps := []Edge{{From: 1, To: 2}, {From: 2, To: 3}, {From: 3, To: 1}}
	page := mustSummary(t, PairCount{Pair{1, 3}, 2})
	ix, err := NewIndex(snapshot(10, nil), deps, page)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Validate(); err != nil {
		t.Fatal(err)
	}
	bags := ix.Decomposition().Bags
	covered := func(a, b TxID) bool {
		for _, bag := range bags {
			if bagContains(bag, a) && bagContains(bag, b) {
				return true
			}
		}
		return false
	}
	for _, e := range deps {
		if !covered(e.From, e.To) {
			t.Fatalf("dependency %v not covered", e)
		}
	}
	for _, pc := range page.Pairs() {
		if pc.Pair.Delete != NoTransaction && !covered(pc.Pair.Create, pc.Pair.Delete) {
			t.Fatalf("pair %v not covered", pc.Pair)
		}
	}

	// Sorted bag contents are part of the deterministic representation.
	for _, bag := range bags {
		if !sort.SliceIsSorted(bag, func(i, j int) bool { return bag[i] < bag[j] }) {
			t.Fatalf("bag is not sorted: %v", bag)
		}
	}
}

func TestLargeChain4096(t *testing.T) {
	const n = 4096
	deps := make([]Edge, 0, n-1)
	for i := 0; i+1 < n; i++ {
		deps = append(deps, Edge{From: TxID(i), To: TxID(i + 1)})
	}
	page := mustSummary(t, PairCount{Pair{0, NoTransaction}, 1})
	ix, err := NewIndex(snapshot(n, nil), deps, page)
	if err != nil {
		t.Fatal(err)
	}
	td := ix.Decomposition()
	if len(td.Bags) != n+1 {
		t.Fatalf("bags = %d, want %d", len(td.Bags), n+1)
	}
	for _, bag := range td.Bags {
		if len(bag) > MaxBagSize {
			t.Fatalf("oversized bag: %v", bag)
		}
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 0 || res.Max != 1 {
		t.Fatalf("large-chain bounds = %d..%d, want 0..1", res.Min, res.Max)
	}
}

func TestTreewidthTenBags(t *testing.T) {
	const n = 160
	const clique = 11
	var deps []Edge
	for i := 0; i < clique; i++ {
		for j := i + 1; j < clique; j++ {
			deps = append(deps, Edge{From: TxID(i), To: TxID(j)})
		}
	}
	for v := clique; v < n; v++ {
		for u := v - 10; u < v; u++ {
			deps = append(deps, Edge{From: TxID(u), To: TxID(v)})
		}
	}
	page := mustSummary(t, PairCount{Pair{0, NoTransaction}, 1})
	ix, err := NewIndex(snapshot(n+10, nil), deps, page)
	if err != nil {
		t.Fatal(err)
	}
	maxBag := 0
	for _, bag := range ix.Decomposition().Bags {
		if len(bag) > maxBag {
			maxBag = len(bag)
		}
	}
	if maxBag != MaxBagSize {
		t.Fatalf("max bag size = %d, want %d", maxBag, MaxBagSize)
	}
	res, err := ix.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Min != 0 || res.Max != 1 {
		t.Fatalf("10-tree bounds = %d..%d, want 0..1", res.Min, res.Max)
	}
}
