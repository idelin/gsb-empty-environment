package lease

import (
	"errors"
	"testing"
	"time"
)

func testRecord() DeliveryRecord {
	return DeliveryRecord{
		Key:     RecordKey{Tenant: 1, Auth: 9, Seq: 1005},
		Payload: []byte("payload"),
	}
}

func deliverAsync(r *Receiver, rec DeliveryRecord) <-chan error {
	done := make(chan error, 1)
	go func() { done <- r.Deliver(rec) }()
	return done
}

func waitEntered(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not reach the pause gate")
	}
}

func TestDeliverCommits(t *testing.T) {
	store, inbox := NewMemStore(), NewMemInbox()
	r := NewReceiver(store, inbox)
	rec := testRecord()
	if err := r.Deliver(rec); err != nil {
		t.Fatal(err)
	}
	if st, err := r.State(rec.Key); err != nil || st != StateCommitted {
		t.Fatalf("state %v, err %v", st, err)
	}
	if inbox.Len() != 1 || inbox.Applies != 1 {
		t.Fatalf("inbox: len %d applies %d", inbox.Len(), inbox.Applies)
	}
}

func TestDuplicateControlReceipts(t *testing.T) {
	store, inbox := NewMemStore(), NewMemInbox()
	r := NewReceiver(store, inbox)
	rec := testRecord()
	if err := r.Deliver(rec); err != nil {
		t.Fatal(err)
	}
	// Duplicate control receipts are idempotent no-ops.
	for i := 0; i < 3; i++ {
		if err := r.Confirm(rec.Key); err != nil {
			t.Fatalf("confirm %d: %v", i, err)
		}
	}
	// A duplicate delivery attempt is also a no-op.
	if err := r.Deliver(rec); err != nil {
		t.Fatal(err)
	}
	if inbox.Applies != 1 {
		t.Fatalf("applies %d, want 1", inbox.Applies)
	}
	if err := r.Confirm(RecordKey{Tenant: 1, Auth: 9, Seq: 42}); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("unknown key: want ErrRecordNotFound, got %v", err)
	}
}

func TestConfirmCommitsStagedRecord(t *testing.T) {
	memStore, memInbox := NewMemStore(), NewMemInbox()
	inbox := NewPausableInbox(memInbox)
	r := NewReceiver(memStore, inbox)
	rec := testRecord()
	// Crash the delivery before the inbox applies it: record stays staged.
	inbox.Pause(PauseBefore)
	entered := inbox.Entered()
	done := deliverAsync(r, rec)
	waitEntered(t, entered)
	inbox.Release(ErrInjected)
	if err := <-done; !errors.Is(err, ErrInjected) {
		t.Fatalf("deliver: %v", err)
	}
	if st, _ := r.State(rec.Key); st != StatePending {
		t.Fatalf("state %v, want pending", st)
	}
	// A control receipt asserts the host definitely has it.
	if err := r.Confirm(rec.Key); err != nil {
		t.Fatal(err)
	}
	if err := r.Confirm(rec.Key); err != nil {
		t.Fatal("duplicate receipt must be a no-op")
	}
	if st, _ := r.State(rec.Key); st != StateCommitted {
		t.Fatalf("state %v, want committed", st)
	}
}

// A crash between delivery (inbox applied) and receipt (local commit)
// must never be marked complete from local state alone: only a definite
// answer from the host's idempotent inbox commits the record.
func TestCrashBetweenDeliveryAndReceipt(t *testing.T) {
	memStore, memInbox := NewMemStore(), NewMemInbox()
	inbox := NewPausableInbox(memInbox)
	r := NewReceiver(memStore, inbox)
	rec := testRecord()

	inbox.Pause(PauseAfterApply)
	entered := inbox.Entered()
	done := deliverAsync(r, rec)
	waitEntered(t, entered)
	// The host durably applied the record, then crashed before the
	// receipt reached the receiver.
	inbox.Release(ErrInjected)
	if err := <-done; !errors.Is(err, ErrInjected) {
		t.Fatalf("deliver: %v", err)
	}
	if st, _ := r.State(rec.Key); st != StatePending {
		t.Fatalf("before crash: state %v, want pending", st)
	}

	// Simulate a process crash: a fresh Receiver over the same durable
	// store and inbox. Local state alone must not mark it complete.
	r = NewReceiver(memStore, inbox)
	if st, _ := r.State(rec.Key); st == StateCommitted {
		t.Fatal("crashed record must not be committed from local state alone")
	}

	// While the inbox cannot give a definite answer, the record is
	// unknown and stays uncommitted.
	inbox.Pause(PauseBefore)
	entered = inbox.Entered()
	reconciled := make(chan error, 1)
	go func() { reconciled <- r.Reconcile() }()
	waitEntered(t, entered)
	inbox.Release(ErrInjected)
	if err := <-reconciled; !errors.Is(err, ErrInjected) {
		t.Fatalf("reconcile: %v", err)
	}
	if st, _ := r.State(rec.Key); st != StateUnknown {
		t.Fatalf("state %v, want unknown", st)
	}

	// The inbox's definite answer (record present) commits it without
	// a second delivery.
	if err := r.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.State(rec.Key); st != StateCommitted {
		t.Fatalf("state %v, want committed", st)
	}
	if memInbox.Len() != 1 || memInbox.Applies != 1 {
		t.Fatalf("inbox: len %d applies %d, want 1/1", memInbox.Len(), memInbox.Applies)
	}
}

// A crash before the inbox applied the record: the inbox's definite
// "absent" answer makes reconciliation redeliver it.
func TestCrashBeforeInboxApplyRedelivers(t *testing.T) {
	memStore, memInbox := NewMemStore(), NewMemInbox()
	inbox := NewPausableInbox(memInbox)
	r := NewReceiver(memStore, inbox)
	rec := testRecord()

	inbox.Pause(PauseBefore)
	entered := inbox.Entered()
	done := deliverAsync(r, rec)
	waitEntered(t, entered)
	inbox.Release(ErrInjected)
	<-done

	r = NewReceiver(memStore, inbox)
	if err := r.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.State(rec.Key); st != StateCommitted {
		t.Fatalf("state %v, want committed", st)
	}
	if memInbox.Len() != 1 || memInbox.Applies != 1 {
		t.Fatalf("inbox: len %d applies %d, want 1/1", memInbox.Len(), memInbox.Applies)
	}
}

// Exhaustively crash at every persistent write point of the delivery
// pipeline; after recovery the record must end up committed exactly
// once, however far the original delivery got.
func TestCrashAtEveryPersistentWritePoint(t *testing.T) {
	rec := testRecord()
	points := []struct {
		name  string
		crash func(t *testing.T, r *Receiver, store *PausableStore, inbox *PausableInbox)
	}{
		{"PutStaged", func(t *testing.T, r *Receiver, store *PausableStore, inbox *PausableInbox) {
			store.PauseOn(WritePutStaged)
			entered := store.Entered(WritePutStaged)
			done := deliverAsync(r, rec)
			waitEntered(t, entered)
			store.Release(WritePutStaged, ErrInjected)
			if err := <-done; !errors.Is(err, ErrInjected) {
				t.Fatalf("deliver: %v", err)
			}
		}},
		{"InboxPutBeforeApply", func(t *testing.T, r *Receiver, store *PausableStore, inbox *PausableInbox) {
			inbox.Pause(PauseBefore)
			entered := inbox.Entered()
			done := deliverAsync(r, rec)
			waitEntered(t, entered)
			inbox.Release(ErrInjected)
			<-done
		}},
		{"InboxPutAfterApply", func(t *testing.T, r *Receiver, store *PausableStore, inbox *PausableInbox) {
			inbox.Pause(PauseAfterApply)
			entered := inbox.Entered()
			done := deliverAsync(r, rec)
			waitEntered(t, entered)
			inbox.Release(ErrInjected)
			<-done
		}},
		{"MarkCommitted", func(t *testing.T, r *Receiver, store *PausableStore, inbox *PausableInbox) {
			store.PauseOn(WriteMarkCommitted)
			entered := store.Entered(WriteMarkCommitted)
			done := deliverAsync(r, rec)
			waitEntered(t, entered)
			store.Release(WriteMarkCommitted, ErrInjected)
			<-done
		}},
	}
	for _, p := range points {
		t.Run(p.name, func(t *testing.T) {
			memStore, memInbox := NewMemStore(), NewMemInbox()
			store := NewPausableStore(memStore)
			inbox := NewPausableInbox(memInbox)
			r := NewReceiver(store, inbox)

			p.crash(t, r, store, inbox)

			// Crash: a fresh receiver over the same durable state.
			r = NewReceiver(store, inbox)
			st, stateErr := r.State(rec.Key)
			if st == StateCommitted {
				t.Fatal("crashed delivery must not be committed yet")
			}
			// Recover: reconcile until the inbox gives definite answers.
			if err := r.Reconcile(); err != nil {
				t.Fatal(err)
			}
			st, err := r.State(rec.Key)
			if errors.Is(err, ErrRecordNotFound) {
				// The crash happened before staging: redeliver.
				if err := r.Deliver(rec); err != nil {
					t.Fatal(err)
				}
				st, _ = r.State(rec.Key)
			} else if err != nil {
				t.Fatal(err)
			}
			if st != StateCommitted {
				t.Fatalf("state %v, want committed", st)
			}
			if memInbox.Len() != 1 {
				t.Fatalf("inbox len %d, want exactly one delivery", memInbox.Len())
			}
			if staged, _ := memStore.Staged(); len(staged) != 0 {
				t.Fatalf("staged records left: %v", staged)
			}
			_ = stateErr
		})
	}
}
