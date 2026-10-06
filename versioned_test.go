package roadgraph

import (
	"math/rand"
	"testing"
)

func mustAddVE(t *testing.T, vg *VersionedGraph, id EdgeID, from, to int, revs ...Revision) {
	t.Helper()
	if err := vg.AddEdge(id, from, to, revs...); err != nil {
		t.Fatalf("AddEdge(%d): %v", id, err)
	}
}

func TestFreezeSelectsLatestKnownRevision(t *testing.T) {
	vg := NewVersionedGraph(2)
	mustAddVE(t, vg, 1, 0, 1,
		Revision{Rev: 1, Start: 0, End: 50, Cost: 5},
		Revision{Rev: 3, Start: 0, End: 50, Cost: 7},
		Revision{Rev: 2, Start: 10, End: 20, Closed: true},
	)

	cases := []struct {
		knowledge, depart int64
		present           bool
		cost              int64
	}{
		{0, 5, false, 0},  // nothing known yet
		{1, 5, true, 5},   // only rev 1 known
		{2, 5, false, 0},  // rev 2 is latest known but does not cover depart 5
		{2, 15, false, 0}, // rev 2 closes the road at depart 15
		{3, 15, true, 7},  // rev 3 supersedes the closure
		{3, 60, false, 0}, // outside every valid interval
		{100, 5, true, 7}, // far-future knowledge still sees rev 3
	}
	for _, c := range cases {
		frozen, considered := vg.Freeze(c.knowledge, c.depart)
		e, ok := frozen.Edge(1)
		if ok != c.present {
			t.Fatalf("K=%d t=%d: present=%v, want %v", c.knowledge, c.depart, ok, c.present)
		}
		if ok && e.Weight != c.cost {
			t.Fatalf("K=%d t=%d: cost=%d, want %d", c.knowledge, c.depart, e.Weight, c.cost)
		}
		if c.knowledge >= 1 && len(considered) != 1 {
			t.Fatalf("K=%d: edge 1 must be in the considered set", c.knowledge)
		}
	}
}

func TestFrozenGraphIsASingleSnapshot(t *testing.T) {
	// Two routes; each edge has its own interval. A path's edges are all
	// judged at the same departure time, never mixed across snapshots.
	vg := NewVersionedGraph(4)
	mustAddVE(t, vg, 1, 0, 1, Revision{Rev: 1, Start: 0, End: 10, Cost: 3})
	mustAddVE(t, vg, 2, 1, 3, Revision{Rev: 1, Start: 5, End: 20, Cost: 3})
	mustAddVE(t, vg, 3, 0, 2, Revision{Rev: 1, Start: 0, End: 20, Cost: 4})
	mustAddVE(t, vg, 4, 2, 3, Revision{Rev: 1, Start: 0, End: 20, Cost: 4})

	// At depart 3 edge 2 is not yet valid, so the 0->1->3 route (cost 6)
	// does not exist even though both of its edges are cheap somewhere.
	r := vg.Query(Query{Knowledge: 1, Depart: 3, Source: 0, Target: 3})
	if !r.Reachable || r.Distance != 8 {
		t.Fatalf("depart 3: got %+v, want distance 8 via 0->2->3", r)
	}
	// At depart 7 both snapshots' edges coexist in one frozen graph.
	r = vg.Query(Query{Knowledge: 1, Depart: 7, Source: 0, Target: 3})
	if !r.Reachable || r.Distance != 6 {
		t.Fatalf("depart 7: got %+v, want distance 6 via 0->1->3", r)
	}
}

func TestQueryMatchesBruteForceOnFrozenGraph(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 150; trial++ {
		n := 2 + rng.Intn(4)
		vg := NewVersionedGraph(n)
		m := 1 + rng.Intn(8)
		for i := 0; i < m; i++ {
			from := rng.Intn(n)
			to := rng.Intn(n)
			if to == from {
				to = (to + 1) % n
			}
			var revs []Revision
			revNo := int64(0)
			for k := 0; k < 1+rng.Intn(3); k++ {
				revNo += 1 + int64(rng.Intn(3)) // strictly increasing per edge
				start := int64(rng.Intn(15))
				revs = append(revs, Revision{
					Rev:    revNo,
					Start:  start,
					End:    start + 1 + int64(rng.Intn(6)),
					Cost:   int64(rng.Intn(10)),
					Closed: rng.Intn(5) == 0,
				})
			}
			if err := vg.AddEdge(EdgeID(100*trial+i), from, to, revs...); err != nil {
				t.Fatal(err)
			}
		}
		source := rng.Intn(n)
		target := rng.Intn(n)
		if source == target {
			continue
		}
		q := Query{
			Knowledge: int64(rng.Intn(8)),
			Depart:    int64(rng.Intn(20)),
			Source:    source,
			Target:    target,
		}
		got := vg.Query(q)
		frozen, _ := vg.Freeze(q.Knowledge, q.Depart)
		best, found := bruteForce(frozen, source, target, -1)
		if found != got.Reachable {
			t.Fatalf("trial %d %+v: reachable=%v, brute force=%v", trial, q, got.Reachable, found)
		}
		if found {
			if got.Distance != best {
				t.Fatalf("trial %d %+v: dist=%d, brute force=%d", trial, q, got.Distance, best)
			}
			checkWitness(t, frozen, source, target, got.Path, got.Distance, -1)
		}
	}
}
