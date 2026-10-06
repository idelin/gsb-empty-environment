package pageindex

import (
	"math/rand"
	"sync"
	"testing"
)

// checkBTree verifies the structural invariants of the tree rooted at root:
// sorted keys, key-count bounds, child-count consistency and uniform leaf
// depth. Caller must hold st.mu.
func checkBTree(t *testing.T, st *Store, root PageID) {
	t.Helper()
	depth := -1
	var walk func(id PageID, lo, hi uint64, hasLo, hasHi bool, d int)
	walk = func(id PageID, lo, hi uint64, hasLo, hasHi bool, d int) {
		n := st.pages[id]
		if n == nil {
			t.Fatalf("page %d: dangling reference", id)
		}
		for i, k := range n.keys {
			if i > 0 && k <= n.keys[i-1] {
				t.Fatalf("page %d: keys not strictly increasing", id)
			}
			if hasLo && k <= lo || hasHi && k >= hi {
				t.Fatalf("page %d: key %d out of bounds (%d,%d)", id, k, lo, hi)
			}
		}
		if len(n.vals) != len(n.keys) {
			t.Fatalf("page %d: vals/keys length mismatch", id)
		}
		if n.leaf {
			if len(n.kids) != 0 {
				t.Fatalf("page %d: leaf with children", id)
			}
			if depth == -1 {
				depth = d
			} else if depth != d {
				t.Fatalf("page %d: leaf depth %d, want %d", id, d, depth)
			}
			return
		}
		if len(n.kids) != len(n.keys)+1 {
			t.Fatalf("page %d: %d kids for %d keys", id, len(n.kids), len(n.keys))
		}
		if id != st.root && (len(n.keys) < minDegree-1 || len(n.keys) > maxKeys) {
			t.Fatalf("page %d: %d keys out of bounds", id, len(n.keys))
		}
		for i, kid := range n.kids {
			var klo, khi uint64
			var hasKlo, hasKhi bool
			if i > 0 {
				klo, hasKlo = n.keys[i-1], true
			}
			if i < len(n.keys) {
				khi, hasKhi = n.keys[i], true
			}
			walk(kid, klo, khi, hasKlo, hasKhi, d+1)
		}
	}
	walk(root, 0, 0, false, false, 0)
}

func mustGet(t *testing.T, get func(uint64) (uint64, error), key uint64, want uint64) {
	t.Helper()
	got, err := get(key)
	if err != nil {
		t.Fatalf("Get(%d): %v", key, err)
	}
	if got != want {
		t.Fatalf("Get(%d) = %d, want %d", key, got, want)
	}
}

func mustMiss(t *testing.T, get func(uint64) (uint64, error), key uint64) {
	t.Helper()
	if _, err := get(key); err != ErrNotFound {
		t.Fatalf("Get(%d) err = %v, want ErrNotFound", key, err)
	}
}

// TestForkedSnapshots: a long-lived reader pins an old root while the
// writer keeps replacing nodes; a second snapshot forks later. Closing the
// first must recycle exactly the pages no remaining root can reach, and
// reallocation must not pollute the surviving snapshot.
func TestForkedSnapshots(t *testing.T) {
	st := NewStore()
	for k := uint64(0); k < 200; k++ {
		st.Put(k, k*10)
	}
	snap1 := st.Snapshot()

	for k := uint64(0); k < 200; k++ {
		st.Put(k, k*10+1)
	}
	for k := uint64(200); k < 400; k++ {
		st.Put(k, k)
	}
	snap2 := st.Snapshot()

	if snap1.Version() >= snap2.Version() {
		t.Fatalf("snap1 version %d, snap2 version %d", snap1.Version(), snap2.Version())
	}
	mustGet(t, snap1.Get, 7, 70)
	mustGet(t, snap2.Get, 7, 71)
	mustGet(t, st.Get, 7, 71)
	mustMiss(t, snap1.Get, 300)
	mustGet(t, snap2.Get, 300, 300)

	if err := st.CheckRefGraph(); err != nil {
		t.Fatal(err)
	}
	st.mu.RLock()
	checkBTree(t, st, st.root)
	st.mu.RUnlock()

	// Pages reachable only from snap1 are the ones Close must recycle.
	st.mu.RLock()
	only1 := difference(st.reachableLocked(snap1.root),
		st.reachableLocked(st.root, snap2.root))
	shared2 := st.reachableLocked(snap2.root)
	st.mu.RUnlock()
	if len(only1) == 0 {
		t.Fatal("expected pages unique to snap1")
	}

	snap1.Close()
	snap1.Close() // idempotent

	for id := range only1 {
		if st.isAllocated(id) {
			t.Fatalf("page %d reachable only from closed snap1 still allocated", id)
		}
	}
	for id := range shared2 {
		if !st.isAllocated(id) {
			t.Fatalf("page %d reachable from snap2 was recycled", id)
		}
	}
	if err := st.CheckRefGraph(); err != nil {
		t.Fatal(err)
	}
	if _, err := snap1.Get(7); err != ErrSnapshotClosed {
		t.Fatalf("closed snap1 Get err = %v, want ErrSnapshotClosed", err)
	}

	// Force reallocation of recycled page numbers; snap2 must be unaffected.
	before := st.AllocatedPages()
	for k := uint64(400); k < 600; k++ {
		st.Put(k, k)
	}
	for k := uint64(0); k < 400; k++ {
		want := k
		if k < 200 {
			want = k*10 + 1
		}
		mustGet(t, snap2.Get, k, want)
	}
	_ = before
	snap2.Close()
	if err := st.CheckRefGraph(); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteKeepsSnapshot: deleted keys vanish from the current version
// but stay visible to a pinned snapshot; reclamation follows reachability.
func TestDeleteKeepsSnapshot(t *testing.T) {
	st := NewStore()
	for k := uint64(0); k < 300; k++ {
		st.Put(k, k+1000)
	}
	snap := st.Snapshot()
	snapRoot := snap.RootPage()

	for k := uint64(0); k < 300; k += 2 {
		if !st.Delete(k) {
			t.Fatalf("Delete(%d) = false", k)
		}
	}
	if st.Delete(12345) {
		t.Fatal("Delete of absent key = true")
	}
	if st.RootPage() == snapRoot {
		t.Fatal("root page did not change after deletes")
	}

	for k := uint64(0); k < 300; k++ {
		if k%2 == 0 {
			mustMiss(t, st.Get, k)
			mustGet(t, snap.Get, k, k+1000)
		} else {
			mustGet(t, st.Get, k, k+1000)
			mustGet(t, snap.Get, k, k+1000)
		}
	}
	if err := st.CheckRefGraph(); err != nil {
		t.Fatal(err)
	}
	st.mu.RLock()
	checkBTree(t, st, st.root)
	st.mu.RUnlock()

	// After Close, pages only the snapshot used are recycled.
	st.mu.RLock()
	onlySnap := difference(st.reachableLocked(snap.root), st.reachableLocked(st.root))
	st.mu.RUnlock()
	snap.Close()
	for id := range onlySnap {
		if st.isAllocated(id) {
			t.Fatalf("page %d leaked after snapshot close", id)
		}
	}
	if err := st.CheckRefGraph(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentClose: repeated and concurrent Close calls are idempotent
// and race-free while a writer keeps replacing nodes and readers probe.
func TestConcurrentClose(t *testing.T) {
	st := NewStore()
	for k := uint64(0); k < 500; k++ {
		st.Put(k, k)
	}

	snaps := make([]*Snapshot, 16)
	for i := range snaps {
		snaps[i] = st.Snapshot()
	}

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		k := uint64(1000)
		for {
			select {
			case <-stop:
				return
			default:
			}
			st.Put(k, k)
			if k%3 == 0 {
				st.Delete(k - 2)
			}
			k++
		}
	}()

	var wg sync.WaitGroup
	for _, s := range snaps {
		for r := 0; r < 4; r++ {
			wg.Add(1)
			go func(s *Snapshot) {
				defer wg.Done()
				s.Close()
			}(s)
		}
		wg.Add(1)
		go func(s *Snapshot) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				// Either a value or ErrSnapshotClosed; never a panic.
				s.Get(uint64(j))
			}
		}(s)
	}
	wg.Wait()
	close(stop)
	writer.Wait()

	if err := st.CheckRefGraph(); err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if _, err := s.Get(0); err != ErrSnapshotClosed {
			t.Fatalf("closed snapshot Get err = %v, want ErrSnapshotClosed", err)
		}
	}
}

// TestRandomAgainstModel drives random puts/deletes/snapshot/close cycles
// and checks every live snapshot against a model map, plus the reference
// graph and B-tree invariants.
func TestRandomAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	st := NewStore()
	model := make(map[uint64]uint64)
	type pinned struct {
		s *Snapshot
		m map[uint64]uint64
	}
	var live []pinned

	for step := 0; step < 3000; step++ {
		k := uint64(rng.Intn(400))
		switch rng.Intn(12) {
		case 0, 1:
			_, existed := model[k]
			if got := st.Delete(k); got != existed {
				t.Fatalf("step %d: Delete(%d) = %v, want %v", step, k, got, existed)
			}
			delete(model, k)
		case 2:
			m := make(map[uint64]uint64, len(model))
			for mk, mv := range model {
				m[mk] = mv
			}
			live = append(live, pinned{st.Snapshot(), m})
		case 3:
			if len(live) > 0 {
				i := rng.Intn(len(live))
				live[i].s.Close()
				live[i].s.Close() // idempotent
				live = append(live[:i], live[i+1:]...)
			}
		default:
			v := uint64(rng.Intn(1 << 30))
			st.Put(k, v)
			model[k] = v
		}
		if step%101 == 0 {
			if err := st.CheckRefGraph(); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		}
	}

	for _, p := range live {
		for k, want := range p.m {
			mustGet(t, p.s.Get, k, want)
		}
	}
	for k := uint64(0); k < 400; k++ {
		want, ok := model[k]
		if !ok {
			mustMiss(t, st.Get, k)
		} else {
			mustGet(t, st.Get, k, want)
		}
	}
	st.mu.RLock()
	checkBTree(t, st, st.root)
	st.mu.RUnlock()

	for _, p := range live {
		p.s.Close()
	}
	if err := st.CheckRefGraph(); err != nil {
		t.Fatal(err)
	}
	if got := len(st.ReachablePages(st.RootPage())); got != len(st.AllocatedPages()) {
		t.Fatalf("%d reachable pages but %d allocated", got, len(st.AllocatedPages()))
	}
}

func difference(a, b map[PageID]bool) map[PageID]bool {
	out := make(map[PageID]bool)
	for id := range a {
		if !b[id] {
			out[id] = true
		}
	}
	return out
}

func (st *Store) isAllocated(id PageID) bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.pages[id] != nil
}
