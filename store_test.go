package migsim

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// receiptRecorder captures Switch receipts from the event stream.
type receiptRecorder struct {
	mu       sync.Mutex
	receipts []Receipt
}

func (r *receiptRecorder) handle(e Event) {
	if e.Kind == EventSwitched {
		r.mu.Lock()
		r.receipts = append(r.receipts, e.Receipt)
		r.mu.Unlock()
	}
}

func (r *receiptRecorder) last() Receipt {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.receipts) == 0 {
		return Receipt{}
	}
	return r.receipts[len(r.receipts)-1]
}

func newTestStore() (*Store, *Volume, *Volume, *Ledger) {
	oldV := NewVolume("old")
	newV := NewVolume("new")
	s := NewStore(oldV, newV)
	led := NewLedger()
	s.Subscribe(led.Handle)
	return s, oldV, newV, led
}

// A migration switch is a per-block generational address flip: references
// added before, during, and after the copy all resolve correctly, handles
// stay pinned to their replica, and the old replica is reclaimed only after
// the last old handle is released.
func TestSwitchPinsHandlesAndReclaims(t *testing.T) {
	s, oldV, newV, led := newTestStore()
	data := []byte("shared-block-contents")
	if err := s.Put("blk", "obj1", data, "old"); err != nil {
		t.Fatal(err)
	}

	h1, err := s.Open("blk") // pins gen 0 on the old volume
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginMigration("blk", "new"); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	s.CopyHook = func(BlockID) { close(entered); <-release }
	migrateDone := make(chan error, 1)
	go func() { migrateDone <- s.Migrate("blk") }()
	<-entered // copy is in flight, blocked at the barrier

	// References can be added and removed while the copy is in flight.
	if err := s.AddRef("obj2", "blk"); err != nil {
		t.Fatal(err)
	}
	h2, err := s.Open("blk") // still pins gen 0: no switch yet
	if err != nil {
		t.Fatal(err)
	}
	if h2.Gen() != 0 {
		t.Fatalf("handle opened during copy got gen %d, want 0", h2.Gen())
	}

	close(release)
	if err := <-migrateDone; err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if g, _ := s.Generation("blk"); g != 1 {
		t.Fatalf("generation = %d, want 1", g)
	}
	ref, _ := s.Replica("blk")
	if ref.Volume != "new" {
		t.Fatalf("current replica on %q, want new", ref.Volume)
	}

	// New handles after the switch pin the new replica on the new volume.
	h3, err := s.Open("blk")
	if err != nil {
		t.Fatal(err)
	}
	if h3.Gen() != 1 {
		t.Fatalf("post-switch handle gen = %d, want 1", h3.Gen())
	}

	// The old handle still reads the old replica, which must survive
	// reclamation sweeps while pinned.
	b1, err := h1.Read()
	if err != nil || !bytes.Equal(b1, data) {
		t.Fatalf("old handle read = %v, %v", b1, err)
	}
	s.Reclaim()
	if !oldV.Has("blk@0") {
		t.Fatal("old replica reclaimed while still pinned by a handle")
	}

	// Releasing the old handles frees the old replica.
	h1.Close()
	h2.Close()
	if oldV.Has("blk@0") {
		t.Fatal("old replica not reclaimed after handles closed")
	}
	if !newV.Has("blk@1") {
		t.Fatal("new replica missing after switch")
	}
	b3, err := h3.Read()
	if err != nil || !bytes.Equal(b3, data) {
		t.Fatalf("new handle read = %v, %v", b3, err)
	}
	h3.Close()

	if err := s.RemoveRef("obj1", "blk"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRef("obj2", "blk"); err != nil {
		t.Fatal(err)
	}
	s.Reclaim()
	if keys := oldV.Keys(); len(keys) != 0 {
		t.Fatalf("old volume not empty: %v", keys)
	}
	if keys := newV.Keys(); len(keys) != 0 {
		t.Fatalf("new volume not empty: %v", keys)
	}
	if err := led.Verify(s.Volumes()...); err != nil {
		t.Fatal(err)
	}
}

// Replaying an old receipt is a no-op: it never switches back, never
// duplicates a switch, and never disturbs a later generation.
func TestReceiptReplayIsNoOp(t *testing.T) {
	s, oldV, newV, led := newTestStore()
	rec := &receiptRecorder{}
	s.Subscribe(rec.handle)
	data := []byte("replayed-contents")
	if err := s.Put("blk", "obj1", data, "old"); err != nil {
		t.Fatal(err)
	}

	// First migration: old -> new, gen 0 -> 1.
	if err := s.BeginMigration("blk", "new"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate("blk"); err != nil {
		t.Fatal(err)
	}
	r1 := rec.last()
	if r1.FromGen != 0 || r1.ToGen != 1 {
		t.Fatalf("receipt r1 = %+v, want 0->1", r1)
	}
	s.Reclaim()

	// Replay r1: no-op, state unchanged.
	if err := s.ApplyReceipt(r1); err != nil {
		t.Fatalf("replay r1: %v", err)
	}
	if g, _ := s.Generation("blk"); g != 1 {
		t.Fatalf("gen after replay = %d, want 1", g)
	}

	// Second migration: new -> old, gen 1 -> 2.
	if err := s.BeginMigration("blk", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate("blk"); err != nil {
		t.Fatal(err)
	}
	r2 := rec.last()
	if r2.FromGen != 1 || r2.ToGen != 2 {
		t.Fatalf("receipt r2 = %+v, want 1->2", r2)
	}
	s.Reclaim()

	// Replay both old receipts: still no-ops, generation stays 2.
	if err := s.ApplyReceipt(r1); err != nil {
		t.Fatalf("replay r1 again: %v", err)
	}
	if err := s.ApplyReceipt(r2); err != nil {
		t.Fatalf("replay r2: %v", err)
	}
	if g, _ := s.Generation("blk"); g != 2 {
		t.Fatalf("gen after replays = %d, want 2", g)
	}
	if !oldV.Has("blk@2") || len(oldV.Keys()) != 1 || len(newV.Keys()) != 0 {
		t.Fatalf("unexpected replicas: old=%v new=%v", oldV.Keys(), newV.Keys())
	}
	if err := led.Verify(s.Volumes()...); err != nil {
		t.Fatal(err)
	}
}

// Cancelling while the copy is in flight aborts the migration: no switch,
// no staged copy left behind, and the block keeps serving the old replica.
func TestCancelDuringCopy(t *testing.T) {
	s, oldV, newV, led := newTestStore()
	data := []byte("cancel-me")
	if err := s.Put("blk", "obj1", data, "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginMigration("blk", "new"); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	s.CopyHook = func(BlockID) { close(entered); <-release }
	done := make(chan error, 1)
	go func() { done <- s.Migrate("blk") }()
	<-entered

	if err := s.CancelMigration("blk"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrCancelled) {
		t.Fatalf("migrate err = %v, want ErrCancelled", err)
	}

	if g, _ := s.Generation("blk"); g != 0 {
		t.Fatalf("gen after cancel = %d, want 0", g)
	}
	if keys := newV.Keys(); len(keys) != 0 {
		t.Fatalf("staged copy left on new volume: %v", keys)
	}
	if !oldV.Has("blk@0") {
		t.Fatal("old replica lost after cancel")
	}

	// Cancelling again reports no active migration; the switch never reverts.
	if err := s.CancelMigration("blk"); !errors.Is(err, ErrNotMigrating) {
		t.Fatalf("second cancel = %v, want ErrNotMigrating", err)
	}

	// The block is still fully usable and a later migration succeeds.
	s.CopyHook = nil
	if err := s.BeginMigration("blk", "new"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate("blk"); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Generation("blk"); g != 1 {
		t.Fatalf("gen after retry = %d, want 1", g)
	}
	s.Reclaim()
	if err := led.Verify(s.Volumes()...); err != nil {
		t.Fatal(err)
	}
}

// A digest failure (corrupted copy) must never switch the generation, and
// the failed staged copy must be cleaned up.
func TestDigestMismatchPreventsSwitch(t *testing.T) {
	s, oldV, newV, led := newTestStore()
	data := []byte("integrity-matters")
	if err := s.Put("blk", "obj1", data, "old"); err != nil {
		t.Fatal(err)
	}
	s.Corrupt = func(_ BlockID, b []byte) []byte {
		c := append([]byte(nil), b...)
		c[0] ^= 0xff
		return c
	}
	if err := s.BeginMigration("blk", "new"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate("blk"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("migrate err = %v, want ErrDigestMismatch", err)
	}
	if g, _ := s.Generation("blk"); g != 0 {
		t.Fatalf("gen after digest failure = %d, want 0", g)
	}
	if keys := newV.Keys(); len(keys) != 0 {
		t.Fatalf("corrupt copy left on new volume: %v", keys)
	}
	h, err := s.Open("blk")
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.Read()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("content after failed migration = %v, %v", got, err)
	}
	h.Close()
	if !oldV.Has("blk@0") {
		t.Fatal("old replica lost after digest failure")
	}
	if err := led.Verify(s.Volumes()...); err != nil {
		t.Fatal(err)
	}
}

// Removing the last reference mid-copy deletes the block and cancels the
// migration; replicas are reclaimed once pinned handles drain.
func TestDeleteDuringMigration(t *testing.T) {
	s, oldV, newV, led := newTestStore()
	data := []byte("doomed-block")
	if err := s.Put("blk", "obj1", data, "old"); err != nil {
		t.Fatal(err)
	}
	h, err := s.Open("blk")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginMigration("blk", "new"); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	s.CopyHook = func(BlockID) { close(entered); <-release }
	done := make(chan error, 1)
	go func() { done <- s.Migrate("blk") }()
	<-entered

	if err := s.RemoveRef("obj1", "blk"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrCancelled) {
		t.Fatalf("migrate err = %v, want ErrCancelled", err)
	}

	if keys := newV.Keys(); len(keys) != 0 {
		t.Fatalf("staged copy left on new volume: %v", keys)
	}
	if _, err := s.Open("blk"); !errors.Is(err, ErrDeleted) {
		t.Fatalf("open on deleted block = %v, want ErrDeleted", err)
	}
	if err := s.AddRef("obj2", "blk"); !errors.Is(err, ErrDeleted) {
		t.Fatalf("add ref on deleted block = %v, want ErrDeleted", err)
	}

	// The pinned handle keeps the old replica alive until released.
	if !oldV.Has("blk@0") {
		t.Fatal("old replica reclaimed while pinned")
	}
	if _, err := h.Read(); err != nil {
		t.Fatalf("pinned handle read: %v", err)
	}
	h.Close()
	if oldV.Has("blk@0") {
		t.Fatal("old replica not reclaimed after last handle closed")
	}
	if err := led.Verify(s.Volumes()...); err != nil {
		t.Fatal(err)
	}
}

// Stress: concurrent reference add/remove and handle open/read/close while a
// migration copies with a barrier, then the ledger confirms the final
// reference-to-replica relationship. Run with -race.
func TestConcurrentRefsDuringMigration(t *testing.T) {
	s, _, _, led := newTestStore()
	data := []byte("hot-block")
	if err := s.Put("blk", "obj0", data, "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginMigration("blk", "new"); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s.CopyHook = func(BlockID) { once.Do(func() { close(entered) }); <-release }
	done := make(chan error, 1)
	go func() { done <- s.Migrate("blk") }()
	<-entered

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			obj := ObjectID(fmt.Sprintf("obj-%d", i))
			for j := 0; j < 50; j++ {
				if err := s.AddRef(obj, "blk"); err != nil {
					t.Errorf("addref: %v", err)
					return
				}
				if h, err := s.Open("blk"); err == nil {
					if b, rerr := h.Read(); rerr != nil || !bytes.Equal(b, data) {
						t.Errorf("read = %v, %v", b, rerr)
					}
					h.Close()
				} else {
					t.Errorf("open: %v", err)
				}
				if err := s.RemoveRef(obj, "blk"); err != nil {
					t.Errorf("removeref: %v", err)
					return
				}
			}
		}(i)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("migrate: %v", err)
	}
	wg.Wait()

	if g, _ := s.Generation("blk"); g != 1 {
		t.Fatalf("gen = %d, want 1", g)
	}
	s.Reclaim()
	if err := led.Verify(s.Volumes()...); err != nil {
		t.Fatal(err)
	}
}
