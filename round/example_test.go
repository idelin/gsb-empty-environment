package round_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"example.org/netsec/uid1274/round"
)

// TestExampleHostFlow shows a host driving one round with a fixed roster,
// persisting it to disk, and recovering through Replay. It also demonstrates
// that an absent member produces no seed and must be handled by aborting.
func TestExampleHostFlow(t *testing.T) {
	ctx := context.Background()
	roster := []round.MemberID{"alice", "bob", "carol"}

	dir := t.TempDir()
	store, err := round.OpenFileStore(filepath.Join(dir, "drill-round.events"))
	if err != nil {
		t.Fatal(err)
	}

	r, err := round.Start(ctx, round.Config{ID: "drill-2026-11-11", Members: roster}, store)
	if err != nil {
		t.Fatal(err)
	}

	// In a real deployment each member generates and keeps its own secret;
	// the host only sees commitments first, then reveals.
	secrets := map[round.MemberID][]byte{}
	for _, member := range roster {
		secret, err := round.NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		secrets[member] = secret
		commit := round.Commitment(r.Snapshot().RoundID, member, secret)
		if _, err := r.SubmitCommit(ctx, round.CommitMessage{
			RoundID: r.Snapshot().RoundID,
			Member:  member,
			Commit:  commit,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Identical repeats are safe: they persist as duplicate events and change
	// no state. Conflicting values would instead be retained as evidence.
	for _, member := range roster {
		receipt, err := r.SubmitReveal(ctx, round.RevealMessage{
			RoundID: r.Snapshot().RoundID,
			Member:  member,
			Secret:  secrets[member],
		})
		if err != nil || !receipt.Accepted {
			t.Fatalf("reveal %s failed: %+v %v", member, receipt, err)
		}
	}

	seed, err := r.Seed()
	fmt.Printf("completed=%v seed-bytes=%d err=%v\n", r.Status() == round.StatusCompleted, len(seed), err)

	// A restarted process rebuilds the identical round from the event log.
	reopened, err := round.OpenFileStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := round.Replay(ctx, reopened)
	if err != nil {
		t.Fatal(err)
	}
	replayedSeed, err := replayed.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if !equalSlices(replayedSeed, seed) {
		t.Fatal("replayed seed differs")
	}

	// Absence never yields a zero-filled seed: a short roster round aborts.
	absentStore, err := round.OpenFileStore(filepath.Join(dir, "absent-round.events"))
	if err != nil {
		t.Fatal(err)
	}
	absent, err := round.Start(ctx, round.Config{ID: "absent", Members: roster}, absentStore)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(expectNoSeed(absent), round.ErrNoSeed) {
		t.Fatal("absent round must expose ErrNoSeed, not a zero seed")
	}
	if err := absent.Abort(ctx, "carol never responded"); err != nil {
		t.Fatal(err)
	}
	if absent.Status() != round.StatusAborted {
		t.Fatalf("status = %s", absent.Status())
	}
	if _, err := os.Stat(absentStore.Path()); err != nil {
		t.Fatalf("aborted round log missing: %v", err)
	}
}

func expectNoSeed(r *round.Round) error {
	_, err := r.Seed()
	return err
}

func equalSlices(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
