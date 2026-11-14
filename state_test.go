package netsec

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func testConfig() (SealConfig, []byte) {
	cfg := SealConfig{
		SealID:    "key-service-vault",
		Threshold: 3,
		Holders: []Holder{
			{ID: "alice", Key: bytes.Repeat([]byte{0x11}, 32)},
			{ID: "bob", Key: bytes.Repeat([]byte{0x22}, 32)},
			{ID: "carol", Key: bytes.Repeat([]byte{0x33}, 32)},
			{ID: "dave", Key: bytes.Repeat([]byte{0x44}, 32)},
			{ID: "erin", Key: bytes.Repeat([]byte{0x55}, 32)},
		},
	}
	stateKey := bytes.Repeat([]byte{0x99}, 32)
	return cfg, stateKey
}

func newTestState(t *testing.T) (*State, *MemoryAuditSink, *SliceNotifier, []byte) {
	t.Helper()
	cfg, stateKey := testConfig()
	sink := &MemoryAuditSink{}
	notifier := &SliceNotifier{}
	state := NewState(StateConfig{
		StateKey:  stateKey,
		AuditSink: sink,
		Notifier:  notifier,
	})
	if _, err := state.Initialize(context.Background(), cfg); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return state, sink, notifier, stateKey
}

func testShares(t *testing.T, state *State) map[string]Share {
	t.Helper()
	// Re-seal through the public path so the returned shares are genuine.
	cfg := state.SealConfigView()
	sealed, err := state.Rotate(context.Background(), cfg)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	return sealed.Shares
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}

func countByType(events []AuditEvent, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

// TestReplayRejected submits the same share twice and proves the second
// submission (the replay) is rejected while the first still counts.
func TestReplayRejected(t *testing.T) {
	state, _, _, _ := newTestState(t)
	shares := testShares(t, state)

	unsealed, err := state.SubmitShare(context.Background(), shares["alice"])
	if err != nil || unsealed {
		t.Fatalf("first submission: unsealed=%v err=%v", unsealed, err)
	}
	if state.PendingCount() != 1 {
		t.Fatalf("pending count = %d, want 1", state.PendingCount())
	}

	_, err = state.SubmitShare(context.Background(), shares["alice"])
	assertErrorIs(t, err, ErrDuplicateShare)

	if state.PendingCount() != 1 {
		t.Fatalf("pending count after replay = %d, want 1", state.PendingCount())
	}
	events := state.AuditEvents()
	if countByType(events, EventShareRejected) != 1 {
		t.Fatalf("expected one rejection audit event, got %d", countByType(events, EventShareRejected))
	}
}

// TestStaleGenerationRejected verifies that a share minted for an older
// generation cannot unseal the current generation after rotation.
func TestStaleGenerationRejected(t *testing.T) {
	state, _, _, _ := newTestState(t)
	oldShares := testShares(t, state)
	oldGeneration := state.Generation()

	if _, err := state.Rotate(context.Background(), state.SealConfigView()); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if state.Generation() != oldGeneration+1 {
		t.Fatalf("generation = %d, want %d", state.Generation(), oldGeneration+1)
	}

	_, err := state.SubmitShare(context.Background(), oldShares["alice"])
	assertErrorIs(t, err, ErrStaleGeneration)
}

// TestFutureGenerationRejected verifies a share carrying a generation that has
// not been reached yet is refused.
func TestFutureGenerationRejected(t *testing.T) {
	state, _, _, _ := newTestState(t)
	shares := testShares(t, state)

	share := shares["alice"]
	share.Generation++
	_, err := state.SubmitShare(context.Background(), share)
	assertErrorIs(t, err, ErrFutureGeneration)
}

// TestThresholdNotReached proves fewer than threshold shares never unseal.
func TestThresholdNotReached(t *testing.T) {
	state, _, _, _ := newTestState(t)
	shares := testShares(t, state)

	for _, id := range []string{"alice", "bob"} {
		unsealed, err := state.SubmitShare(context.Background(), shares[id])
		if err != nil || unsealed {
			t.Fatalf("submission %s: unsealed=%v err=%v", id, unsealed, err)
		}
	}
	if state.IsUnsealed() {
		t.Fatal("state must remain sealed below threshold")
	}
	if _, err := state.UnsealKey(); !errors.Is(err, ErrNotUnsealed) {
		t.Fatalf("UnsealKey err = %v, want ErrNotUnsealed", err)
	}
}

// TestLegalUnseal reconstructs the key with exactly threshold shares and
// verifies that further submissions are refused because unseal is one-time.
func TestLegalUnseal(t *testing.T) {
	state, sink, notifier, _ := newTestState(t)
	shares := testShares(t, state)

	for i, id := range []string{"alice", "bob", "carol"} {
		unsealed, err := state.SubmitShare(context.Background(), shares[id])
		if err != nil {
			t.Fatalf("submission %s: %v", id, err)
		}
		wantUnsealed := i == 2
		if unsealed != wantUnsealed {
			t.Fatalf("submission %d unsealed = %v, want %v", i, unsealed, wantUnsealed)
		}
	}
	if !state.IsUnsealed() {
		t.Fatal("state should be unsealed at threshold")
	}
	key, err := state.UnsealKey()
	if err != nil || len(key) != unsealKeyLength {
		t.Fatalf("unseal key: len=%d err=%v", len(key), err)
	}

	// One-time unseal: every later share, including valid ones, is rejected.
	_, err = state.SubmitShare(context.Background(), shares["dave"])
	assertErrorIs(t, err, ErrAlreadyUnsealed)

	events := state.AuditEvents()
	if countByType(events, EventUnsealed) != 1 {
		t.Fatalf("unsealed events = %d, want 1", countByType(events, EventUnsealed))
	}
	if countByType(sink.Events(), EventUnsealed) != 1 {
		t.Fatal("audit sink did not record the unseal event")
	}
	if countByType(notifier.Events(), EventUnsealed) != 1 {
		t.Fatal("notifier did not receive the unseal event")
	}
	var unsealEvent *AuditEvent
	for i := range events {
		if events[i].Type == EventUnsealed {
			unsealEvent = &events[i]
		}
	}
	if unsealEvent == nil || unsealEvent.ShareCount != state.Threshold() ||
		unsealEvent.Generation != state.Generation() || unsealEvent.Phase != phaseUnsealed {
		t.Fatalf("unexpected unseal audit event: %+v", unsealEvent)
	}
}

// TestRotationOnlyNewGenerationUnseals verifies the central anti-replay
// guarantee: after rotation, old shares cannot unseal and only shares of the
// new generation can complete an unseal.
func TestRotationOnlyNewGenerationUnseals(t *testing.T) {
	state, _, _, _ := newTestState(t)
	oldShares := testShares(t, state)

	// Collect enough old shares to reach the threshold, then rotate.
	for _, id := range []string{"alice", "bob"} {
		if _, err := state.SubmitShare(context.Background(), oldShares[id]); err != nil {
			t.Fatalf("old share %s: %v", id, err)
		}
	}
	sealed, err := state.Rotate(context.Background(), state.SealConfigView())
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if state.PendingCount() != 0 {
		t.Fatalf("pending count after rotation = %d, want 0", state.PendingCount())
	}

	// Every old share is now stale.
	for _, id := range []string{"alice", "bob", "carol"} {
		if _, err := state.SubmitShare(context.Background(), oldShares[id]); !errors.Is(err, ErrStaleGeneration) {
			t.Fatalf("old share %s err = %v, want ErrStaleGeneration", id, err)
		}
	}
	if state.IsUnsealed() {
		t.Fatal("stale shares must not unseal the new generation")
	}

	// New-generation shares reach the threshold and unseal.
	for _, id := range []string{"alice", "bob", "carol"} {
		unsealed, err := state.SubmitShare(context.Background(), sealed.Shares[id])
		if err != nil {
			t.Fatalf("new share %s: %v", id, err)
		}
		if unsealed != (id == "carol") {
			t.Fatalf("unsealed flag for %s unexpected", id)
		}
	}
	if !state.IsUnsealed() {
		t.Fatal("new generation should unseal with new-generation shares")
	}
}
