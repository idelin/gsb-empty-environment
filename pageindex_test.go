package pageindex

import (
	"math/rand"
	"sync"
	"testing"
)

// TestSnapshotForksVersions: two snapshots forked at different epochs each
// keep seeing their own version while the writer commits on.
func TestSnapshotForksVersions(t *testing.T) {
	db := New(3)
	for k := uint64(1); k <= 50; k++ {
		db.Put(k, k*10)
	}
	s1 := db.Snapshot()
	for k := uint64(51); k <= 100; k++ {
		db.Put(k, k*10)
	}
	for k := uint64(1); k <= 50; k++ {
		db.Put(k, k*10+1) // overwrite keys s1 still sees
	}
	s2 := db.Snapshot()

	if s1.Root() == s2.Root() {
		t.Fatalf("forked snapshots share root page %d", s1.Root())
	}
	if v, ok := s1.Get(7); !ok || v != 70 {
		t.Fatalf("s1.Get(7) = %v,%v want 70,true", v, ok)
	}
	if _, ok := s1.Get(77); ok {
		t.Fatal("s1 must not see key 77")
	}
	if v, ok := s2.Get(7); !ok || v != 71 {
		t.Fatalf("s2.Get(7) = %v,%v want 71,true", v, ok)
	}
	if v, ok := s2.Get(77); !ok || v != 770 {
		t.Fatalf("s2.Get(77) = %v,%v want 770,true", v, ok)
	}

	// Writer keeps churning; the pinned version must not move.
	for k := uint64(1); k <= 100; k++ {
		db.Put(k, 0)
	}
	if v, _ := s1.Get(7); v != 70 {
		t.Fatalf("s1 polluted by later commits: Get(7) = %v", v)
	}
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2.Close()
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestDeletePreservesSnapshot: deletes never touch a pinned snapshot.
func TestDeletePreservesSnapshot(t *testing.T) {
	db := New(3)
	for k := uint64(1); k <= 100; k++ {
		db.Put(k, k)
	}
	s := db.Snapshot()
	for k := uint64(1); k <= 100; k += 2 {
		if !db.Delete(k) {
			t.Fatalf("Delete(%d) reported missing", k)
		}
	}
	for k := uint64(1); k <= 100; k++ {
		_, okCur := db.Get(k)
		_, okSnap := s.Get(k)
		if k%2 == 1 {
			if okCur || !okSnap {
				t.Fatalf("key %d: current=%v snapshot=%v, want false,true", k, okCur, okSnap)
			}
		} else if !okCur || !okSnap {
			t.Fatalf("key %d: current=%v snapshot=%v, want true,true", k, okCur, okSnap)
		}
	}
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	// Drain the tree completely; the snapshot still holds all 100 keys.
	for k := uint64(2); k <= 100; k += 2 {
		db.Delete(k)
	}
	if n := len(db.Items()); n != 0 {
		t.Fatalf("current tree has %d items, want 0", n)
	}
	if n := len(s.Items()); n != 100 {
		t.Fatalf("snapshot has %d items, want 100", n)
	}
	if db.Delete(12345) {
		t.Fatal("Delete of missing key reported true")
	}
	s.Close()
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestExplicitPageRecycling: page numbers are recycled only after the last
// snapshot that could reach them is closed.
func TestExplicitPageRecycling(t *testing.T) {
	db := New(3)
	for k := uint64(1); k <= 10; k++ {
		db.Put(k, k)
	}
	s := db.Snapshot()
	pinnedRoot := s.Root()
	pinned := Reachable(db.pager, pinnedRoot)

	// Overwrite every key: the whole old path is retired.
	for k := uint64(1); k <= 10; k++ {
		db.Put(k, k+1000)
	}

	db.pager.mu.RLock()
	for no := range pinned {
		if _, ok := db.pager.pages[no]; !ok {
			t.Fatalf("pinned page %d reclaimed while snapshot active", no)
		}
	}
	for _, no := range db.pager.free {
		if _, bad := pinned[no]; bad {
			t.Fatalf("pinned page %d on free list while snapshot active", no)
		}
	}
	db.pager.mu.RUnlock()

	s.Close()

	// With no active snapshot the pinned root itself must be recycled.
	db.pager.mu.RLock()
	if _, ok := db.pager.pages[pinnedRoot]; ok {
		t.Fatalf("page %d still allocated after last snapshot closed", pinnedRoot)
	}
	freeBefore := append([]PageNo(nil), db.pager.free...)
	nextBefore := db.pager.next
	db.pager.mu.RUnlock()
	if len(freeBefore) == 0 {
		t.Fatal("no pages recycled after last snapshot closed")
	}
	found := false
	for _, no := range freeBefore {
		if no == pinnedRoot {
			found = true
		}
	}
	if !found {
		t.Fatalf("pinned root page %d not on free list %v", pinnedRoot, freeBefore)
	}

	// The next commit must reuse recycled pages, never fresh ones.
	db.Put(999, 999)
	db.pager.mu.RLock()
	if db.pager.next != nextBefore {
		t.Fatalf("allocator consumed fresh pages: next %d -> %d", nextBefore, db.pager.next)
	}
	db.pager.mu.RUnlock()
	freeSet := make(map[PageNo]bool, len(freeBefore))
	for _, no := range freeBefore {
		freeSet[no] = true
	}
	if root := db.Root(); !freeSet[root] {
		t.Fatalf("new root page %d was not recycled from the free set", root)
	}
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestOldReadersNeverPolluted: page contents pinned by a snapshot keep their
// identity (same pointer) across hundreds of commits.
func TestOldReadersNeverPolluted(t *testing.T) {
	db := New(3)
	for k := uint64(0); k < 40; k++ {
		db.Put(k, k*3)
	}
	s := db.Snapshot()
	saved := make(map[PageNo]*node)
	db.pager.mu.RLock()
	for no := range Reachable(db.pager, s.Root()) {
		saved[no] = db.pager.pages[no]
	}
	db.pager.mu.RUnlock()

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		k := uint64(rng.Intn(120))
		if rng.Intn(2) == 0 {
			db.Put(k, uint64(i))
		} else {
			db.Delete(k)
		}
	}

	db.pager.mu.RLock()
	for no, ptr := range saved {
		if got := db.pager.pages[no]; got != ptr {
			t.Fatalf("page %d polluted: contents replaced while pinned", no)
		}
	}
	db.pager.mu.RUnlock()
	for k := uint64(0); k < 40; k++ {
		if v, ok := s.Get(k); !ok || v != k*3 {
			t.Fatalf("snapshot Get(%d) = %v,%v want %d,true", k, v, ok, k*3)
		}
	}
	s.Close()
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentCloseAndCommit: writers commit while readers fork snapshots
// and many goroutines close the same snapshot concurrently.
func TestConcurrentCloseAndCommit(t *testing.T) {
	db := New(4)
	for k := uint64(0); k < 200; k++ {
		db.Put(k, k)
	}
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 400; i++ {
				k := uint64(rng.Intn(400))
				if rng.Intn(3) == 0 {
					db.Delete(k)
				} else {
					db.Put(k, uint64(i))
				}
			}
		}(w)
	}
	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s := db.Snapshot()
				s.Get(uint64(i % 200))
				_ = s.Items()
				var swg sync.WaitGroup
				for g := 0; g < 3; g++ {
					swg.Add(1)
					go func() { defer swg.Done(); s.Close() }()
				}
				swg.Wait()
			}
		}()
	}
	wg.Wait()

	// Closing again after everything must stay a no-op.
	s := db.Snapshot()
	for i := 0; i < 5; i++ {
		s.Close()
	}
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	db.mu.Lock()
	if len(db.snaps) != 0 {
		t.Fatalf("snapshot registry not empty: %v", db.snaps)
	}
	db.mu.Unlock()
}

// TestRandomizedInvariantCheck: random interleaving of puts, deletes,
// snapshot forks and closes, with the reference-graph checker and full
// snapshot content verification.
func TestRandomizedInvariantCheck(t *testing.T) {
	db := New(3)
	rng := rand.New(rand.NewSource(42))
	type openSnap struct {
		s    *Snapshot
		want map[uint64]uint64
	}
	cur := make(map[uint64]uint64)
	var open []openSnap

	verify := func(s *Snapshot, want map[uint64]uint64) {
		t.Helper()
		for k := uint64(0); k < 150; k++ {
			wv, wok := want[k]
			gv, gok := s.Get(k)
			if wv != gv || wok != gok {
				t.Fatalf("snapshot mismatch key %d: want %v,%v got %v,%v", k, wv, wok, gv, gok)
			}
		}
	}

	for i := 0; i < 3000; i++ {
		switch rng.Intn(8) {
		case 0, 1, 2, 3:
			k := uint64(rng.Intn(150))
			v := uint64(rng.Intn(1000))
			db.Put(k, v)
			cur[k] = v
		case 4, 5:
			k := uint64(rng.Intn(150))
			db.Delete(k)
			delete(cur, k)
		case 6:
			want := make(map[uint64]uint64, len(cur))
			for k, v := range cur {
				want[k] = v
			}
			open = append(open, openSnap{s: db.Snapshot(), want: want})
		case 7:
			if len(open) > 0 {
				j := rng.Intn(len(open))
				verify(open[j].s, open[j].want)
				open[j].s.Close()
				open = append(open[:j], open[j+1:]...)
			}
		}
		if i%97 == 0 {
			if err := db.CheckInvariants(); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		}
	}
	for _, o := range open {
		verify(o.s, o.want)
		o.s.Close()
	}
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestIteratorOrder: Items returns keys in sorted order.
func TestIteratorOrder(t *testing.T) {
	db := New(2)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		db.Put(uint64(rng.Intn(1000)), uint64(i))
	}
	items := db.Items()
	for i := 1; i < len(items); i++ {
		if items[i-1].Key >= items[i].Key {
			t.Fatalf("items out of order at %d: %v then %v", i, items[i-1], items[i])
		}
	}
	if err := db.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}
