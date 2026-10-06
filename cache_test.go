package roadgraph

import (
	"math/rand"
	"reflect"
	"testing"
)

// buildCounterexampleGraph returns the graph for the external-price-drop
// scenario: route A (edges 1,2) costs 10, route B (edges 3,4) costs 13.
func buildCounterexampleGraph(t *testing.T) *VersionedGraph {
	t.Helper()
	vg := NewVersionedGraph(4) // 0=s, 1=a, 2=b, 3=t
	mustAddVE(t, vg, 1, 0, 1, Revision{Rev: 1, Start: 0, End: 100, Cost: 1})
	mustAddVE(t, vg, 2, 1, 3, Revision{Rev: 1, Start: 0, End: 100, Cost: 9})
	mustAddVE(t, vg, 3, 0, 2, Revision{Rev: 1, Start: 0, End: 100, Cost: 1})
	mustAddVE(t, vg, 4, 2, 3, Revision{Rev: 1, Start: 0, End: 100, Cost: 12})
	return vg
}

// naiveCache invalidates only when a revised edge lies on the cached path.
// It exists to demonstrate that path-only invalidation is unsound.
type naiveCache struct {
	vg      *VersionedGraph
	entries map[Query]QueryResult
}

func (n *naiveCache) Query(q Query) QueryResult {
	if r, ok := n.entries[q]; ok {
		return r
	}
	r := n.vg.Query(q)
	n.entries[q] = r
	return r
}

func (n *naiveCache) AddRevision(id EdgeID, rev Revision) {
	for q, r := range n.entries {
		onPath := false
		for _, eid := range r.Path {
			if eid == id {
				onPath = true
			}
		}
		if onPath && rev.Rev <= q.Knowledge {
			delete(n.entries, q)
		}
	}
	n.vg.AddRevision(id, rev)
}

func TestCacheExternalEdgePriceDrop(t *testing.T) {
	vg := buildCounterexampleGraph(t)
	cache := NewPathCache(vg)
	q := Query{Knowledge: 5, Depart: 0, Source: 0, Target: 3}

	r1 := cache.Query(q)
	if !r1.Reachable || r1.Distance != 10 {
		t.Fatalf("initial: got %+v, want distance 10 via route A", r1)
	}
	if !reflect.DeepEqual(r1.Path, []EdgeID{1, 2}) {
		t.Fatalf("initial path = %v, want [1 2]", r1.Path)
	}

	// The dependency set must contain external edge 4 even though the
	// cached path [1 2] does not: Dijkstra examined it while proving
	// route A optimal.
	entry := cache.entries[q]
	if !entry.readSet[4] {
		t.Fatal("edge 4 must be in the dependency set")
	}
	for _, id := range r1.Path {
		if id == 4 {
			t.Fatal("edge 4 is not on the cached path")
		}
	}

	// A local revision lowers the cost of edge 4 (external to the path).
	drop := Revision{Rev: 2, Start: 0, End: 100, Cost: 8}
	if err := cache.AddRevision(4, drop); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.entries[q]; ok {
		t.Fatal("entry must be invalidated by the external edge's revision")
	}

	// The next answer must match a full recompute: route B now wins 9 < 10.
	r2 := cache.Query(q)
	want := vg.Query(q) // full recompute, no cache
	if !reflect.DeepEqual(r2, want) {
		t.Fatalf("cached answer %+v != full recompute %+v", r2, want)
	}
	if r2.Distance != 9 || !reflect.DeepEqual(r2.Path, []EdgeID{3, 4}) {
		t.Fatalf("got %+v, want distance 9 via [3 4]", r2)
	}
}

func TestPathOnlyInvalidationIsUnsound(t *testing.T) {
	// Same scenario through the naive cache: invalidating only on edges of
	// the cached path keeps the stale answer and disagrees with a full
	// recompute. This is the counterexample the dependency set protects
	// against.
	vg := buildCounterexampleGraph(t)
	naive := &naiveCache{vg: vg, entries: make(map[Query]QueryResult)}
	q := Query{Knowledge: 5, Depart: 0, Source: 0, Target: 3}

	if r := naive.Query(q); r.Distance != 10 {
		t.Fatalf("initial: got %d, want 10", r.Distance)
	}
	naive.AddRevision(4, Revision{Rev: 2, Start: 0, End: 100, Cost: 8})

	stale := naive.Query(q)
	truth := vg.Query(q) // full recompute
	if stale.Distance == truth.Distance {
		t.Fatalf("test is broken: naive cache should be stale, both give %d", stale.Distance)
	}
	if stale.Distance != 10 || truth.Distance != 9 {
		t.Fatalf("naive=%d truth=%d, want 10 vs 9", stale.Distance, truth.Distance)
	}
}

func TestCacheSelectiveInvalidation(t *testing.T) {
	vg := buildCounterexampleGraph(t)
	cache := NewPathCache(vg)
	q := Query{Knowledge: 5, Depart: 0, Source: 0, Target: 3}
	cache.Query(q)
	if cache.Size() != 1 {
		t.Fatalf("size = %d, want 1", cache.Size())
	}

	// A revision invisible at the cached knowledge version changes nothing.
	if err := cache.AddRevision(4, Revision{Rev: 9, Start: 0, End: 100, Cost: 1}); err != nil {
		t.Fatal(err)
	}
	if cache.Size() != 1 {
		t.Fatal("future revision (Rev > knowledge) must not invalidate")
	}

	// A visible revision to an examined edge does invalidate.
	if err := cache.AddRevision(4, Revision{Rev: 2, Start: 0, End: 100, Cost: 12}); err != nil {
		t.Fatal(err)
	}
	if cache.Size() != 0 {
		t.Fatal("visible revision to a dependency must invalidate")
	}
}

func TestCacheBackfilledRevisionInvalidates(t *testing.T) {
	// Edge 5 starts with no revisions at all: unknown at any knowledge
	// version, so it is absent from the frozen graph and from the read set.
	vg := buildCounterexampleGraph(t)
	mustAddVE(t, vg, 5, 0, 3) // direct s->t, no revisions yet
	cache := NewPathCache(vg)
	q := Query{Knowledge: 5, Depart: 0, Source: 0, Target: 3}

	if r := cache.Query(q); r.Distance != 10 {
		t.Fatalf("initial: got %d, want 10", r.Distance)
	}
	if cache.entries[q].readSet[5] {
		t.Fatal("edge 5 is unknown at K=5 and must not be in the read set")
	}

	// A backfilled revision (Rev <= K) makes edge 5 appear retroactively.
	if err := cache.AddRevision(5, Revision{Rev: 3, Start: 0, End: 100, Cost: 4}); err != nil {
		t.Fatal(err)
	}
	if cache.Size() != 0 {
		t.Fatal("backfilled revision for a previously unknown edge must invalidate")
	}
	if r := cache.Query(q); r.Distance != 4 {
		t.Fatalf("after backfill: got %d, want 4 via edge 5", r.Distance)
	}
}

func TestCacheInvalidationAcrossDepartTimes(t *testing.T) {
	vg := buildCounterexampleGraph(t)
	cache := NewPathCache(vg)
	// Two queries sharing the knowledge version but differing in depart time.
	q1 := Query{Knowledge: 5, Depart: 0, Source: 0, Target: 3}
	q2 := Query{Knowledge: 5, Depart: 250, Source: 0, Target: 3}
	cache.Query(q1)
	cache.Query(q2)

	// The revision only covers [0,100): q2's snapshot at depart 250 is
	// unaffected in substance, but the conservative dependency rule still
	// drops q1 only through visibility; q2 is dropped too because edge 4 is
	// in its read set. Both are recomputed and must match full recompute.
	if err := cache.AddRevision(4, Revision{Rev: 2, Start: 0, End: 100, Cost: 8}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []Query{q1, q2} {
		if got, want := cache.Query(q), vg.Query(q); !reflect.DeepEqual(got, want) {
			t.Fatalf("%+v: cached %+v != recompute %+v", q, got, want)
		}
	}
}

func TestCacheMatchesFullRecomputeUnderRevisionStream(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 40; trial++ {
		n := 2 + rng.Intn(4)
		vg := NewVersionedGraph(n)
		cache := NewPathCache(vg)
		m := 2 + rng.Intn(7)
		var edgeIDs []EdgeID
		nextRev := make(map[EdgeID]int64)
		for i := 0; i < m; i++ {
			from := rng.Intn(n)
			to := rng.Intn(n)
			if to == from {
				to = (to + 1) % n
			}
			id := EdgeID(1000*trial + i)
			rev := Revision{
				Rev:    1,
				Start:  0,
				End:    10,
				Cost:   int64(rng.Intn(10)),
				Closed: rng.Intn(6) == 0,
			}
			if err := vg.AddEdge(id, from, to, rev); err != nil {
				t.Fatal(err)
			}
			edgeIDs = append(edgeIDs, id)
			nextRev[id] = 2
		}

		queries := make([]Query, 0, 6)
		for k := 0; k < 6; k++ {
			queries = append(queries, Query{
				Knowledge: int64(rng.Intn(12)),
				Depart:    int64(rng.Intn(12)),
				Source:    rng.Intn(n),
				Target:    rng.Intn(n),
			})
		}

		for step := 0; step < 30; step++ {
			// Randomly populate the cache or apply a local revision.
			if rng.Intn(2) == 0 {
				cache.Query(queries[rng.Intn(len(queries))])
			} else {
				id := edgeIDs[rng.Intn(len(edgeIDs))]
				start := int64(rng.Intn(10))
				rev := Revision{
					Rev:    nextRev[id],
					Start:  start,
					End:    start + 1 + int64(rng.Intn(4)),
					Cost:   int64(rng.Intn(10)),
					Closed: rng.Intn(6) == 0,
				}
				nextRev[id]++
				if err := cache.AddRevision(id, rev); err != nil {
					t.Fatal(err)
				}
			}
			// After every step the cache must agree with a full recompute
			// for every query, including ones never asked before. Equal-cost
			// ties allow different valid witnesses, so compare distance and
			// reachability and validate the witness on a fresh freeze.
			for _, q := range queries {
				got := cache.Query(q)
				want := vg.Query(q)
				if got.Reachable != want.Reachable || got.Distance != want.Distance {
					t.Fatalf("trial %d step %d %+v: cached %+v != recompute %+v",
						trial, step, q, got, want)
				}
				if got.Reachable && q.Source != q.Target {
					frozen, _ := vg.Freeze(q.Knowledge, q.Depart)
					checkWitness(t, frozen, q.Source, q.Target, got.Path, got.Distance, -1)
				}
			}
		}
	}
}
