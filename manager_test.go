package netsec

import (
	"errors"
	"testing"
	"time"
)

var (
	chA = Channel{Kind: "email", Value: "old@example.test"}
	chB = Channel{Kind: "email", Value: "new@example.test"}
	chC = Channel{Kind: "email", Value: "third@example.test"}

	testKey = []byte("0123456789abcdef0123456789abcdef")
)

const testTTL = time.Hour

func newTestManager(t *testing.T) (*Manager, *MemoryStore, *FakeClock) {
	t.Helper()
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	store := NewMemoryStore()
	manager, err := NewManager(store, testKey, WithClock(clock), WithTTL(testTTL))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager, store, clock
}

func provisionUser(t *testing.T, store *MemoryStore, userID string, active Channel) {
	t.Helper()
	if _, err := store.Provision(userID, active); err != nil {
		t.Fatalf("Provision: %v", err)
	}
}

func TestHappyPathBothSidesSwitchAtomic(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "u1"
	provisionUser(t, store, user, chA)

	pending, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}
	if pending.Digest == "" || pending.OldToken == "" || pending.NewToken == "" {
		t.Fatal("pending change missing digest or tokens")
	}

	mid, err := manager.State(user)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !mid.Active.Equal(chA) || mid.Pending == nil {
		t.Fatalf("intermediate state lost old recovery channel: %+v", mid)
	}

	first, err := manager.Confirm(user, pending.OldToken)
	if err != nil || first.Status != StatusPending {
		t.Fatalf("old confirm = %+v, %v; want pending", first, err)
	}

	afterOne, err := manager.State(user)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !afterOne.Active.Equal(chA) {
		t.Fatal("new channel gained recovery power before both confirmations")
	}
	if afterOne.Pending.OldConfirmed == nil || afterOne.Pending.NewConfirmed != nil {
		t.Fatal("confirmation bookkeeping wrong after old-side proof")
	}

	second, err := manager.Confirm(user, pending.NewToken)
	if err != nil || second.Status != StatusSwitched {
		t.Fatalf("new confirm = %+v, %v; want switched", second, err)
	}

	final, err := manager.State(user)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !final.Active.Equal(chB) || final.Pending != nil {
		t.Fatalf("channel did not switch cleanly: %+v", final)
	}
}

func TestConfirmationsOrderIndependent(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "order"
	provisionUser(t, store, user, chA)

	pending, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}
	if _, err := manager.Confirm(user, pending.NewToken); err != nil {
		t.Fatalf("new-side first: %v", err)
	}
	res, err := manager.Confirm(user, pending.OldToken)
	if err != nil || res.Status != StatusSwitched || !res.Active.Equal(chB) {
		t.Fatalf("old-side second = %+v, %v", res, err)
	}
}

func TestResponseLossIsIdempotent(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "loss"
	provisionUser(t, store, user, chA)

	pending, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}

	r1, err := manager.Confirm(user, pending.OldToken)
	if err != nil || r1.Status != StatusPending {
		t.Fatalf("first old confirm: %+v %v", r1, err)
	}
	r2, err := manager.Confirm(user, pending.OldToken)
	if err != nil || r2.Status != StatusAlreadyConfirmed {
		t.Fatalf("replayed old confirm = %+v, %v; want already_confirmed", r2, err)
	}

	r3, err := manager.Confirm(user, pending.NewToken)
	if err != nil || r3.Status != StatusSwitched {
		t.Fatalf("new confirm: %+v %v", r3, err)
	}
	// Retry of the new-side click after the response was lost: the host
	// reports the completed switch idempotently.
	r4, err := manager.Confirm(user, pending.NewToken)
	if err != nil || r4.Status != StatusSwitched || !r4.Active.Equal(chB) {
		t.Fatalf("replayed new confirm = %+v, %v", r4, err)
	}
	// Old-side replay after the switch returns the same terminal result and
	// never reopens the transaction.
	r5, err := manager.Confirm(user, pending.OldToken)
	if err != nil || r5.Status != StatusSwitched || !r5.Active.Equal(chB) {
		t.Fatalf("post-switch old replay = %+v, %v", r5, err)
	}

	state, _ := manager.State(user)
	if !state.Active.Equal(chB) || state.Pending != nil || len(state.Past) != 1 {
		t.Fatalf("unexpected state after retries: %+v", state)
	}
}

func TestDeadlineBoundary(t *testing.T) {
	manager, store, clock := newTestManager(t)
	const user = "ttl"
	provisionUser(t, store, user, chA)

	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock.Set(start)
	pending, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}

	// One nanosecond before the deadline: live.
	clock.Set(start.Add(testTTL - time.Nanosecond))
	res, err := manager.Confirm(user, pending.OldToken)
	if err != nil || res.Status != StatusPending {
		t.Fatalf("confirm just before deadline = %+v, %v", res, err)
	}

	// Exactly at the deadline: strict expiry.
	clock.Set(start.Add(testTTL))
	if _, err := manager.Confirm(user, pending.NewToken); !errors.Is(err, ErrProofExpired) {
		t.Fatalf("confirm at deadline = %v; want ErrProofExpired", err)
	}

	// The recorded old-side proof also cannot be used past the deadline.
	clock.Set(start.Add(testTTL + time.Minute))
	if _, err := manager.Confirm(user, pending.OldToken); !errors.Is(err, ErrProofExpired) {
		t.Fatalf("old proof after deadline = %v; want ErrProofExpired", err)
	}

	state, _ := manager.State(user)
	if !state.Active.Equal(chA) || state.Pending == nil {
		t.Fatal("expired transaction changed the active channel or vanished")
	}

	// A fresh change is still possible and leaves a definite active channel.
	clock.Set(start.Add(2 * time.Hour))
	next, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("second BeginChange: %v", err)
	}
	if _, err := manager.Confirm(user, next.OldToken); err != nil {
		t.Fatalf("new old confirm: %v", err)
	}
	if _, err := manager.Confirm(user, next.NewToken); err != nil {
		t.Fatalf("new new confirm: %v", err)
	}
	state, _ = manager.State(user)
	if !state.Active.Equal(chB) {
		t.Fatalf("active after redo = %v; want %v", state.Active, chB)
	}
}

func TestCancelRevokesProofs(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "cancel"
	provisionUser(t, store, user, chA)

	pending, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}
	if _, err := manager.Confirm(user, pending.OldToken); err != nil {
		t.Fatalf("old confirm: %v", err)
	}

	if err := manager.CancelChange(user); err != nil {
		t.Fatalf("CancelChange: %v", err)
	}

	for _, token := range []string{pending.OldToken, pending.NewToken} {
		if _, err := manager.Confirm(user, token); !errors.Is(err, ErrProofRevoked) {
			t.Fatalf("confirm after cancel = %v; want ErrProofRevoked", err)
		}
	}

	state, _ := manager.State(user)
	if !state.Active.Equal(chA) || state.Pending != nil {
		t.Fatalf("cancel altered active channel or left pending: %+v", state)
	}

	if err := manager.CancelChange(user); !errors.Is(err, ErrNoPending) {
		t.Fatalf("second cancel = %v; want ErrNoPending", err)
	}
}

func TestSecondChangeSupersedesFirst(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "second"
	provisionUser(t, store, user, chA)

	first, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("first BeginChange: %v", err)
	}
	if _, err := manager.Confirm(user, first.OldToken); err != nil {
		t.Fatalf("first old confirm: %v", err)
	}

	second, err := manager.BeginChange(user, chA, chC)
	if err != nil {
		t.Fatalf("second BeginChange: %v", err)
	}

	state, _ := manager.State(user)
	if state.Pending == nil || state.Pending.Digest != second.Digest {
		t.Fatalf("second change did not become the live transaction: %+v", state)
	}

	for _, token := range []string{first.OldToken, first.NewToken} {
		if _, err := manager.Confirm(user, token); !errors.Is(err, ErrProofRevoked) {
			t.Fatalf("stale proof = %v; want ErrProofRevoked", err)
		}
	}

	if _, err := manager.Confirm(user, second.OldToken); err != nil {
		t.Fatalf("second old confirm: %v", err)
	}
	res, err := manager.Confirm(user, second.NewToken)
	if err != nil || res.Status != StatusSwitched {
		t.Fatalf("second new confirm = %+v, %v", res, err)
	}
	state, _ = manager.State(user)
	if !state.Active.Equal(chC) {
		t.Fatalf("active = %v; want %v", state.Active, chC)
	}
	if state.Past[len(state.Past)-1].Outcome != OutcomeSwitched {
		t.Fatal("terminal outcome not recorded")
	}
}

func TestCrossUserAndTamperedTokensRejected(t *testing.T) {
	manager, store, _ := newTestManager(t)
	provisionUser(t, store, "u1", chA)
	provisionUser(t, store, "u2", chA)

	pending, err := manager.BeginChange("u1", chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}

	if _, err := manager.Confirm("u2", pending.OldToken); !errors.Is(err, ErrSideMismatch) {
		t.Fatalf("cross-user token = %v; want ErrSideMismatch", err)
	}

	otherSigner, err := newSigner(append(append([]byte(nil), testKey...), 'x'))
	if err != nil {
		t.Fatalf("newSigner: %v", err)
	}
	legitClaims, err := manager.sign.parse(pending.OldToken)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	forged, err := otherSigner.issue(legitClaims)
	if err != nil {
		t.Fatalf("issue forged: %v", err)
	}
	if _, err := manager.Confirm("u1", forged); err == nil {
		t.Fatal("forged token with foreign key accepted")
	}

	for _, garbage := range []string{"not-a-token", "", pending.OldToken[:3]} {
		if _, err := manager.Confirm("u1", garbage); err == nil {
			t.Fatalf("malformed token %q accepted", garbage)
		}
	}
}

func TestSideBindingPreventsCrossChannelReplay(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "side"
	provisionUser(t, store, user, chA)
	pending, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}

	// Tampering with the token body invalidates the HMAC signature.
	tampered := pending.OldToken[:len(pending.OldToken)-1]
	if _, err := manager.Confirm(user, tampered); err == nil {
		t.Fatal("truncated token accepted")
	}

	// Unknown side values cannot be forged through the public API.
	claims, err := manager.sign.parse(pending.OldToken)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	claims.Side = "attacker"
	forged, err := manager.sign.issue(claims)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := manager.Confirm(user, forged); !errors.Is(err, ErrSideMismatch) {
		t.Fatalf("unknown side = %v; want ErrSideMismatch", err)
	}

	if _, err := manager.Confirm(user, pending.OldToken); err != nil {
		t.Fatalf("genuine old confirm: %v", err)
	}
	if _, err := manager.Confirm(user, pending.NewToken); err != nil {
		t.Fatalf("genuine new confirm: %v", err)
	}
	state, _ := manager.State(user)
	if !state.Active.Equal(chB) {
		t.Fatalf("active = %v; want %v", state.Active, chB)
	}
}

func TestConcurrentSecondChangeRetriesAndWins(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "race"
	provisionUser(t, store, user, chA)

	first, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("first BeginChange: %v", err)
	}

	// Force the first CAS of the second Begin to lose a simulated race: the
	// store advances the version underneath the manager's base snapshot.
	flaked := false
	store.setConflictHook(func(string, *State) error {
		if !flaked {
			flaked = true
			return ErrConflict
		}
		return nil
	})

	second, err := manager.BeginChange(user, chA, chC)
	if err != nil {
		t.Fatalf("second BeginChange with CAS retry: %v", err)
	}
	if !flaked {
		t.Fatal("conflict hook did not fire")
	}

	if _, err := manager.Confirm(user, first.OldToken); !errors.Is(err, ErrProofRevoked) {
		t.Fatalf("first-change proof after race = %v; want ErrProofRevoked", err)
	}
	if _, err := manager.Confirm(user, second.OldToken); err != nil {
		t.Fatalf("second old confirm: %v", err)
	}
	if _, err := manager.Confirm(user, second.NewToken); err != nil {
		t.Fatalf("second new confirm: %v", err)
	}
	state, _ := manager.State(user)
	if !state.Active.Equal(chC) {
		t.Fatalf("active = %v; want %v", state.Active, chC)
	}
}

func TestEventStreamObservesLifecycle(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "events"
	provisionUser(t, store, user, chA)

	var events []Event
	manager.Observe = func(ev Event) { events = append(events, ev) }

	pending, err := manager.BeginChange(user, chA, chB)
	if err != nil {
		t.Fatalf("BeginChange: %v", err)
	}
	if _, err := manager.Confirm(user, pending.OldToken); err != nil {
		t.Fatalf("old confirm: %v", err)
	}
	if _, err := manager.Confirm(user, pending.NewToken); err != nil {
		t.Fatalf("new confirm: %v", err)
	}

	got := make([]string, len(events))
	for i, ev := range events {
		got[i] = ev.Type
	}
	want := []string{EventChangeBegun, EventChangeConfirmed, EventChangeConfirmed, EventChannelSwitched}
	if len(got) != len(want) {
		t.Fatalf("events = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %s; want %s (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestBeginValidation(t *testing.T) {
	manager, store, _ := newTestManager(t)
	const user = "validate"
	provisionUser(t, store, user, chA)

	if _, err := manager.BeginChange(user, chA, chA); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("identical channels = %v; want ErrInvalidChannel", err)
	}
	if _, err := manager.BeginChange(user, chB, chC); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("stale old channel = %v; want ErrInvalidChannel", err)
	}
	if _, err := manager.BeginChange(user, Channel{}, chB); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("empty old channel = %v; want ErrInvalidChannel", err)
	}
	if _, err := manager.BeginChange("ghost", chA, chB); !errors.Is(err, ErrNoState) {
		t.Fatalf("unknown user = %v; want ErrNoState", err)
	}
}

func TestStoreGuardPaths(t *testing.T) {
	store := NewMemoryStore()

	if _, err := store.Load("nobody"); !errors.Is(err, ErrNoState) {
		t.Fatalf("load unknown = %v; want ErrNoState", err)
	}
	if _, err := store.Provision("u", Channel{}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("provision invalid = %v; want ErrInvalidChannel", err)
	}
	if _, err := store.Provision("u", chA); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := store.Provision("u", chB); !errors.Is(err, ErrStateVersion) {
		t.Fatalf("duplicate provision = %v; want ErrStateVersion", err)
	}

	base, err := store.Load("u")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// A stale base version loses the compare-and-swap.
	stale := base.Clone()
	if _, err := store.CommitCAS("u", base, func(s *State) (*State, error) {
		s.Version++
		return s, nil
	}); err != nil {
		t.Fatalf("first CAS: %v", err)
	}
	if _, err := store.CommitCAS("u", stale, func(s *State) (*State, error) {
		s.Version++
		return s, nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS = %v; want ErrConflict", err)
	}

	// A mutation that fails to bump the version is rejected.
	current, _ := store.Load("u")
	if _, err := store.CommitCAS("u", current, func(s *State) (*State, error) {
		return s, nil
	}); !errors.Is(err, ErrStateVersion) {
		t.Fatalf("unversioned CAS = %v; want ErrStateVersion", err)
	}
}

func TestStateMachineGuardPaths(t *testing.T) {
	var nilState *State
	if _, _, err := nilState.Begin(chA, chB, "d", 1, 2); !errors.Is(err, ErrNoState) {
		t.Fatalf("nil begin = %v; want ErrNoState", err)
	}
	if _, _, err := nilState.Confirm(SideOld, "d", 1, 2); !errors.Is(err, ErrNoState) {
		t.Fatalf("nil confirm = %v; want ErrNoState", err)
	}
	if _, _, err := nilState.Cancel(1); !errors.Is(err, ErrNoState) {
		t.Fatalf("nil cancel = %v; want ErrNoState", err)
	}
	if nilState.Clone() != nil {
		t.Fatal("nil clone should be nil")
	}

	state := &State{Version: 1, Active: chA}
	if _, _, err := state.Begin(chA, chA, "d", 1, 2); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("same-channel begin = %v; want ErrInvalidChannel", err)
	}
	if _, _, err := state.Begin(chA, chB, "d", 5, 5); !errors.Is(err, ErrProofExpired) {
		t.Fatalf("non-positive TTL begin = %v; want ErrProofExpired", err)
	}
	if _, _, err := state.Confirm("bogus", "d", 1, 2); !errors.Is(err, ErrProofRevoked) {
		t.Fatalf("confirm without pending = %v; want ErrProofRevoked", err)
	}
	if _, _, err := state.Cancel(1); !errors.Is(err, ErrNoPending) {
		t.Fatalf("cancel without pending = %v; want ErrNoPending", err)
	}

	// Unknown side on a live transaction is rejected before expiry checks.
	tx, _, err := state.Begin(chA, chB, "digest", 10, 20)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if tx.Digest != "digest" {
		t.Fatalf("digest = %q; want digest", tx.Digest)
	}
	if _, _, err := state.Confirm("rogue", "digest", 11, 20); !errors.Is(err, ErrSideMismatch) {
		t.Fatalf("rogue side = %v; want ErrSideMismatch", err)
	}
}

func TestNewManagerRejectsWeakKeyAndBadTTL(t *testing.T) {
	if _, err := NewManager(NewMemoryStore(), []byte("short")); err == nil {
		t.Fatal("weak HMAC key accepted")
	}
	if _, err := NewManager(NewMemoryStore(), testKey, WithTTL(0)); !errors.Is(err, ErrProofExpired) {
		t.Fatalf("zero TTL = %v; want ErrProofExpired", err)
	}
}
