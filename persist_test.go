package paircache

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { f.t = f.t.Add(time.Second); return f.t }

// varLoader returns deterministic variable-length values per key.
func varLoader(string) Loader {
	return func(k string) ([]byte, error) {
		h := 0
		for _, r := range k {
			h = h*31 + int(r)
		}
		if h < 0 {
			h = -h
		}
		return make([]byte, 32+h%97), nil
	}
}

func makeTrace(seed int64, space, ops int) []string {
	r := rand.New(rand.NewSource(seed))
	trace := make([]string, ops)
	for i := range trace {
		trace[i] = fmt.Sprintf("k%d", r.Intn(space))
	}
	return trace
}

func runTrace(t *testing.T, c interface{ Acquire(string) (*Lease, error) }, keys []string) {
	t.Helper()
	for _, k := range keys {
		l, err := c.Acquire(k)
		if err != nil {
			t.Fatalf("Acquire(%q): %v", k, err)
		}
		l.Release()
	}
}

func twoqOrder(c *TwoQ) (recent, freq, ghost []string) {
	for el := c.recent.Front(); el != nil; el = el.Next() {
		recent = append(recent, el.Value.(*entry).key)
	}
	for el := c.freq.Front(); el != nil; el = el.Next() {
		freq = append(freq, el.Value.(*entry).key)
	}
	for el := c.ghost.Front(); el != nil; el = el.Next() {
		ghost = append(ghost, el.Value.(*ghostRec).key)
	}
	return
}

func lruOrder(c *LRU) (keys []string) {
	for el := c.lru.Front(); el != nil; el = el.Next() {
		keys = append(keys, el.Value.(*entry).key)
	}
	return
}

// A restored 2Q cache must replay the remaining access stream with hit/miss/
// eviction decisions identical to the uninterrupted original run.
func TestRestoreReplayConsistency2Q(t *testing.T) {
	trace := makeTrace(7, 300, 800)
	clkA := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	optA := Options{CapacityBytes: 2048, Loader: varLoader(""), Now: clkA.now, GhostLimit: 64}

	orig := NewTwoQ(optA)
	runTrace(t, orig, trace[:400])
	snap, err := orig.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	baseA := orig.Stats()
	runTrace(t, orig, trace[400:])
	statsA := orig.Stats()
	deltaA := Stats{
		Hits:      statsA.Hits - baseA.Hits,
		Misses:    statsA.Misses - baseA.Misses,
		GhostHits: statsA.GhostHits - baseA.GhostHits,
		Evictions: statsA.Evictions - baseA.Evictions,
	}

	clkB := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	restored, err := RestoreTwoQ(snap, Options{Loader: varLoader(""), Now: clkB.now})
	if err != nil {
		t.Fatal(err)
	}
	runTrace(t, restored, trace[400:])
	statsB := restored.Stats()

	if deltaA.Hits != statsB.Hits || deltaA.Misses != statsB.Misses ||
		deltaA.GhostHits != statsB.GhostHits || deltaA.Evictions != statsB.Evictions {
		t.Fatalf("replay divergence:\norig delta %+v\nrestored   %+v", deltaA, statsB)
	}
	if statsA.ResidentBytes != statsB.ResidentBytes {
		t.Fatalf("ResidentBytes %d != %d", statsA.ResidentBytes, statsB.ResidentBytes)
	}
	rA, fA, gA := twoqOrder(orig)
	rB, fB, gB := twoqOrder(restored)
	eq := func(a, b []string) bool {
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
	if !eq(rA, rB) || !eq(fA, fB) || !eq(gA, gB) {
		t.Fatal("queue order diverged after restore+replay")
	}
}

func TestRestoreReplayConsistencyLRU(t *testing.T) {
	trace := makeTrace(11, 200, 600)
	clkA := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	orig := NewLRU(Options{CapacityBytes: 2048, Loader: varLoader(""), Now: clkA.now})
	runTrace(t, orig, trace[:300])
	snap, err := orig.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	baseA := orig.Stats()
	runTrace(t, orig, trace[300:])
	statsA := orig.Stats()
	deltaA := Stats{
		Hits:      statsA.Hits - baseA.Hits,
		Misses:    statsA.Misses - baseA.Misses,
		Evictions: statsA.Evictions - baseA.Evictions,
	}

	clkB := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	restored, err := RestoreLRU(snap, Options{Loader: varLoader(""), Now: clkB.now})
	if err != nil {
		t.Fatal(err)
	}
	runTrace(t, restored, trace[300:])
	statsB := restored.Stats()

	if deltaA.Hits != statsB.Hits || deltaA.Misses != statsB.Misses || deltaA.Evictions != statsB.Evictions {
		t.Fatalf("replay divergence:\norig delta %+v\nrestored   %+v", deltaA, statsB)
	}
	a, b := lruOrder(orig), lruOrder(restored)
	if len(a) != len(b) {
		t.Fatal("queue length diverged")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("queue order diverged at %d: %q != %q", i, a[i], b[i])
		}
	}
}

// Identical runs with an injected deterministic clock must produce identical
// snapshot bytes.
func TestSnapshotDeterministic(t *testing.T) {
	snapOf := func() []byte {
		clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
		c := NewTwoQ(Options{CapacityBytes: 2048, Loader: varLoader(""), Now: clk.now})
		runTrace(t, c, makeTrace(3, 100, 300))
		data, err := c.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if a, b := snapOf(), snapOf(); !bytes.Equal(a, b) {
		t.Fatal("snapshots of identical runs differ")
	}
}

// Restored entries carry metadata only; values are reloaded lazily on access
// and the access still counts as a hit (policy state was preserved).
func TestRestoreReloadsValuesLazily(t *testing.T) {
	c := NewTwoQ(Options{CapacityBytes: 1024, Loader: varLoader("")})
	runTrace(t, c, []string{"a", "b", "c"})
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	restored, err := RestoreTwoQ(snap, Options{Loader: func(k string) ([]byte, error) {
		loads++
		return varLoader("")(k)
	}})
	if err != nil {
		t.Fatal(err)
	}
	before := restored.Stats().Hits
	l, err := restored.Acquire("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Value()) == 0 {
		t.Fatal("restored entry returned empty value")
	}
	l.Release()
	s := restored.Stats()
	if s.Hits-before != 1 {
		t.Fatalf("access to restored entry should be a hit, Hits delta = %d", s.Hits-before)
	}
	if loads != 1 {
		t.Fatalf("loader called %d times, want 1 lazy reload", loads)
	}
}

func TestRestoreRejectsWrongKind(t *testing.T) {
	c := NewLRU(Options{CapacityBytes: 128, Loader: fixedLoader(8)})
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreTwoQ(snap, Options{}); err == nil {
		t.Fatal("RestoreTwoQ should reject an lru snapshot")
	}
	if _, err := RestoreLRU([]byte("{broken"), Options{}); err == nil {
		t.Fatal("RestoreLRU should reject corrupt data")
	}
}
