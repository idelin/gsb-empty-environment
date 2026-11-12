package taskpkg_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.org/domainreissue/q029"
)

func TestCommitHappyPathAndReopen(t *testing.T) {
	_, priv := mustKey(t)
	witness := &recordingWitness{local: taskpkg.NewLocalWitness(priv)}
	counter := taskpkg.NewMemoryCounter()
	committer, logPath := newRecoveredCommitter(t, witness, counter)
	ctx := context.Background()

	effect := &effectRecorder{}
	if err := committer.Commit(ctx, sampleOp("admin-1", "delete-user-42"), effect.effect); err != nil {
		t.Fatalf("commit op1: %v", err)
	}
	if err := committer.Commit(ctx, sampleOp("admin-1", "rotate-key-7"), effect.effect); err != nil {
		t.Fatalf("commit op2: %v", err)
	}
	if effect.count() != 2 {
		t.Fatalf("expected 2 side effects, got %d", effect.count())
	}

	entries := committer.Entries()
	if len(entries) != 2 || entries[0].Sequence != 1 || entries[1].Sequence != 2 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if string(entries[1].PriorCommit) != string(entries[0].Commitment) {
		t.Fatal("hash chain broken: prior commitment does not link to previous entry")
	}
	if string(entries[0].PriorCommit) != string(make([]byte, 32)) {
		t.Fatal("first entry must commit against the all-zero genesis value")
	}

	if err := committer.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := taskpkg.Open(ctx, taskpkg.Config{
		LogPath: logPath,
		Witness: witness,
		Counter: counter,
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if len(reopened.Entries()) != 2 {
		t.Fatalf("reopened log lost entries: %+v", reopened.Entries())
	}
	next, err := counter.Next()
	if err != nil || next != 3 {
		t.Fatalf("counter next = %d, err=%v", next, err)
	}
}

func TestSideEffectRunsOnlyAfterPersist(t *testing.T) {
	_, priv := mustKey(t)
	witness := &recordingWitness{local: taskpkg.NewLocalWitness(priv)}
	counter := taskpkg.NewMemoryCounter()
	committer, logPath := newRecoveredCommitter(t, witness, counter)

	ran := false
	err := committer.Commit(context.Background(), sampleOp("admin-1", "x"), func(_ context.Context) error {
		raw, readErr := os.ReadFile(logPath)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(raw), `"sequence":1`) {
			return errors.New("audit entry not persisted before side effect")
		}
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !ran {
		t.Fatal("side effect never ran")
	}
}

func TestCrossOperationReplayRejected(t *testing.T) {
	pub, priv := mustKey(t)
	witness := &recordingWitness{local: taskpkg.NewLocalWitness(priv)}
	counter := taskpkg.NewMemoryCounter()
	committer, logPath := newRecoveredCommitter(t, witness, counter)
	ctx := context.Background()

	effect := &effectRecorder{}
	if err := committer.Commit(ctx, sampleOp("admin-1", "op-A"), effect.effect); err != nil {
		t.Fatalf("commit op-A: %v", err)
	}
	oldConfirmation := witness.snapshot()[0]

	// Replaying an old, already-consumed confirmation against a different
	// operation fails local field binding; the side effect never runs.
	replay := &replayWitness{pub: pub, msg: oldConfirmation}
	replayCommitter, _ := newRecoveredCommitter(t, replay, taskpkg.NewMemoryCounter())
	replayEffect := &effectRecorder{}
	err := replayCommitter.Commit(ctx, sampleOp("admin-1", "op-B"), replayEffect.effect)
	if !errors.Is(err, taskpkg.ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature on replay, got %v", err)
	}

	// Pairing a log with a counter that pretends nothing was consumed (an old
	// counter snapshot) is refused at open time, so consumed sequences cannot
	// be reused against a restored local snapshot.
	rolledBackCounter := taskpkg.NewMemoryCounter() // next == 1, pretends nothing was consumed
	_, err = taskpkg.Open(ctx, taskpkg.Config{
		LogPath: logPath,
		Witness: witness,
		Counter: rolledBackCounter,
	})
	if !errors.Is(err, taskpkg.ErrLogCounterDesync) {
		t.Fatalf("expected ErrLogCounterDesync, got %v", err)
	}
	if replayEffect.count() != 0 {
		t.Fatalf("replay attempt must have zero side effects, count=%d", replayEffect.count())
	}
}

func TestCounterUnavailableZeroSideEffects(t *testing.T) {
	_, priv := mustKey(t)
	witness := &recordingWitness{local: taskpkg.NewLocalWitness(priv)}
	failing := taskpkg.NewFailCounter(taskpkg.NewMemoryCounter())
	failing.SetNextFailure(true)

	_, err := taskpkg.Recover(context.Background(), taskpkg.Config{
		LogPath: filepath.Join(t.TempDir(), "audit.log"),
		Witness: witness,
		Counter: failing,
	})
	if !errors.Is(err, taskpkg.ErrCounterUnavailable) {
		t.Fatalf("recover with dead counter: %v", err)
	}

	working := taskpkg.NewFailCounter(taskpkg.NewMemoryCounter())
	committer, logPath := newRecoveredCommitter(t, witness, working)

	// Next fails: no witness call, no side effect.
	working.SetNextFailure(true)
	effect := &effectRecorder{}
	err = committer.Commit(context.Background(), sampleOp("admin-1", "op"), effect.effect)
	if !errors.Is(err, taskpkg.ErrCounterUnavailable) {
		t.Fatalf("commit with dead Next: %v", err)
	}
	if len(witness.requests) != 0 {
		t.Fatal("witness must not be contacted when counter is unavailable")
	}

	// Consume fails: confirmation is discarded, nothing persisted or executed.
	working.SetNextFailure(false)
	working.SetConsumeFailure(true)
	err = committer.Commit(context.Background(), sampleOp("admin-1", "op"), effect.effect)
	if !errors.Is(err, taskpkg.ErrCounterUnavailable) {
		t.Fatalf("commit with dead Consume: %v", err)
	}
	raw, _ := os.ReadFile(logPath)
	if len(raw) != 0 {
		t.Fatalf("nothing may be persisted when Consume fails, got %q", raw)
	}

	// After recovery the operation commits cleanly with sequence 1.
	working.SetConsumeFailure(false)
	if err := committer.Commit(context.Background(), sampleOp("admin-1", "op"), effect.effect); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if effect.count() != 1 || committer.Entries()[0].Sequence != 1 {
		t.Fatalf("retry must produce exactly one committed effect, count=%d", effect.count())
	}
}

func TestLostConfirmationRetriesSafely(t *testing.T) {
	_, priv := mustKey(t)
	witness := &recordingWitness{
		local: taskpkg.NewLocalWitness(priv),
		fail:  taskpkg.ErrWitnessUnavailable,
	}
	counter := taskpkg.NewFailCounter(taskpkg.NewMemoryCounter())
	committer, _ := newRecoveredCommitter(t, witness, counter)
	ctx := context.Background()

	effect := &effectRecorder{}
	err := committer.Commit(ctx, sampleOp("admin-1", "op"), effect.effect)
	if !errors.Is(err, taskpkg.ErrWitnessUnavailable) {
		t.Fatalf("lost confirmation: %v", err)
	}
	if effect.count() != 0 || len(counter.ConsumedSequences()) != 0 || len(committer.Entries()) != 0 {
		t.Fatal("lost confirmation must leave zero side effects, zero consumption, empty log")
	}

	witness.fail = nil
	if err := committer.Commit(ctx, sampleOp("admin-1", "op"), effect.effect); err != nil {
		t.Fatalf("retry after lost confirmation: %v", err)
	}
	if effect.count() != 1 || len(witness.requests) != 2 {
		t.Fatalf("retry should issue a fresh confirmation, effects=%d requests=%d", effect.count(), len(witness.requests))
	}
	if witness.requests[0].Sequence != 1 || witness.requests[1].Sequence != 1 {
		t.Fatal("failed attempt must not consume its sequence")
	}
}
