package dvv

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Duplicate write: the same value re-written with the same context
// (client retry, at-least-once broadcast) must not create a sibling.
func TestDuplicateWriteIsDeduplicated(t *testing.T) {
	s := NewStore("a")
	ctx := EmptyClock()
	s.Put("k", "v", ctx)
	s.Put("k", "v", ctx) // retry of the same logical write

	if got := s.Get("k"); len(got) != 1 {
		t.Fatalf("duplicate write must not create a sibling, got %v", got)
	}
}

// Same value written concurrently on two replicas is the same logical
// write: the clocks fuse, no redundant branch survives the sync.
func TestSameValueConcurrentWritesFuse(t *testing.T) {
	a, b := NewStore("a"), NewStore("b")
	a.Put("k", "x", EmptyClock())
	b.Put("k", "x", EmptyClock())
	a.Sync(b)
	b.Sync(a)

	for _, s := range []*Store{a, b} {
		vs := s.Get("k")
		if len(vs) != 1 {
			t.Fatalf("%s: same-value duplicates must fuse, got %v", s.Node(), vs)
		}
		if !vs[0].Clock.Descends(a.Context("k")) || !vs[0].Clock.Descends(b.Context("k")) {
			t.Fatalf("%s: fused clock %s must cover both events", s.Node(), vs[0].Clock)
		}
	}
}

// Causal overwrite: the new write saw the old one, so the old version
// is pruned. No ghost sibling.
func TestCausalOverwriteLeavesNoGhostSibling(t *testing.T) {
	s := NewStore("a")
	c1 := s.Put("k", "v1", EmptyClock())
	s.Put("k", "v2", c1)

	vs := s.Get("k")
	if len(vs) != 1 || vs[0].Data != "v2" {
		t.Fatalf("causal overwrite must prune v1, got %v", vs)
	}
}

// Genuine conflict: concurrent writes of different values are the only
// case that produces siblings.
func TestConcurrentDifferentValuesKeepSiblings(t *testing.T) {
	a, b := NewStore("a"), NewStore("b")
	a.Put("k", "from-a", EmptyClock())
	b.Put("k", "from-b", EmptyClock())
	a.Sync(b)

	if vs := a.Get("k"); len(vs) != 2 {
		t.Fatalf("genuine conflict must keep 2 siblings, got %v", vs)
	}
}

// Stale blind write: a client holding an old context writes a different
// value. A classic version vector would let the new dot counter falsely
// dominate the unseen newer value (ghost branch, silent data loss).
// DVV keeps both as honest siblings.
func TestStaleWriteDoesNotClobberUnseenValue(t *testing.T) {
	s := NewStore("a")
	c1 := s.Put("k", "v1", EmptyClock())
	s.Put("k", "v2", c1) // v2 supersedes v1

	// Delayed client still holding c1 writes a different value.
	s.Put("k", "v1-late", c1)

	vs := s.Get("k")
	if len(vs) != 2 {
		t.Fatalf("stale blind write is concurrent with the unseen v2: want 2 siblings, got %v", vs)
	}
}

// High-frequency empty updates: the same value re-written with the
// latest context. Every write supersedes the previous one, so exactly
// one version survives and the clock stays O(nodes), not O(writes).
func TestHighFrequencyEmptyUpdatesStayMinimal(t *testing.T) {
	s := NewStore("a")
	ctx := EmptyClock()
	const writes = 1000
	for i := 0; i < writes; i++ {
		ctx = s.Put("hot-key", "same-value", ctx)
	}

	vs := s.Get("hot-key")
	if len(vs) != 1 {
		t.Fatalf("%d empty updates produced %d versions, want 1", writes, len(vs))
	}
	if size := vs[0].Clock.Size(); size != 2 {
		t.Fatalf("clock footprint = %d entries, want 2 (vector entry + dot)", size)
	}
	if ctr := vs[0].Clock.maxCounter("a"); ctr != writes {
		t.Fatalf("counter = %d, want %d", ctr, writes)
	}
}

// Clock width is bounded by the number of nodes, not the number of
// writes, even when writes hop across nodes.
func TestMultiNodeClockWidthBoundedByNodes(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	stores := map[string]*Store{}
	for _, n := range nodes {
		stores[n] = NewStore(n)
	}
	ctx := EmptyClock()
	for i := 0; i < 300; i++ {
		n := nodes[i%len(nodes)]
		ctx = stores[n].Put("k", fmt.Sprintf("v%d", i), ctx)
	}
	if size := ctx.Size(); size != len(nodes)+1 {
		t.Fatalf("clock width = %d, want %d (one vector entry per node + dot)", size, len(nodes)+1)
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	a, b := NewStore("a"), NewStore("b")
	c := a.Put("k", "v", EmptyClock())
	b.Sync(a)
	before := b.Snapshot()
	for i := 0; i < 5; i++ {
		b.Merge("k", Value{Data: "v", Clock: c})
	}
	if !reflect.DeepEqual(before, b.Snapshot()) {
		t.Fatal("re-merging the same version must be a no-op")
	}
}

// TestConvergenceUnderShuffledBroadcast simulates leaderless replicas
// exchanging versions over an unreliable, reordering, duplicating
// network, and asserts all replicas converge to the same minimal state.
func TestConvergenceUnderShuffledBroadcast(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	nodes := []string{"n1", "n2", "n3"}
	replicas := map[string]*Store{}
	for _, n := range nodes {
		replicas[n] = NewStore(n)
	}

	type msg struct {
		key string
		v   Value
	}
	var network []msg

	// Phase 1: mixed workload — high-frequency empty updates on "hot",
	// causal overwrites on "doc", and blind concurrent writes on
	// "conflict".
	ctxs := map[string]Clock{}
	for i := 0; i < 200; i++ {
		n := nodes[rng.Intn(len(nodes))]
		s := replicas[n]
		switch i % 3 {
		case 0: // empty update: same value again
			c := s.Put("hot", "payload", ctxs["hot"])
			ctxs["hot"] = c
			network = append(network, msg{"hot", Value{"payload", c}})
		case 1: // causal overwrite
			v := fmt.Sprintf("rev-%d", i)
			c := s.Put("doc", v, ctxs["doc"])
			ctxs["doc"] = c
			network = append(network, msg{"doc", Value{v, c}})
		default: // blind write of a fresh value (genuine conflict)
			v := fmt.Sprintf("blind-%s-%d", n, i)
			c := s.Put("conflict", v, EmptyClock())
			network = append(network, msg{"conflict", Value{v, c}})
		}
	}

	// Phase 2: deliver every message to random subsets of replicas, in
	// random order, with duplication (at-least-once delivery).
	for round := 0; round < 3; round++ {
		perm := rng.Perm(len(network))
		for _, idx := range perm {
			m := network[idx]
			for _, n := range nodes {
				if rng.Intn(2) == 0 {
					replicas[n].Merge(m.key, m.v)
				}
			}
		}
	}
	// Full anti-entropy rounds in random pairwise order.
	for round := 0; round < 2; round++ {
		perm := rng.Perm(len(nodes))
		for _, i := range perm {
			for _, j := range perm {
				if i != j {
					replicas[nodes[i]].Sync(replicas[nodes[j]])
				}
			}
		}
	}

	// All replicas converge to identical state.
	want := replicas["n1"].Snapshot()
	for _, n := range nodes[1:] {
		if got := replicas[n].Snapshot(); !reflect.DeepEqual(want, got) {
			t.Fatalf("replica %s diverged:\n got %v\nwant %v", n, got, want)
		}
	}

	// Minimality: hot and doc hold exactly one version each (no ghost
	// branches from 133 writes); conflict holds only genuine siblings.
	for _, n := range nodes {
		s := replicas[n]
		if vs := s.Get("hot"); len(vs) != 1 {
			t.Fatalf("%s: hot has %d versions, want 1", n, len(vs))
		}
		if vs := s.Get("doc"); len(vs) != 1 {
			t.Fatalf("%s: doc has %d versions, want 1 (no ghost branches)", n, len(vs))
		}
		siblings := len(s.Get("conflict"))
		_, versions, clockEntries := s.Stats()
		if versions != siblings+2 {
			t.Fatalf("%s: %d live versions, want %d (conflict siblings + hot + doc)", n, versions, siblings+2)
		}
		// Every clock is O(nodes) wide regardless of the 200 writes.
		if clockEntries > versions*(len(nodes)+1) {
			t.Fatalf("%s: clock footprint %d exceeds %d", n, clockEntries, versions*(len(nodes)+1))
		}
	}
	t.Logf("converged state: %v", want)
}
