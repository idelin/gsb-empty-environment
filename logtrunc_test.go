package logtrunc

import (
	"errors"
	"fmt"
	"testing"
)

// newStore builds n contiguous segments of segSize entries: [1..s], ...
func newStore(segSize uint64, n int) *MemStore {
	store := NewMemStore()
	for i := 0; i < n; i++ {
		start := uint64(i)*segSize + 1
		if err := store.Append(Segment{ID: SegmentID(fmt.Sprintf("seg-%03d", i+1)), Start: start, End: start + segSize - 1}); err != nil {
			panic(err)
		}
	}
	return store
}

func newPlanner(store *MemStore, reg *Registry, snaps *Snapshots) *Planner {
	return &Planner{Store: store, Reg: reg, Snaps: snaps}
}

func deletableIDs(plan Plan) map[SegmentID]bool {
	out := map[SegmentID]bool{}
	for _, seg := range plan.Deletable {
		out[seg.ID] = true
	}
	return out
}

func retainedByID(plan Plan, id SegmentID) *RetainedSegment {
	for i := range plan.Retained {
		if plan.Retained[i].Segment.ID == id {
			return &plan.Retained[i]
		}
	}
	return nil
}

// A confirmation gap must not be masked by a larger confirmed sequence
// number: confirming {1,2,4} leaves the horizon at 2 and keeps the
// segment containing the gap.
func TestGapNotMaskedByMaxSeq(t *testing.T) {
	store := newStore(2, 2) // [1,2], [3,4]
	reg := NewRegistry(store.End)
	if err := reg.Register("c1"); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{1, 2, 4} {
		if err := reg.Confirm("c1", seq); err != nil {
			t.Fatal(err)
		}
	}
	if horizon, _ := reg.Horizon("c1"); horizon != 2 {
		t.Fatalf("horizon = %d, want 2 (gap at 3 must not be masked by 4)", horizon)
	}
	plan := newPlanner(store, reg, NewSnapshots()).Plan(4)
	if !deletableIDs(plan)["seg-001"] {
		t.Fatalf("seg-001 should be deletable, plan=%+v", plan)
	}
	ret := retainedByID(plan, "seg-002")
	if ret == nil {
		t.Fatalf("seg-002 must be retained, plan=%+v", plan)
	}
	if len(ret.Blockers) != 1 || ret.Blockers[0].Kind != BlockerConsumer {
		t.Fatalf("want exactly one consumer blocker, got %+v", ret.Blockers)
	}
	b := ret.Blockers[0]
	if b.Consumer != "c1" || b.Horizon != 2 || b.FirstGap != 3 {
		t.Fatalf("blocker = %+v, want consumer c1 horizon 2 first gap 3", b)
	}
}

// Moving a confirmed prefix backwards is rejected.
func TestConfirmPrefixRollbackRejected(t *testing.T) {
	store := newStore(2, 3)
	reg := NewRegistry(store.End)
	if err := reg.Register("c1"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ConfirmPrefix("c1", 5); err != nil {
		t.Fatal(err)
	}
	if err := reg.ConfirmPrefix("c1", 3); !errors.Is(err, ErrRollback) {
		t.Fatalf("rollback: got %v, want ErrRollback", err)
	}
	if horizon, _ := reg.Horizon("c1"); horizon != 5 {
		t.Fatalf("horizon moved to %d after rejected rollback", horizon)
	}
	// Re-confirming the same prefix is a no-op, not a rollback.
	if err := reg.ConfirmPrefix("c1", 5); err != nil {
		t.Fatalf("idempotent confirm: %v", err)
	}
	// Out-of-range confirmations are rejected too.
	if err := reg.Confirm("c1", 7); err == nil {
		t.Fatal("confirm beyond log end should fail")
	}
}

// A consumer that exits stops blocking truncation.
func TestConsumerExitUnblocks(t *testing.T) {
	store := newStore(2, 2)
	reg := NewRegistry(store.End)
	for _, id := range []string{"c1", "c2"} {
		if err := reg.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.ConfirmPrefix("c1", 4); err != nil {
		t.Fatal(err)
	}
	if err := reg.Confirm("c2", 1); err != nil {
		t.Fatal(err)
	}
	planner := newPlanner(store, reg, NewSnapshots())
	if got := len(planner.Plan(4).Deletable); got != 0 {
		t.Fatalf("c2 lags at horizon 1, want 0 deletable, got %d", got)
	}
	if err := reg.Exit("c2"); err != nil {
		t.Fatal(err)
	}
	plan := planner.Plan(4)
	if got := len(plan.Deletable); got != 2 {
		t.Fatalf("after c2 exit want 2 deletable, got %d (%+v)", got, plan)
	}
}

// An old snapshot keeps historical segments alive even after every
// consumer has moved past them.
func TestSnapshotPinsHistoricalSegment(t *testing.T) {
	store := newStore(2, 3) // [1,2], [3,4], [5,6]
	reg := NewRegistry(store.End)
	if err := reg.Register("c1"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ConfirmPrefix("c1", 6); err != nil {
		t.Fatal(err)
	}
	snaps := NewSnapshots()
	if err := snaps.Pin("snap-old", 1, 2); err != nil {
		t.Fatal(err)
	}
	planner := newPlanner(store, reg, snaps)
	plan := planner.Plan(6)
	ids := deletableIDs(plan)
	if ids["seg-001"] || !ids["seg-002"] || !ids["seg-003"] {
		t.Fatalf("only seg-001 should be pinned, plan=%+v", plan)
	}
	ret := retainedByID(plan, "seg-001")
	if ret == nil || len(ret.Blockers) != 1 || ret.Blockers[0].Kind != BlockerSnapshot || ret.Blockers[0].Snapshot != "snap-old" {
		t.Fatalf("seg-001 evidence = %+v, want snapshot snap-old", ret)
	}
	snaps.Release("snap-old")
	if got := len(planner.Plan(6).Deletable); got != 3 {
		t.Fatalf("after release want 3 deletable, got %d", got)
	}
}

// The evidence set is minimal: it contains exactly the entities that
// must change, and shrinks as each one unblocks.
func TestMinimalBlockingEvidence(t *testing.T) {
	store := newStore(2, 2) // [1,2], [3,4]
	reg := NewRegistry(store.End)
	for _, id := range []string{"c1", "c2"} {
		if err := reg.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.ConfirmPrefix("c1", 2); err != nil {
		t.Fatal(err)
	}
	if err := reg.Confirm("c2", 1); err != nil {
		t.Fatal(err)
	}
	snaps := NewSnapshots()
	if err := snaps.Pin("s1", 3, 4); err != nil {
		t.Fatal(err)
	}
	planner := newPlanner(store, reg, snaps)

	ret := retainedByID(planner.Plan(4), "seg-002")
	if ret == nil || len(ret.Blockers) != 3 {
		t.Fatalf("seg-002 should have 3 blockers (c1, c2, s1), got %+v", ret)
	}
	// c2 catches up: its blocker disappears, the rest remain.
	if err := reg.ConfirmPrefix("c2", 4); err != nil {
		t.Fatal(err)
	}
	ret = retainedByID(planner.Plan(4), "seg-002")
	if ret == nil || len(ret.Blockers) != 2 {
		t.Fatalf("after c2 catches up want 2 blockers, got %+v", ret)
	}
	for _, b := range ret.Blockers {
		if b.Kind == BlockerConsumer && b.Consumer == "c2" {
			t.Fatalf("c2 should no longer block: %+v", ret.Blockers)
		}
	}
	// c1's evidence points at its first gap inside the segment.
	for _, b := range ret.Blockers {
		if b.Kind == BlockerConsumer && b.Consumer == "c1" && b.FirstGap != 3 {
			t.Fatalf("c1 first gap = %d, want 3", b.FirstGap)
		}
	}
}

// A failed deletion leaves the request pending and retryable.
func TestDeletionFailureIsRetried(t *testing.T) {
	store := newStore(2, 2)
	reg := NewRegistry(store.End)
	if err := reg.Register("c1"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ConfirmPrefix("c1", 4); err != nil {
		t.Fatal(err)
	}
	planner := newPlanner(store, reg, NewSnapshots())

	failures := 2
	store.DeleteHook = func(id SegmentID) error {
		if failures > 0 {
			failures--
			return errors.New("injected I/O error")
		}
		return nil
	}

	dir := t.TempDir()
	tr, err := OpenTruncator(dir, planner)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Submit("req-1", 4); err == nil {
		t.Fatal("submit should report the deletion failure")
	}
	if got := tr.Pending(); len(got) != 1 || got[0] != "req-1" {
		t.Fatalf("pending = %v, want [req-1]", got)
	}
	if err := tr.RetryPending(); err == nil {
		t.Fatal("second attempt should still fail")
	}
	if err := tr.RetryPending(); err != nil {
		t.Fatalf("third attempt should succeed: %v", err)
	}
	if got := tr.Pending(); len(got) != 0 {
		t.Fatalf("pending = %v, want empty", got)
	}
	if got := len(store.Segments()); got != 0 {
		t.Fatalf("store still holds %d segments", got)
	}
}

// A crash (reopen) must not forget an unfinished request.
func TestRecoveryRemembersUnfinishedRequest(t *testing.T) {
	store := newStore(2, 2)
	reg := NewRegistry(store.End)
	if err := reg.Register("c1"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ConfirmPrefix("c1", 4); err != nil {
		t.Fatal(err)
	}
	planner := newPlanner(store, reg, NewSnapshots())

	store.DeleteHook = func(id SegmentID) error { return errors.New("disk full") }

	dir := t.TempDir()
	tr, err := OpenTruncator(dir, planner)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Submit("req-1", 4); err == nil {
		t.Fatal("submit should fail while the disk is broken")
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}

	// "Restart": a fresh Truncator over the same journal must restore
	// the unfinished request even though nothing was deleted.
	tr2, err := OpenTruncator(dir, planner)
	if err != nil {
		t.Fatal(err)
	}
	defer tr2.Close()
	if got := tr2.Pending(); len(got) != 1 || got[0] != "req-1" {
		t.Fatalf("after recovery pending = %v, want [req-1]", got)
	}
	store.DeleteHook = nil
	if err := tr2.RetryPending(); err != nil {
		t.Fatalf("retry after recovery: %v", err)
	}
	if got := len(store.Segments()); got != 0 {
		t.Fatalf("store still holds %d segments", got)
	}

	// A completed request must not resurface on the next reopen.
	if err := tr2.Close(); err != nil {
		t.Fatal(err)
	}
	tr3, err := OpenTruncator(dir, planner)
	if err != nil {
		t.Fatal(err)
	}
	defer tr3.Close()
	if got := tr3.Pending(); len(got) != 0 {
		t.Fatalf("completed request resurfaced: %v", got)
	}
}

// A crash between deleting segment A and segment B resumes with B only;
// the already-deleted segment is skipped idempotently.
func TestRecoverySkipsDeletedSegments(t *testing.T) {
	store := newStore(2, 3)
	reg := NewRegistry(store.End)
	if err := reg.Register("c1"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ConfirmPrefix("c1", 6); err != nil {
		t.Fatal(err)
	}
	planner := newPlanner(store, reg, NewSnapshots())

	deleted := 0
	store.DeleteHook = func(id SegmentID) error {
		deleted++
		if deleted == 2 {
			return errors.New("crash after first deletion")
		}
		return nil
	}

	dir := t.TempDir()
	tr, err := OpenTruncator(dir, planner)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Submit("req-1", 6); err == nil {
		t.Fatal("submit should fail mid-way")
	}
	tr.Close()
	if got := len(store.Segments()); got != 2 {
		t.Fatalf("want 2 segments remaining after crash, got %d", got)
	}

	store.DeleteHook = nil
	tr2, err := OpenTruncator(dir, planner)
	if err != nil {
		t.Fatal(err)
	}
	defer tr2.Close()
	if err := tr2.RetryPending(); err != nil {
		t.Fatal(err)
	}
	if got := len(store.Segments()); got != 0 {
		t.Fatalf("store still holds %d segments", got)
	}
}
