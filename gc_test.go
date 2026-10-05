package blockgc

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func putChain(s *Store, ids ...BlockID) {
	for i, id := range ids {
		var refs []BlockID
		if i+1 < len(ids) {
			refs = []BlockID{ids[i+1]}
		}
		s.Put(Block{ID: id, Refs: refs})
	}
}

func mustLive(t *testing.T, s *Store, ids ...BlockID) {
	t.Helper()
	for _, id := range ids {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("block %s missing, want present", id)
		}
		if s.IsQuarantined(id) {
			t.Fatalf("block %s quarantined, want live", id)
		}
	}
}

func mustDeleted(t *testing.T, s *Store, ids ...BlockID) {
	t.Helper()
	for _, id := range ids {
		if _, ok := s.Get(id); ok {
			t.Fatalf("block %s present, want deleted", id)
		}
	}
}

func newCluster() (*Store, *Manifest, *MemJournal, *Collector) {
	s := NewStore()
	m := NewManifest()
	j := NewMemJournal()
	return s, m, j, NewCollector(s, m, j)
}

// TestSharedBlocks: blocks shared between two roots must survive the
// revocation of either root; only truly unreferenced blocks are collected.
func TestSharedBlocks(t *testing.T) {
	s, m, _, c := newCluster()
	// rootA -> a1 -> shared -> s1 ; rootB -> b1 -> shared
	s.Put(Block{ID: "shared", Refs: []BlockID{"s1"}})
	s.Put(Block{ID: "s1"})
	s.Put(Block{ID: "a1", Refs: []BlockID{"shared"}})
	s.Put(Block{ID: "b1", Refs: []BlockID{"shared"}})
	s.Put(Block{ID: "rootA", Refs: []BlockID{"a1"}})
	s.Put(Block{ID: "rootB", Refs: []BlockID{"b1"}})
	s.Put(Block{ID: "garbage"})
	m.Publish("A", "rootA")
	m.Publish("B", "rootB")

	m.Revoke("A")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	mustLive(t, s, "rootB", "b1", "shared", "s1")
	mustDeleted(t, s, "rootA", "a1", "garbage")

	m.Revoke("B")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	mustDeleted(t, s, "rootB", "b1", "shared", "s1")
}

// TestCyclicAndBadReferences: cycles must not hang the traversal; reachable
// cycles survive, unreachable cycles are collected, and references to
// missing blocks are tolerated.
func TestCyclicAndBadReferences(t *testing.T) {
	s, m, _, c := newCluster()
	// Reachable cycle with a self-loop and a dangling ref.
	s.Put(Block{ID: "root", Refs: []BlockID{"x", "missing"}})
	s.Put(Block{ID: "x", Refs: []BlockID{"y"}})
	s.Put(Block{ID: "y", Refs: []BlockID{"x", "y"}})
	// Unreachable cycle.
	s.Put(Block{ID: "p", Refs: []BlockID{"q"}})
	s.Put(Block{ID: "q", Refs: []BlockID{"p", "gone"}})
	m.Publish("main", "root")

	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	mustLive(t, s, "root", "x", "y")
	mustDeleted(t, s, "p", "q")
}

// TestRootRevocationWithPin: a revoked root keeps its graph alive while a
// reader still pins the old manifest view; collection happens after Unpin.
func TestRootRevocationWithPin(t *testing.T) {
	s, m, _, c := newCluster()
	putChain(s, "oldRoot", "old1", "old2")
	m.Publish("data", "oldRoot")

	// Reader loads the manifest and keeps working from the old root.
	pin, heads := m.Pin()
	if len(heads) != 1 || heads[0] != "oldRoot" {
		t.Fatalf("unexpected pin heads: %v", heads)
	}

	putChain(s, "newRoot", "new1")
	m.Publish("data", "newRoot") // old root replaced
	m.Revoke("data")             // ... and then the name is revoked entirely

	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	// Pinned old graph must survive even though its root was revoked.
	mustLive(t, s, "oldRoot", "old1", "old2")
	mustDeleted(t, s, "newRoot", "new1")

	m.Unpin(pin)
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	mustDeleted(t, s, "oldRoot", "old1", "old2")
}

// TestRootPublishedDuringScan: a root published while the collector is
// marking must be picked up by the barrier, not swept.
func TestRootPublishedDuringScan(t *testing.T) {
	s, m, j, c := newCluster()
	putChain(s, "stable", "stable1")
	m.Publish("stable", "stable")
	// Not yet reachable from any root.
	putChain(s, "lateRoot", "late1", "late2")

	j.AfterSave = func(p Phase) {
		if p == PhaseMark {
			m.Publish("late", "lateRoot") // publish mid-scan
		}
	}
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	mustLive(t, s, "stable", "stable1", "lateRoot", "late1", "late2")
}

// TestReferenceAddedAfterScan: a new block referencing a quarantined block,
// published after the sweep decision, must force the collector to abort and
// restore rather than delete reachable data.
func TestReferenceAddedAfterScan(t *testing.T) {
	s, m, j, c := newCluster()
	putChain(s, "stable", "stable1")
	m.Publish("stable", "stable")
	putChain(s, "victim", "victim1") // unreachable: will be swept

	j.AfterSave = func(p Phase) {
		if p == PhaseSweep {
			// Sweep list is decided; now a writer makes "victim" reachable.
			s.Put(Block{ID: "fresh", Refs: []BlockID{"victim"}})
			m.Publish("fresh", "fresh")
		}
	}
	err := c.Run()
	if !errors.Is(err, ErrQuarantineConflict) {
		t.Fatalf("want ErrQuarantineConflict, got %v", err)
	}
	mustLive(t, s, "stable", "stable1", "victim", "victim1", "fresh")

	// A follow-up collection with no concurrent writes succeeds and keeps
	// the now-reachable graph.
	j.AfterSave = nil
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	mustLive(t, s, "victim", "victim1", "fresh")
}

// TestWriteBarrierResurrects: the write barrier transitively restores
// quarantined blocks referenced by a new write.
func TestWriteBarrierResurrects(t *testing.T) {
	s, m, _, c := newCluster()
	putChain(s, "a", "b", "c")
	m.Publish("keep", "a")
	s.Quarantine("b", "c")

	c.mu.Lock()
	c.active = true
	c.pending = make(map[BlockID]struct{})
	c.mu.Unlock()

	s.Put(Block{ID: "new", Refs: []BlockID{"b"}})
	mustLive(t, s, "b", "c") // b restored directly, c transitively via b
}

// TestCrashBeforeConfirm: crash after the quarantine list is journaled but
// before deletion is confirmed; recovery must restore and rerun.
func TestCrashBeforeConfirm(t *testing.T) {
	s, m, j, c := newCluster()
	putChain(s, "root", "live1")
	s.Put(Block{ID: "garbage"})
	m.Publish("main", "root")

	// Simulate a crash: quarantine journaled (and applied), no confirmation.
	crash := errors.New("simulated crash")
	j.AfterSave = func(p Phase) {
		if p == PhaseSweep {
			panic(crash)
		}
	}
	func() {
		defer func() {
			if r := recover(); r != crash {
				t.Fatalf("unexpected panic: %v", r)
			}
		}()
		_ = c.Run()
	}()
	// Journal holds an unconfirmed sweep; store may or may not have applied
	// the quarantine depending on the exact crash point.
	s.Quarantine("garbage") // crash landed after quarantine was applied

	// Recover with a fresh collector over the same journal.
	c2 := NewCollector(s, m, j)
	j.AfterSave = nil
	resumed, err := c2.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if !resumed {
		t.Fatal("expected resume to find an interrupted collection")
	}
	mustLive(t, s, "root", "live1")
	mustDeleted(t, s, "garbage") // rolled back, then collected by the rerun
}

// TestCrashAfterConfirm: crash after the confirmation record is written but
// before deletion finishes; recovery must complete the deletion.
func TestCrashAfterConfirm(t *testing.T) {
	s, m, j, c := newCluster()
	putChain(s, "root", "live1")
	s.Put(Block{ID: "garbage"})
	m.Publish("main", "root")

	crash := errors.New("simulated crash")
	j.AfterSave = func(p Phase) {
		if p == PhaseDelete {
			panic(crash) // confirmation record is durable; delete never ran
		}
	}
	func() {
		defer func() {
			if r := recover(); r != crash {
				t.Fatalf("unexpected panic: %v", r)
			}
		}()
		_ = c.Run()
	}()
	// "garbage" is quarantined and confirmed, but not yet deleted.
	if !s.IsQuarantined("garbage") {
		t.Fatal("garbage should be quarantined at crash point")
	}

	c2 := NewCollector(s, m, j)
	j.AfterSave = nil
	resumed, err := c2.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if !resumed {
		t.Fatal("expected resume to find an interrupted collection")
	}
	mustLive(t, s, "root", "live1")
	mustDeleted(t, s, "garbage")
}

// TestFileJournalCrashRecovery: same recovery flow, but the journal is a
// real file that survives the "process" (collector instance) restart.
func TestFileJournalCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	s := NewStore()
	m := NewManifest()
	putChain(s, "root", "live1")
	s.Put(Block{ID: "garbage"})
	m.Publish("main", "root")

	path := filepath.Join(dir, "gc-journal.json")
	c1 := NewCollector(s, m, NewFileJournal(path))
	// Crash after confirm by making the store delete unreachable... instead
	// run to completion but simulate crash by saving a confirmed state.
	if err := c1.Run(); err != nil {
		t.Fatal(err)
	}
	mustDeleted(t, s, "garbage")

	// Simulate a crash mid-delete: journal says confirmed, block still there.
	s.Put(Block{ID: "garbage2"})
	s.Quarantine("garbage2")
	j := NewFileJournal(path)
	if err := j.Save(&State{Phase: PhaseDelete, Quarantine: []BlockID{"garbage2"}, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	c2 := NewCollector(s, m, j)
	resumed, err := c2.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if !resumed {
		t.Fatal("expected resume")
	}
	mustDeleted(t, s, "garbage2")
	mustLive(t, s, "root", "live1")
}

// TestConcurrentPublishAndCollect: publishers and writers race a running
// collector; the invariant is that everything reachable from the final
// manifest (and pins) is present afterwards.
func TestConcurrentPublishAndCollect(t *testing.T) {
	s, m, _, c := newCluster()
	putChain(s, "genesis")
	m.Publish("root-0", "genesis")

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				id := BlockID(fmt.Sprintf("w%d-b%d", w, i))
				s.Put(Block{ID: id})
				m.Publish(fmt.Sprintf("root-%d", w), id)
				if i%3 == 0 {
					pin, _ := m.Pin()
					m.Unpin(pin)
				}
			}
		}(w)
	}
	for i := 0; i < 10; i++ {
		if err := c.Run(); err != nil && !errors.Is(err, ErrQuarantineConflict) {
			t.Fatalf("unexpected GC error: %v", err)
		}
	}
	wg.Wait()
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}

	// Invariant: every active head resolves to a fully present graph.
	bad := Verify(s, m, map[BlockID]bool{})
	if len(bad) > 0 {
		t.Fatalf("reachable blocks missing after concurrent GC: %v", bad)
	}
	if missing := missingRefs(s, m.ActiveHeads()); len(missing) > 0 {
		t.Fatalf("reachable blocks deleted under concurrent GC: %v", missing)
	}
}

// missingRefs traverses from roots and returns referenced block IDs that are
// absent from the store.
func missingRefs(s *Store, roots []BlockID) []BlockID {
	seen := make(map[BlockID]bool)
	var missing []BlockID
	stack := append([]BlockID(nil), roots...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		b, ok := s.Get(id)
		if !ok {
			missing = append(missing, id)
			continue
		}
		stack = append(stack, b.Refs...)
	}
	return missing
}
