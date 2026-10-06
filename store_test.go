package objstore

import (
	"testing"
	"time"
)

const testGrace = 10 * time.Second

func newTestStore() (*Store, *FakeClock, time.Time) {
	start := time.Unix(1700000000, 0)
	clk := NewFakeClock(start)
	return NewStore(clk, testGrace), clk, start
}

func mustCreate(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.Create(id); err != nil {
		t.Fatalf("Create(%q): %v", id, err)
	}
}

func mustInfo(t *testing.T, s *Store, id string) Info {
	t.Helper()
	info, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q): not found", id)
	}
	return info
}

func TestDeleteLifecycle(t *testing.T) {
	s, clk, start := newTestStore()
	mustCreate(t, s, "a")

	r, err := s.AddRef("a")
	if err != nil {
		t.Fatalf("AddRef: %v", err)
	}
	if info := mustInfo(t, s, "a"); info.Refs != 1 || info.Generation != 1 || info.State != Active {
		t.Fatalf("unexpected info: %+v", info)
	}
	r.Release()

	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	info := mustInfo(t, s, "a")
	if info.State != PendingDelete {
		t.Fatalf("state = %v, want PendingDelete", info.State)
	}
	if want := start.Add(testGrace); !info.DeleteAt.Equal(want) {
		t.Fatalf("DeleteAt = %v, want %v", info.DeleteAt, want)
	}

	clk.Advance(testGrace - time.Nanosecond)
	if info := mustInfo(t, s, "a"); info.State != PendingDelete {
		t.Fatalf("before deadline state = %v, want PendingDelete", info.State)
	}

	clk.Advance(time.Nanosecond)
	if info := mustInfo(t, s, "a"); info.State != Deleted || info.Refs != 0 {
		t.Fatalf("after grace info = %+v, want Deleted with 0 refs", info)
	}

	if _, err := s.AddRef("a"); err != ErrDeleted {
		t.Fatalf("AddRef after deletion = %v, want ErrDeleted", err)
	}
	if err := s.Create("a"); err != ErrDeleted {
		t.Fatalf("Create after deletion = %v, want ErrDeleted", err)
	}
	if err := s.Delete("a"); err != ErrDeleted {
		t.Fatalf("Delete after deletion = %v, want ErrDeleted", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	s, clk, _ := newTestStore()
	mustCreate(t, s, "a")

	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	first := mustInfo(t, s, "a").DeleteAt

	clk.Advance(time.Second)
	if err := s.Delete("a"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if got := mustInfo(t, s, "a").DeleteAt; !got.Equal(first) {
		t.Fatalf("DeleteAt moved to %v, want %v (idempotent)", got, first)
	}
}

func TestReviveDuringGraceBumpsGeneration(t *testing.T) {
	s, clk, _ := newTestStore()
	mustCreate(t, s, "a")

	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	clk.Advance(4 * time.Second)

	r, err := s.AddRef("a")
	if err != nil {
		t.Fatalf("AddRef during grace: %v", err)
	}
	if r.Generation() != 2 {
		t.Fatalf("ref generation = %d, want 2", r.Generation())
	}
	info := mustInfo(t, s, "a")
	if info.State != Active || info.Generation != 2 || info.Refs != 1 || !info.DeleteAt.IsZero() {
		t.Fatalf("after revive info = %+v, want Active gen 2 refs 1", info)
	}

	clk.Advance(time.Hour)
	if info := mustInfo(t, s, "a"); info.State != Active {
		t.Fatalf("stale sweep deleted revived object: %+v", info)
	}

	r.Release()
	if err := s.Delete("a"); err != nil {
		t.Fatalf("re-Delete: %v", err)
	}
	clk.Advance(testGrace)
	if info := mustInfo(t, s, "a"); info.State != Deleted || info.Generation != 2 {
		t.Fatalf("final info = %+v, want Deleted gen 2", info)
	}
}

func TestStaleSweepOnlyAffectsOriginalGeneration(t *testing.T) {
	s, clk, start := newTestStore()
	mustCreate(t, s, "a")

	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	clk.Advance(6 * time.Second)
	r, err := s.AddRef("a")
	if err != nil {
		t.Fatalf("AddRef revive: %v", err)
	}
	r.Release()
	if err := s.Delete("a"); err != nil {
		t.Fatalf("re-Delete: %v", err)
	}

	clk.Advance(4 * time.Second)
	if got := clk.Now(); !got.Equal(start.Add(testGrace)) {
		t.Fatalf("now = %v, want %v (first sweep fire time)", got, start.Add(testGrace))
	}
	if info := mustInfo(t, s, "a"); info.State != PendingDelete || info.Generation != 2 {
		t.Fatalf("stale sweep took effect on new generation: %+v", info)
	}

	clk.Advance(6 * time.Second)
	if info := mustInfo(t, s, "a"); info.State != Deleted {
		t.Fatalf("info = %+v, want Deleted after second sweep", info)
	}
}

func TestSweepWaitsForReferencesToDrain(t *testing.T) {
	s, clk, _ := newTestStore()
	mustCreate(t, s, "a")

	holder, err := s.AddRef("a")
	if err != nil {
		t.Fatalf("AddRef: %v", err)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	clk.Advance(testGrace)
	if info := mustInfo(t, s, "a"); info.State != PendingDelete || info.Refs != 1 {
		t.Fatalf("sweep finalized with live refs: %+v", info)
	}

	if _, err := s.AddRef("a"); err != ErrDeleted {
		t.Fatalf("AddRef past deadline = %v, want ErrDeleted", err)
	}

	holder.Release()
	if info := mustInfo(t, s, "a"); info.State != Deleted || info.Refs != 0 {
		t.Fatalf("info = %+v, want Deleted with 0 refs after last release", info)
	}
}

func TestAddRefBoundaryAtDeadline(t *testing.T) {
	s, clk, _ := newTestStore()

	mustCreate(t, s, "before")
	if err := s.Delete("before"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	clk.Advance(testGrace - time.Nanosecond)
	if _, err := s.AddRef("before"); err != nil {
		t.Fatalf("AddRef 1ns before deadline: %v", err)
	}

	mustCreate(t, s, "at")
	holder, err := s.AddRef("at")
	if err != nil {
		t.Fatalf("AddRef: %v", err)
	}
	if err := s.Delete("at"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	clk.Advance(testGrace)
	if _, err := s.AddRef("at"); err != ErrDeleted {
		t.Fatalf("AddRef exactly at deadline = %v, want ErrDeleted", err)
	}
	holder.Release()
	if info := mustInfo(t, s, "at"); info.State != Deleted {
		t.Fatalf("info = %+v, want Deleted", info)
	}
}

func TestLedgerReconciliation(t *testing.T) {
	s, clk, start := newTestStore()
	mustCreate(t, s, "a")
	mustCreate(t, s, "b")
	mustCreate(t, s, "c")

	ra1, err := s.AddRef("a")
	if err != nil {
		t.Fatalf("AddRef: %v", err)
	}
	ra2, err := s.AddRef("a")
	if err != nil {
		t.Fatalf("AddRef: %v", err)
	}
	rb, err := s.AddRef("b")
	if err != nil {
		t.Fatalf("AddRef: %v", err)
	}
	if err := s.Delete("c"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ledger := s.Ledger()
	if len(ledger) != 3 || ledger[0].ID != "a" || ledger[1].ID != "b" || ledger[2].ID != "c" {
		t.Fatalf("ledger not sorted by id: %+v", ledger)
	}
	if e := ledger[0]; e.State != Active || e.Generation != 1 || e.Refs != 2 {
		t.Fatalf("ledger[a] = %+v, want Active gen 1 refs 2", e)
	}
	if e := ledger[1]; e.State != Active || e.Generation != 1 || e.Refs != 1 {
		t.Fatalf("ledger[b] = %+v, want Active gen 1 refs 1", e)
	}
	if e := ledger[2]; e.State != PendingDelete || e.Refs != 0 || !e.DeleteAt.Equal(start.Add(testGrace)) {
		t.Fatalf("ledger[c] = %+v, want PendingDelete refs 0", e)
	}

	clk.Advance(testGrace)
	if e := s.Ledger()[2]; e.State != Deleted || e.Refs != 0 {
		t.Fatalf("ledger[c] = %+v, want Deleted refs 0", e)
	}

	ra1.Release()
	ra2.Release()
	rb.Release()
	total := 0
	for _, e := range s.Ledger() {
		if e.Refs < 0 {
			t.Fatalf("negative refs in ledger: %+v", e)
		}
		total += e.Refs
	}
	if total != 0 {
		t.Fatalf("total refs = %d, want 0 after all releases", total)
	}
}

func TestRefReleaseIsIdempotent(t *testing.T) {
	s, _, _ := newTestStore()
	mustCreate(t, s, "a")

	r, err := s.AddRef("a")
	if err != nil {
		t.Fatalf("AddRef: %v", err)
	}
	r.Release()
	r.Release()
	if info := mustInfo(t, s, "a"); info.Refs != 0 {
		t.Fatalf("refs = %d, want 0 (double release must not corrupt ledger)", info.Refs)
	}
}

func TestUnknownAndDuplicateObjects(t *testing.T) {
	s, _, _ := newTestStore()

	if _, err := s.AddRef("ghost"); err != ErrNotFound {
		t.Fatalf("AddRef(ghost) = %v, want ErrNotFound", err)
	}
	if err := s.Delete("ghost"); err != ErrNotFound {
		t.Fatalf("Delete(ghost) = %v, want ErrNotFound", err)
	}
	if _, ok := s.Get("ghost"); ok {
		t.Fatal("Get(ghost) = ok, want not found")
	}

	mustCreate(t, s, "a")
	if err := s.Create("a"); err != ErrAlreadyExists {
		t.Fatalf("Create duplicate = %v, want ErrAlreadyExists", err)
	}
}
