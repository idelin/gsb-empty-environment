package replay

import (
	"errors"
	"testing"
)

func deposit(id string, amount int64) Event { return Event{ID: id, Kind: "deposit", Amount: amount} }

func appliedIDs(r *Receiver) map[string]int {
	counts := make(map[string]int)
	for _, e := range r.Applied() {
		counts[e.ID]++
	}
	return counts
}

func TestHandleDispatchesEffectsWithDeterministicIDs(t *testing.T) {
	j := NewJournal()
	r := NewReceiver()
	ex := NewExecutor(j, AccountReducer, r, AccountState{})

	if err := ex.Handle(deposit("ev-1", 1500)); err != nil {
		t.Fatal(err)
	}
	if err := ex.Handle(Event{ID: "ev-2", Kind: "withdraw", Amount: 200}); err != nil {
		t.Fatal(err)
	}

	got := appliedIDs(r)
	want := map[string]int{"ev-1#0": 1, "ev-1#1": 1, "ev-2#0": 1}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("effect %s applied %d times, want %d", id, got[id], n)
		}
	}
	if len(r.Applied()) != len(want) {
		t.Errorf("applied %d effects, want %d", len(r.Applied()), len(want))
	}
	st := ex.State().(AccountState)
	if st.Balance != 1300 || st.TxCount != 2 {
		t.Errorf("state = %+v, want balance 1300 tx 2", st)
	}
	if j.ConfirmedCount() != 3 {
		t.Errorf("confirmed %d effects, want 3", j.ConfirmedCount())
	}
}

func TestDuplicateEventIsIgnored(t *testing.T) {
	j := NewJournal()
	r := NewReceiver()
	ex := NewExecutor(j, AccountReducer, r, AccountState{})

	for i := 0; i < 3; i++ {
		if err := ex.Handle(deposit("ev-dup", 500)); err != nil {
			t.Fatal(err)
		}
	}

	st := ex.State().(AccountState)
	if st.Balance != 500 || st.TxCount != 1 {
		t.Errorf("state = %+v, want balance 500 tx 1", st)
	}
	if len(r.Applied()) != 1 || r.Applied()[0].ID != "ev-dup#0" {
		t.Errorf("applied = %+v, want exactly ev-dup#0", r.Applied())
	}
}

func TestLostReceiptRetriedWithSameID(t *testing.T) {
	j := NewJournal()
	r := NewReceiver()
	// Lose the first receipt for every effect: the receiver records it,
	// but the sender sees an error and resends the same ID.
	seen := make(map[string]bool)
	r.DropReceipt = func(e Effect) bool {
		if !seen[e.ID] {
			seen[e.ID] = true
			return true
		}
		return false
	}
	ex := NewExecutor(j, AccountReducer, r, AccountState{})

	if err := ex.Handle(deposit("ev-loss", 2000)); err != nil {
		t.Fatal(err)
	}

	for id, n := range appliedIDs(r) {
		if n != 1 {
			t.Errorf("effect %s applied %d times, want 1 despite retry", id, n)
		}
	}
	if len(r.Applied()) != 2 {
		t.Fatalf("applied %d effects, want 2", len(r.Applied()))
	}
	if r.Deliveries() != 4 {
		t.Errorf("deliveries = %d, want 4 (2 effects x 1 lost receipt + 1 retry)", r.Deliveries())
	}
	if j.ConfirmedCount() != 2 {
		t.Errorf("confirmed %d, want 2", j.ConfirmedCount())
	}
}

func TestRecoverDoesNotReexecuteConfirmedEffects(t *testing.T) {
	j := NewJournal()
	r := NewReceiver()
	ex := NewExecutor(j, AccountReducer, r, AccountState{})
	events := []Event{
		deposit("ev-a", 1000),
		deposit("ev-b", 50),
		{ID: "ev-c", Kind: "withdraw", Amount: 300},
	}
	for _, ev := range events {
		if err := ex.Handle(ev); err != nil {
			t.Fatal(err)
		}
	}
	deliveriesBefore := r.Deliveries()

	// Crash: a fresh executor over the same journal and receiver.
	ex2 := NewExecutor(j, AccountReducer, r, AccountState{})
	if err := ex2.Recover(); err != nil {
		t.Fatal(err)
	}

	if r.Deliveries() != deliveriesBefore {
		t.Errorf("recover sent %d new deliveries, want 0", r.Deliveries()-deliveriesBefore)
	}
	if len(r.Applied()) != 4 {
		t.Errorf("applied %d effects, want 4 (no duplicates)", len(r.Applied()))
	}
	st := ex2.State().(AccountState)
	if st.Balance != 750 || st.TxCount != 3 {
		t.Errorf("recovered state = %+v, want balance 750 tx 3", st)
	}
}

func TestRecoverResumesUnconfirmedEffects(t *testing.T) {
	j := NewJournal()
	dead := NewReceiver()
	dead.DropReceipt = func(Effect) bool { return true } // receipts always lost
	ex := NewExecutor(j, AccountReducer, dead, AccountState{})
	ex.SetMaxRetries(1)

	err := ex.Handle(deposit("ev-pend", 3000))
	if err == nil {
		t.Fatal("expected dispatch failure with receipts always lost")
	}
	// The event is journaled but its effects remain unconfirmed.
	if j.ConfirmedCount() != 0 {
		t.Fatalf("confirmed %d effects, want 0", j.ConfirmedCount())
	}

	// Recover with a healthy receiver: pending effects dispatch once.
	live := NewReceiver()
	ex2 := NewExecutor(j, AccountReducer, live, AccountState{})
	if err := ex2.Recover(); err != nil {
		t.Fatal(err)
	}

	got := appliedIDs(live)
	if got["ev-pend#0"] != 1 || got["ev-pend#1"] != 1 {
		t.Errorf("applied = %v, want ev-pend#0 and ev-pend#1 exactly once", got)
	}
	if j.ConfirmedCount() != 2 {
		t.Errorf("confirmed %d, want 2 after recovery", j.ConfirmedCount())
	}
	st := ex2.State().(AccountState)
	if st.Balance != 3000 {
		t.Errorf("recovered balance = %d, want 3000", st.Balance)
	}
}

func TestDuplicateEventDuringReplayDoesNotDuplicateEffects(t *testing.T) {
	j := NewJournal()
	r := NewReceiver()
	ex := NewExecutor(j, AccountReducer, r, AccountState{})
	if err := ex.Handle(deposit("ev-x", 100)); err != nil {
		t.Fatal(err)
	}
	// A duplicate sneaks into the journal's event stream is impossible
	// via Handle, but replay must still be idempotent if the same event
	// ID were delivered again post-recovery.
	ex2 := NewExecutor(j, AccountReducer, r, AccountState{})
	if err := ex2.Recover(); err != nil {
		t.Fatal(err)
	}
	if err := ex2.Handle(deposit("ev-x", 100)); err != nil {
		t.Fatal(err)
	}
	if len(r.Applied()) != 1 {
		t.Errorf("applied %d effects, want 1", len(r.Applied()))
	}
	if st := ex2.State().(AccountState); st.Balance != 100 {
		t.Errorf("balance = %d, want 100", st.Balance)
	}
}

func TestDispatchGivesUpAfterMaxRetries(t *testing.T) {
	j := NewJournal()
	fail := SenderFunc(func(Effect) error { return errors.New("boom") })
	ex := NewExecutor(j, AccountReducer, fail, AccountState{})
	ex.SetMaxRetries(2)
	if err := ex.Handle(deposit("ev-fail", 10)); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if j.ConfirmedCount() != 0 {
		t.Errorf("confirmed %d, want 0", j.ConfirmedCount())
	}
}

type SenderFunc func(Effect) error

func (f SenderFunc) Send(e Effect) error { return f(e) }
