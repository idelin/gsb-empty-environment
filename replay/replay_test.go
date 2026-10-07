package replay

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// account is the example state: a balance plus a ledger of entries.
type account struct {
	Balance int
	Entries []string
}

// accountHandler is a pure, deterministic handler. A "credit" event
// updates the balance and emits a notification effect; a "welcome"
// event emits two effects to exercise sequence numbering.
func accountHandler(state any, ev Event) (any, []EffectSpec) {
	acc := state.(*account)
	next := &account{Balance: acc.Balance, Entries: append([]string{}, acc.Entries...)}
	switch ev.Type {
	case "credit":
		amount := ev.Data.(int)
		next.Balance += amount
		next.Entries = append(next.Entries, fmt.Sprintf("credit:%d", amount))
		return next, []EffectSpec{
			{Kind: "notify", Payload: fmt.Sprintf("credited %d", amount)},
		}
	case "welcome":
		next.Entries = append(next.Entries, "welcome")
		return next, []EffectSpec{
			{Kind: "email", Payload: "hello"},
			{Kind: "sms", Payload: "hi"},
		}
	}
	return next, nil
}

func newAccountExecutor(store Store, sink Sink) *Executor {
	return NewExecutor(store, sink, accountHandler, func() any { return &account{} })
}

func TestEffectIDDeterministic(t *testing.T) {
	if got, want := EffectID("evt-1", 0), "evt-1#0"; got != want {
		t.Fatalf("EffectID = %q, want %q", got, want)
	}
	if got, want := EffectID("evt-1", 1), "evt-1#1"; got != want {
		t.Fatalf("EffectID = %q, want %q", got, want)
	}
}

func TestReplaySkipsConfirmedEffects(t *testing.T) {
	store := NewMemStore()
	receiver := NewReceiver()
	ex := newAccountExecutor(store, receiver)

	events := []Event{
		{ID: "e1", Type: "welcome"},
		{ID: "e2", Type: "credit", Data: 100},
		{ID: "e3", Type: "credit", Data: 50},
	}
	for _, ev := range events {
		if err := ex.Execute(ev); err != nil {
			t.Fatalf("Execute(%s): %v", ev.ID, err)
		}
	}

	wantIDs := []string{"e1#0", "e1#1", "e2#0", "e3#0"}
	if got := receiver.AppliedIDs(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("applied IDs = %v, want %v", got, wantIDs)
	}

	before := ex.State().(*account)
	state, effects, err := ex.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	// Replay regenerates identical effect descriptions.
	gotIDs := make([]string, len(effects))
	for i, e := range effects {
		gotIDs[i] = e.ID
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("replayed effect IDs = %v, want %v", gotIDs, wantIDs)
	}

	// No confirmed effect was re-executed and none were duplicates.
	if got := receiver.AppliedIDs(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("applied IDs after replay = %v, want %v", got, wantIDs)
	}
	if d := receiver.Duplicates(); d != 0 {
		t.Fatalf("duplicates after replay = %d, want 0", d)
	}

	// State computation is reproducible from the log alone.
	rebuilt := state.(*account)
	if !reflect.DeepEqual(rebuilt, before) {
		t.Fatalf("rebuilt state = %+v, want %+v", rebuilt, before)
	}
	if rebuilt.Balance != 150 {
		t.Fatalf("balance = %d, want 150", rebuilt.Balance)
	}
}

// lossyStore simulates a crash that loses a receipt after the effect was
// already delivered to the receiver.
type lossyStore struct {
	*MemStore
	dropNext bool
}

func (s *lossyStore) RecordReceipt(effectID string) error {
	if s.dropNext {
		s.dropNext = false
		return nil // receipt lost
	}
	return s.MemStore.RecordReceipt(effectID)
}

func TestLostReceiptRecovery(t *testing.T) {
	store := &lossyStore{MemStore: NewMemStore()}
	receiver := NewReceiver()
	ex := newAccountExecutor(store, receiver)

	if err := ex.Execute(Event{ID: "e1", Type: "credit", Data: 100}); err != nil {
		t.Fatalf("Execute(e1): %v", err)
	}

	// Crash: e1's effect is delivered but its receipt is lost.
	store.dropNext = true
	if err := ex.Execute(Event{ID: "e2", Type: "credit", Data: 50}); err != nil {
		t.Fatalf("Execute(e2): %v", err)
	}
	if store.HasReceipt("e2#0") {
		t.Fatal("receipt for e2#0 should have been lost")
	}
	if got := receiver.AppliedIDs(); !reflect.DeepEqual(got, []string{"e1#0", "e2#0"}) {
		t.Fatalf("applied IDs = %v, want [e1#0 e2#0]", got)
	}

	// Recovery: replay re-sends only the unconfirmed effect, with the
	// same ID, and the receiver's idempotency table deduplicates it.
	if _, _, err := ex.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := receiver.AppliedIDs(); !reflect.DeepEqual(got, []string{"e1#0", "e2#0"}) {
		t.Fatalf("applied IDs after recovery = %v, want [e1#0 e2#0]", got)
	}
	if d := receiver.Duplicates(); d != 1 {
		t.Fatalf("duplicates = %d, want 1 (the re-sent e2#0)", d)
	}
	if !store.HasReceipt("e2#0") {
		t.Fatal("receipt for e2#0 should be recorded after recovery")
	}

	// A second replay is fully quiet.
	if _, _, err := ex.Replay(); err != nil {
		t.Fatalf("second Replay: %v", err)
	}
	if d := receiver.Duplicates(); d != 1 {
		t.Fatalf("duplicates after second replay = %d, want 1", d)
	}
}

func TestDuplicateEvent(t *testing.T) {
	store := NewMemStore()
	receiver := NewReceiver()
	ex := newAccountExecutor(store, receiver)

	ev := Event{ID: "e1", Type: "credit", Data: 100}
	if err := ex.Execute(ev); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := ex.Execute(ev); !errors.Is(err, ErrDuplicateEvent) {
		t.Fatalf("re-Execute = %v, want ErrDuplicateEvent", err)
	}

	// The duplicate event neither changed state nor re-ran effects.
	if got := ex.State().(*account).Balance; got != 100 {
		t.Fatalf("balance = %d, want 100", got)
	}
	if got := receiver.AppliedIDs(); !reflect.DeepEqual(got, []string{"e1#0"}) {
		t.Fatalf("applied IDs = %v, want [e1#0]", got)
	}
	if d := receiver.Duplicates(); d != 0 {
		t.Fatalf("duplicates = %d, want 0", d)
	}

	// Replay still regenerates exactly one effect for the event.
	_, effects, err := ex.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(effects) != 1 || effects[0].ID != "e1#0" {
		t.Fatalf("replayed effects = %v, want single e1#0", effects)
	}
}

func TestDuplicateDeliveryToReceiver(t *testing.T) {
	receiver := NewReceiver()
	eff := Effect{ID: EffectID("e1", 0), EventID: "e1", Seq: 0, Kind: "notify", Payload: "x"}

	if dup := receiver.Deliver(eff); dup {
		t.Fatal("first delivery reported as duplicate")
	}
	if dup := receiver.Deliver(eff); !dup {
		t.Fatal("second delivery not reported as duplicate")
	}
	if got := receiver.AppliedIDs(); !reflect.DeepEqual(got, []string{"e1#0"}) {
		t.Fatalf("applied IDs = %v, want [e1#0]", got)
	}
	if !receiver.Has("e1#0") {
		t.Fatal("idempotency table should contain e1#0")
	}
}
