package versioned

import (
	"math/rand"
	"testing"
)

// pathWeightFrozen sums the frozen cost of a path (base edge IDs) at
// (knowRev, depart); ok is false if any edge is closed.
func pathWeightFrozen(kb *KnowledgeBase, knowRev Revision, depart Time, path []int) (int64, bool) {
	var w int64
	for _, id := range path {
		st := kb.stateAt(id, knowRev, depart)
		if st.closed {
			return 0, false
		}
		w += st.cost
	}
	return w, true
}

func checkAnswer(t *testing.T, kb *KnowledgeBase, knowRev Revision, q Query, ans Answer) {
	t.Helper()
	want := kb.ShortestPathAt(knowRev, q.Depart, q.S, q.T)
	if ans.Reachable != want.Reachable {
		t.Fatalf("query %+v: reachable=%v, full recompute=%v", q, ans.Reachable, want.Reachable)
	}
	if !ans.Reachable {
		return
	}
	if ans.Dist != want.Dist {
		t.Fatalf("query %+v: dist=%d, full recompute=%d", q, ans.Dist, want.Dist)
	}
	w, ok := pathWeightFrozen(kb, knowRev, q.Depart, ans.Path)
	if !ok {
		t.Fatalf("query %+v: path %v uses a closed edge", q, ans.Path)
	}
	if w != ans.Dist {
		t.Fatalf("query %+v: path weight %d != dist %d", q, w, ans.Dist)
	}
}

func TestFrozenSnapshotJudgesAllEdgesAtDepartTime(t *testing.T) {
	// s->a takes 5 time units, so arrival at a is after the cheap window
	// of a->t closes. Snapshot semantics must still judge a->t at the
	// departure time, where it is cheap.
	kb := NewKnowledgeBase(3)
	kb.AddEdge(0, 1, 5) // s->a
	e1 := kb.AddEdge(1, 2, 1)
	kb.Revise([]CostChange{{Edge: e1, From: 10, To: 20, Cost: 50}}, nil)

	ans := kb.ShortestPathAt(kb.CurrentRevision(), 8, 0, 2)
	if !ans.Reachable || ans.Dist != 6 {
		t.Fatalf("depart=8: got %+v, want dist 6 (all edges judged at t=8)", ans)
	}
	// Departing inside the expensive window freezes the high cost.
	ans = kb.ShortestPathAt(kb.CurrentRevision(), 12, 0, 2)
	if !ans.Reachable || ans.Dist != 55 {
		t.Fatalf("depart=12: got %+v, want dist 55", ans)
	}
}

func TestFixedKnowledgeVersionIsImmutable(t *testing.T) {
	kb := NewKnowledgeBase(2)
	e0 := kb.AddEdge(0, 1, 10)
	v0 := kb.CurrentRevision()
	kb.Revise([]CostChange{{Edge: e0, From: 0, To: 100, Cost: 3}}, nil)

	old := kb.ShortestPathAt(v0, 5, 0, 1)
	if old.Dist != 10 {
		t.Fatalf("old revision: got dist %d, want 10 (revisions are append-only)", old.Dist)
	}
	cur := kb.ShortestPathAt(kb.CurrentRevision(), 5, 0, 1)
	if cur.Dist != 3 {
		t.Fatalf("current revision: got dist %d, want 3", cur.Dist)
	}
	// A query outside the change interval is unaffected.
	outside := kb.ShortestPathAt(kb.CurrentRevision(), 200, 0, 1)
	if outside.Dist != 10 {
		t.Fatalf("outside interval: got dist %d, want 10", outside.Dist)
	}
}

// TestOffPathPriceDropCounterexample is the key soundness check: a cached
// shortest path cannot be validated by looking only at its own edges. A
// price drop on an edge outside the cached path must evict the entry.
func TestOffPathPriceDropCounterexample(t *testing.T) {
	kb := NewKnowledgeBase(3)
	e0 := kb.AddEdge(0, 2, 10) // direct s->t
	kb.AddEdge(0, 1, 3)        // s->a
	kb.AddEdge(1, 2, 3)        // a->t

	cache := NewCache(kb)
	q := Query{S: 0, T: 2, Depart: 0}

	first := cache.Query(q)
	if first.Dist != 6 {
		t.Fatalf("initial: got dist %d, want 6", first.Dist)
	}
	if cache.Misses != 1 || cache.Hits != 0 {
		t.Fatalf("initial: misses=%d hits=%d, want 1/0", cache.Misses, cache.Hits)
	}

	// Local revision: the direct arc (NOT on the cached path) gets cheaper.
	kb.Revise([]CostChange{{Edge: e0, From: 0, To: 100, Cost: 4}}, nil)

	// A naive cache that only checks path edges would keep the stale
	// entry: none of the changed edges is on the cached path.
	naiveKeeps := true
	for _, id := range first.Path {
		if id == e0 {
			naiveKeeps = false
		}
	}
	if !naiveKeeps {
		t.Fatal("test setup broken: changed edge is on the cached path")
	}
	stale := first.Dist // what the naive cache would still answer
	truth := kb.ShortestPathAt(kb.CurrentRevision(), q.Depart, q.S, q.T)
	if stale == truth.Dist {
		t.Fatal("test setup broken: price drop did not change the answer")
	}

	// Our cache must have evicted the entry and must agree with a full
	// recomputation.
	if cache.Evictions != 1 || cache.Len() != 0 {
		t.Fatalf("evictions=%d len=%d, want 1/0", cache.Evictions, cache.Len())
	}
	second := cache.Query(q)
	if cache.Hits != 0 {
		t.Fatalf("stale entry served as a hit")
	}
	checkAnswer(t, kb, kb.CurrentRevision(), q, second)
	if second.Dist != 4 {
		t.Fatalf("after price drop: got dist %d, want 4", second.Dist)
	}
}

func TestOffPathPriceIncreaseKeepsEntry(t *testing.T) {
	kb := NewKnowledgeBase(3)
	e0 := kb.AddEdge(0, 2, 10)
	kb.AddEdge(0, 1, 3)
	kb.AddEdge(1, 2, 3)

	cache := NewCache(kb)
	q := Query{S: 0, T: 2, Depart: 0}
	cache.Query(q)

	// Off-path increase: alternatives only got worse, entry must survive.
	kb.Revise([]CostChange{{Edge: e0, From: 0, To: 100, Cost: 20}}, nil)
	if cache.Evictions != 0 || cache.Len() != 1 {
		t.Fatalf("evictions=%d len=%d, want 0/1", cache.Evictions, cache.Len())
	}
	ans := cache.Query(q)
	if cache.Hits != 1 {
		t.Fatalf("expected a cache hit, hits=%d", cache.Hits)
	}
	checkAnswer(t, kb, kb.CurrentRevision(), q, ans)
}

func TestPathEdgeChangeEvicts(t *testing.T) {
	kb := NewKnowledgeBase(3)
	kb.AddEdge(0, 2, 10)
	kb.AddEdge(0, 1, 3)
	e2 := kb.AddEdge(1, 2, 3)

	cache := NewCache(kb)
	q := Query{S: 0, T: 2, Depart: 0}
	cache.Query(q)

	// A cost increase on a path edge evicts even though the path may
	// still be optimal; recomputation decides.
	kb.Revise([]CostChange{{Edge: e2, From: 0, To: 100, Cost: 5}}, nil)
	if cache.Evictions != 1 {
		t.Fatalf("evictions=%d, want 1", cache.Evictions)
	}
	ans := cache.Query(q)
	checkAnswer(t, kb, kb.CurrentRevision(), q, ans)
	if ans.Dist != 8 {
		t.Fatalf("got dist %d, want 8", ans.Dist)
	}
}

func TestClosureEvents(t *testing.T) {
	kb := NewKnowledgeBase(3)
	kb.AddEdge(0, 2, 10)
	kb.AddEdge(0, 1, 3)
	e2 := kb.AddEdge(1, 2, 3)

	cache := NewCache(kb)
	q := Query{S: 0, T: 2, Depart: 50}
	if ans := cache.Query(q); ans.Dist != 6 {
		t.Fatalf("initial: got dist %d, want 6", ans.Dist)
	}

	// Closing a path edge over the departure time evicts and reroutes.
	kb.Revise(nil, []ClosureEvent{{Edge: e2, From: 0, To: 100}})
	if cache.Evictions != 1 {
		t.Fatalf("evictions=%d, want 1", cache.Evictions)
	}
	ans := cache.Query(q)
	checkAnswer(t, kb, kb.CurrentRevision(), q, ans)
	if ans.Dist != 10 {
		t.Fatalf("after closure: got dist %d, want 10", ans.Dist)
	}

	// The closure does not apply outside its interval: a query departing
	// at t=200 still sees the cheap route.
	far := kb.ShortestPathAt(kb.CurrentRevision(), 200, 0, 2)
	if far.Dist != 6 {
		t.Fatalf("outside closure interval: got dist %d, want 6", far.Dist)
	}
}

func TestUnreachableEntrySurvivesIrrelevantRevisions(t *testing.T) {
	// A cached "unreachable" answer has an empty path, so its certificate
	// rests entirely on the off-path "nothing got cheaper" rule. With
	// append-only revisions a closure is never lifted and the topology is
	// static, so an unreachable entry must survive revisions that change
	// nothing at the departure time.
	kb := NewKnowledgeBase(2)
	e0 := kb.AddEdge(0, 1, 7)
	e1 := kb.AddEdge(0, 1, 9) // parallel arc
	kb.Revise(nil, []ClosureEvent{{Edge: e0, From: 0, To: 100}})

	cache := NewCache(kb)
	q := Query{S: 0, T: 1, Depart: 50}
	if ans := cache.Query(q); !ans.Reachable || ans.Dist != 9 {
		t.Fatalf("got %+v, want dist 9 via the parallel arc", ans)
	}

	// A cost record equal to the frozen state changes nothing.
	kb.Revise([]CostChange{{Edge: e0, From: 0, To: 100, Cost: 7}}, nil)
	if cache.Len() != 1 {
		t.Fatalf("no-op revision evicted the entry, len=%d", cache.Len())
	}
	// A closure that does not cover the departure time changes nothing.
	kb.Revise(nil, []ClosureEvent{{Edge: e1, From: 0, To: 40}})
	if cache.Len() != 1 {
		t.Fatalf("irrelevant closure evicted the entry, len=%d", cache.Len())
	}
	// Closing the parallel arc over the departure time makes the target
	// unreachable; the cached entry must be evicted.
	kb.Revise(nil, []ClosureEvent{{Edge: e1, From: 0, To: 100}})
	if cache.Len() != 0 {
		t.Fatalf("path-edge closure did not evict, len=%d", cache.Len())
	}
	ans := cache.Query(q)
	checkAnswer(t, kb, kb.CurrentRevision(), q, ans)
	if ans.Reachable {
		t.Fatalf("got %+v, want unreachable", ans)
	}
}

func TestCacheMatchesFullRecomputeRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 150; trial++ {
		n := 2 + rng.Intn(5)
		kb := NewKnowledgeBase(n)
		m := 1 + rng.Intn(2*n)
		for i := 0; i < m; i++ {
			kb.AddEdge(rng.Intn(n), rng.Intn(n), int64(rng.Intn(10)))
		}
		cache := NewCache(kb)
		queries := []Query{
			{S: 0, T: n - 1, Depart: Time(rng.Intn(20))},
			{S: 0, T: n - 1, Depart: Time(rng.Intn(20))},
		}
		for _, q := range queries {
			checkAnswer(t, kb, kb.CurrentRevision(), q, cache.Query(q))
		}
		// Apply a random sequence of local revisions; after each one the
		// cache must agree with a full recomputation.
		for step := 0; step < 6; step++ {
			var costs []CostChange
			var closures []ClosureEvent
			for k := 0; k < 1+rng.Intn(3); k++ {
				edge := rng.Intn(m)
				from := Time(rng.Intn(20))
				if rng.Intn(2) == 0 {
					costs = append(costs, CostChange{Edge: edge, From: from, To: from + Time(1+rng.Intn(10)), Cost: int64(rng.Intn(10))})
				} else {
					closures = append(closures, ClosureEvent{Edge: edge, From: from, To: from + Time(1+rng.Intn(10))})
				}
			}
			kb.Revise(costs, closures)
			for _, q := range queries {
				checkAnswer(t, kb, kb.CurrentRevision(), q, cache.Query(q))
			}
		}
	}
}
