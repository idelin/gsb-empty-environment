package netsec

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func mutateCopy(data []byte) []byte {
	out := append([]byte(nil), data...)
	out[0] ^= 0xff
	return out
}

// TestShareBindingTamper proves every bound field is authenticated.
func TestShareBindingTamper(t *testing.T) {
	state, _, _, _ := newTestState(t)
	shares := testShares(t, state)

	cases := []struct {
		name string
		edit func(Share) Share
		want error
	}{
		{"nonce", func(s Share) Share { s.Nonce = mutateCopy(s.Nonce); return s }, ErrNonceMismatch},
		{"seal id", func(s Share) Share { s.SealID = "other-seal"; return s }, ErrSealConfigMismatch},
		{"holder", func(s Share) Share { s.HolderID = "bob"; return s }, ErrHolderIndexMismatch},
		{"index", func(s Share) Share { s.Index = 2; return s }, ErrHolderIndexMismatch},
		{"fragment", func(s Share) Share { s.Fragment = mutateCopy(s.Fragment); return s }, ErrInvalidTag},
		{"tag", func(s Share) Share { s.Tag = mutateCopy(s.Tag); return s }, ErrInvalidTag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := state.SubmitShare(context.Background(), tc.edit(shares["alice"]))
			assertErrorIs(t, err, tc.want)
		})
	}
}

// TestUnknownHolder rejects shares from holders outside the seal set.
func TestUnknownHolder(t *testing.T) {
	state, _, _, _ := newTestState(t)
	shares := testShares(t, state)

	share := shares["alice"]
	share.HolderID = "mallory"
	_, err := state.SubmitShare(context.Background(), share)
	assertErrorIs(t, err, ErrUnknownHolder)
}

// TestSubmitBeforeInitialize rejects submissions to an uninitialized state.
func TestSubmitBeforeInitialize(t *testing.T) {
	_, stateKey := testConfig()
	state := NewState(StateConfig{StateKey: stateKey})

	_, err := state.SubmitShare(context.Background(), Share{Generation: 1})
	assertErrorIs(t, err, ErrNotInitialized)
}

// TestSnapshotRestartAcceptsOnlyCurrentGeneration exports a sealed snapshot,
// imports it into a fresh instance (simulating restart) and shows that old
// shares remain stale while current shares still work.
func TestSnapshotRestartAcceptsOnlyCurrentGeneration(t *testing.T) {
	state, _, _, stateKey := newTestState(t)
	shares := testShares(t, state)
	generation := state.Generation()
	nonce := state.Nonce()

	if _, err := state.SubmitShare(context.Background(), shares["alice"]); err != nil {
		t.Fatalf("alice: %v", err)
	}
	snapshot, err := state.ExportSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	restarted := NewState(StateConfig{
		StateKey:  stateKey,
		AuditSink: &MemoryAuditSink{},
		Notifier:  &SliceNotifier{},
	})
	if err := restarted.ImportSnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("import: %v", err)
	}
	if restarted.Generation() != generation || !bytes.Equal(restarted.Nonce(), nonce) {
		t.Fatal("restarted state lost generation or nonce")
	}
	if restarted.PendingCount() != 1 {
		t.Fatalf("restarted pending count = %d, want 1", restarted.PendingCount())
	}

	// Replaying the already-recorded share after restart is still rejected.
	if _, err := restarted.SubmitShare(context.Background(), shares["alice"]); !errors.Is(err, ErrDuplicateShare) {
		t.Fatalf("replay after restart err = %v, want ErrDuplicateShare", err)
	}

	// Complete the unseal with the remaining current-generation shares.
	for _, id := range []string{"bob", "carol"} {
		unsealed, err := restarted.SubmitShare(context.Background(), shares[id])
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if unsealed != (id == "carol") {
			t.Fatalf("unsealed flag for %s unexpected", id)
		}
	}
	if !restarted.IsUnsealed() {
		t.Fatal("restarted state should unseal with current-generation shares")
	}
}

// TestSnapshotRollbackRejected exports a new snapshot after rotation and
// refuses importing an older snapshot into an instance that already observed
// the newer generation.
func TestSnapshotRollbackRejected(t *testing.T) {
	state, _, _, stateKey := newTestState(t)
	_ = testShares(t, state)

	oldSnapshot, err := state.ExportSnapshot()
	if err != nil {
		t.Fatalf("export old: %v", err)
	}

	if _, err := state.Rotate(context.Background(), state.SealConfigView()); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	newSnapshot, err := state.ExportSnapshot()
	if err != nil {
		t.Fatalf("export new: %v", err)
	}

	restarted := NewState(StateConfig{StateKey: stateKey})
	if err := restarted.ImportSnapshot(context.Background(), newSnapshot); err != nil {
		t.Fatalf("import new: %v", err)
	}

	err = restarted.ImportSnapshot(context.Background(), oldSnapshot)
	assertErrorIs(t, err, ErrGenerationRollback)
	if restarted.Generation() != state.Generation() {
		t.Fatal("rollback import must not change the active generation")
	}
}

// TestSnapshotTamperRejected flips a snapshot byte and expects an integrity
// failure rather than a state change.
func TestSnapshotTamperRejected(t *testing.T) {
	state, _, _, stateKey := newTestState(t)
	_ = testShares(t, state)

	snapshot, err := state.ExportSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	snapshot[len(snapshot)-1] ^= 0xff

	restarted := NewState(StateConfig{StateKey: stateKey})
	err = restarted.ImportSnapshot(context.Background(), snapshot)
	assertErrorIs(t, err, ErrSnapshotIntegrity)
	if restarted.Phase() != phaseUninitialized {
		t.Fatal("tampered import must leave state uninitialized")
	}
}

// TestShamirRoundTrip verifies interpolation reconstructs the secret for every
// threshold-sized subset, and that below-threshold subsets do not.
func TestShamirRoundTrip(t *testing.T) {
	secret := bytes.Repeat([]byte{0x7a}, unsealKeyLength)
	fragments, err := splitSecret(secret, 3, 5, deterministicReader())
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	subsets := [][]int{{0, 1, 2}, {0, 3, 4}, {1, 2, 4}, {2, 3, 4}}
	for _, subset := range subsets {
		parts := make([]sharePart, 0, len(subset))
		for _, idx := range subset {
			parts = append(parts, sharePart{index: byte(idx + 1), fragment: fragments[idx]})
		}
		if got := combineSecret(parts, len(secret)); !bytes.Equal(got, secret) {
			t.Fatalf("subset %v reconstructed wrong secret", subset)
		}
	}
}
