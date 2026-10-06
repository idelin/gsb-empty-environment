package objstore

import (
	"sync"
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const grace = 10 * time.Second

func newFixture() (*Store, *VirtualClock) {
	clock := NewVirtualClock(epoch)
	return NewStore(clock, grace), clock
}

func mustGet(t *testing.T, s *Store, id string) Info {
	t.Helper()
	info, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get(%q): %v", id, err)
	}
	return info
}

func TestDeleteEntersPendingThenHardDeletesAfterGrace(t *testing.T) {
	store, clock := newFixture()
	if err := store.Create("a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("a"); err != nil {
		t.Fatal(err)
	}
	info := mustGet(t, store, "a")
	if info.State != StatePendingDeletion {
		t.Fatalf("state = %v, want pending-deletion", info.State)
	}
	if want := epoch.Add(grace); !info.DeleteAt.Equal(want) {
		t.Fatalf("DeleteAt = %v, want %v", info.DeleteAt, want)
	}

	clock.Advance(grace - time.Nanosecond)
	if info := mustGet(t, store, "a"); info.State != StatePendingDeletion {
		t.Fatalf("before deadline: state = %v, want pending-deletion", info.State)
	}

	clock.Advance(time.Nanosecond)
	if info := mustGet(t, store, "a"); info.State != StateDeleted {
		t.Fatalf("at deadline: state = %v, want deleted", info.State)
	}
}

func TestRevokeDuringGraceBumpsGeneration(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Delete("a")

	clock.Advance(grace / 2)
	if err := store.AddRef("a", "ref-1"); err != nil {
		t.Fatalf("AddRef during grace: %v", err)
	}
	info := mustGet(t, store, "a")
	if info.State != StateActive {
		t.Fatalf("state = %v, want active", info.State)
	}
	if info.Generation != 2 {
		t.Fatalf("generation = %d, want 2", info.Generation)
	}
	if info.RefCount != 1 {
		t.Fatalf("refcount = %d, want 1", info.RefCount)
	}

	// The cleanup task for generation 1 fires but must be a no-op.
	clock.Advance(2 * grace)
	info = mustGet(t, store, "a")
	if info.State != StateActive || info.Generation != 2 {
		t.Fatalf("after stale cleanup: state=%v gen=%d, want active gen 2", info.State, info.Generation)
	}
}

func TestStaleCleanupOnlyAppliesToOriginalGeneration(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Delete("a")          // cleanup task #1 for generation 1 at t0+grace
	clock.Advance(grace / 2)   // t0 + 5s
	store.AddRef("a", "ref-1") // revoke, generation -> 2
	store.Delete("a")          // cleanup task #2 for generation 2 at t0+15s

	// t0+10s: task #1 fires, must not touch the generation-2 pending object.
	clock.Advance(grace / 2)
	info := mustGet(t, store, "a")
	if info.State != StatePendingDeletion || info.Generation != 2 {
		t.Fatalf("after stale task: state=%v gen=%d, want pending gen 2", info.State, info.Generation)
	}

	// t0+15s: task #2 fires and hard-deletes.
	clock.Advance(grace / 2)
	if info := mustGet(t, store, "a"); info.State != StateDeleted {
		t.Fatalf("after current task: state = %v, want deleted", info.State)
	}
}

func TestHardDeleteBlocksRefCreationWithoutImplicitRebuild(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Delete("a")
	clock.Advance(grace) // hard-deleted

	if err := store.AddRef("a", "ref-1"); err != ErrObjectDeleted {
		t.Fatalf("AddRef after hard delete: err = %v, want ErrObjectDeleted", err)
	}
	info := mustGet(t, store, "a")
	if info.State != StateDeleted || info.RefCount != 0 {
		t.Fatalf("implicit rebuild detected: state=%v refcount=%d", info.State, info.RefCount)
	}

	if err := store.AddRef("never-existed", "ref-1"); err != ErrObjectNotFound {
		t.Fatalf("AddRef on unknown id: err = %v, want ErrObjectNotFound", err)
	}
}

func TestExplicitRecreateAfterHardDeleteStartsNewGeneration(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Delete("a")
	clock.Advance(grace)

	if err := store.Create("a"); err != nil {
		t.Fatalf("explicit recreate: %v", err)
	}
	info := mustGet(t, store, "a")
	if info.State != StateActive || info.Generation != 2 {
		t.Fatalf("state=%v gen=%d, want active gen 2", info.State, info.Generation)
	}
	if err := store.AddRef("a", "ref-1"); err != nil {
		t.Fatalf("AddRef after explicit recreate: %v", err)
	}
}

func TestBoundaryRevokeOneNanosecondBeforeDeadline(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Delete("a")

	clock.Advance(grace - time.Nanosecond)
	if err := store.AddRef("a", "ref-1"); err != nil {
		t.Fatalf("revoke just before deadline: %v", err)
	}
	// Crossing the original deadline must not delete the revived object.
	clock.Advance(time.Nanosecond)
	if info := mustGet(t, store, "a"); info.State != StateActive {
		t.Fatalf("state = %v, want active", info.State)
	}
}

func TestBoundaryCleanupWinsAtExactDeadline(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Delete("a")

	clock.Advance(grace) // fires cleanup exactly at deleteAt
	if info := mustGet(t, store, "a"); info.State != StateDeleted {
		t.Fatalf("state = %v, want deleted", info.State)
	}
	if err := store.AddRef("a", "ref-1"); err != ErrObjectDeleted {
		t.Fatalf("AddRef at deadline: err = %v, want ErrObjectDeleted", err)
	}
}

// TestConcurrentRevokeVsCleanup hammers the boundary between the virtual
// clock and the reference ledger: whichever side loses the race must fail
// cleanly, and the object must never end up deleted after a successful
// revoke. Run with -race.
func TestConcurrentRevokeVsCleanup(t *testing.T) {
	for i := 0; i < 200; i++ {
		store, clock := newFixture()
		store.Create("a")
		store.Delete("a")

		var wg sync.WaitGroup
		var addErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			clock.Advance(grace) // fires cleanup exactly at the deadline
		}()
		go func() {
			defer wg.Done()
			addErr = store.AddRef("a", "ref-1")
		}()
		wg.Wait()

		info := mustGet(t, store, "a")
		switch {
		case addErr == nil:
			// Revoke won: object alive, generation bumped, stale task no-op.
			if info.State != StateActive || info.Generation != 2 || info.RefCount != 1 {
				t.Fatalf("iter %d: revoke won but state=%v gen=%d refs=%d",
					i, info.State, info.Generation, info.RefCount)
			}
		case addErr == ErrObjectDeleted:
			if info.State != StateDeleted {
				t.Fatalf("iter %d: cleanup won but state=%v", i, info.State)
			}
		default:
			t.Fatalf("iter %d: unexpected AddRef error %v", i, addErr)
		}
	}
}

func TestReconcileSweepsOverdueAfterLostTimers(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Create("b")
	store.Create("c")
	store.Delete("a")
	store.Delete("b")

	// Simulate a restart: the clock jumps past the deadline and the
	// scheduled cleanup tasks are lost (Set does not fire timers).
	clock.Set(epoch.Add(2 * grace))

	purged := store.Reconcile()
	if len(purged) != 2 || purged[0] != "a" || purged[1] != "b" {
		t.Fatalf("purged = %v, want [a b]", purged)
	}
	if info := mustGet(t, store, "c"); info.State != StateActive {
		t.Fatalf("untouched object: state = %v, want active", info.State)
	}
	if got := store.Reconcile(); len(got) != 0 {
		t.Fatalf("second reconcile purged %v, want nothing", got)
	}
}

func TestReconcileIgnoresObjectsStillInGrace(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.Delete("a")
	clock.Advance(grace / 2)
	if got := store.Reconcile(); len(got) != 0 {
		t.Fatalf("purged %v during grace, want nothing", got)
	}
	if info := mustGet(t, store, "a"); info.State != StatePendingDeletion {
		t.Fatalf("state = %v, want pending-deletion", info.State)
	}
}

func TestReferenceLedger(t *testing.T) {
	store, _ := newFixture()
	store.Create("a")
	store.AddRef("a", "ref-1")
	store.AddRef("a", "ref-2")
	if err := store.AddRef("a", "ref-1"); err != nil {
		t.Fatalf("duplicate AddRef must be idempotent: %v", err)
	}
	if info := mustGet(t, store, "a"); info.RefCount != 2 {
		t.Fatalf("refcount = %d, want 2", info.RefCount)
	}
	if err := store.RemoveRef("a", "ref-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveRef("a", "ref-1"); err != ErrRefNotFound {
		t.Fatalf("double remove: err = %v, want ErrRefNotFound", err)
	}
	if err := store.RemoveRef("missing", "ref-1"); err != ErrObjectNotFound {
		t.Fatalf("remove on missing object: err = %v, want ErrObjectNotFound", err)
	}
}

func TestRefsPreservedAcrossRevokeAndClearedOnHardDelete(t *testing.T) {
	store, clock := newFixture()
	store.Create("a")
	store.AddRef("a", "ref-1")
	store.Delete("a")
	store.AddRef("a", "ref-2") // revokes
	if info := mustGet(t, store, "a"); info.RefCount != 2 {
		t.Fatalf("refcount after revoke = %d, want 2", info.RefCount)
	}
	store.Delete("a")
	clock.Advance(grace)
	if info := mustGet(t, store, "a"); info.RefCount != 0 {
		t.Fatalf("refcount after hard delete = %d, want 0", info.RefCount)
	}
}

func TestDeleteIdempotencyAndErrors(t *testing.T) {
	store, clock := newFixture()
	if err := store.Delete("missing"); err != ErrObjectNotFound {
		t.Fatalf("delete missing: err = %v, want ErrObjectNotFound", err)
	}
	store.Create("a")
	store.Delete("a")
	first := mustGet(t, store, "a").DeleteAt
	clock.Advance(grace / 2)
	if err := store.Delete("a"); err != nil {
		t.Fatalf("second delete must be a no-op: %v", err)
	}
	if got := mustGet(t, store, "a").DeleteAt; !got.Equal(first) {
		t.Fatalf("second delete extended grace: %v != %v", got, first)
	}
	clock.Advance(grace)
	if err := store.Delete("a"); err != ErrObjectDeleted {
		t.Fatalf("delete after hard delete: err = %v, want ErrObjectDeleted", err)
	}
}

func TestCreateOnLiveObjectFails(t *testing.T) {
	store, _ := newFixture()
	store.Create("a")
	if err := store.Create("a"); err != ErrObjectExists {
		t.Fatalf("err = %v, want ErrObjectExists", err)
	}
	store.Delete("a")
	if err := store.Create("a"); err != ErrObjectExists {
		t.Fatalf("create during grace: err = %v, want ErrObjectExists", err)
	}
}
