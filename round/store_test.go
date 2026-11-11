package round

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newFileStore(t *testing.T) *FileStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "round.events")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	return store
}

func fullOperation(t *testing.T, r *Round, secrets map[MemberID][]byte, idx int) error {
	ctx := context.Background()
	if idx < len(testRoster) {
		member := testRoster[idx]
		commit := Commitment(r.cfg.ID, member, secrets[member])
		_, err := r.SubmitCommit(ctx, CommitMessage{RoundID: r.cfg.ID, Member: member, Commit: commit})
		return err
	}
	member := testRoster[idx-len(testRoster)]
	_, err := r.SubmitReveal(ctx, RevealMessage{RoundID: r.cfg.ID, Member: member, Secret: secrets[member]})
	return err
}

// TestPowerLossAtEveryStep kills the process simulation before each fsync, one
// event at a time, and proves replay recovers exactly the durable prefix and
// can still reach the same seed without ever exposing it prematurely.
func TestPowerLossAtEveryStep(t *testing.T) {
	totalOps := 2 * len(testRoster)
	secrets := memberSecrets(t)
	want := expectedSeed("crash", testRoster, secrets)

	// failAt 0 loses the creation event; values 1..totalOps lose a member
	// event; failAt totalOps+1 loses the completion event itself. All later
	// replay runs must still converge on the same seed.
	for failAt := 0; failAt <= totalOps+1; failAt++ {
		t.Run(fmt.Sprintf("crash_before_durable_event_%d", failAt), func(t *testing.T) {
			store := newFileStore(t)
			var crashCount int
			store.preSync = func(*Event) error {
				if crashCount == failAt {
					crashCount++
					return errSimulatedPowerLoss
				}
				crashCount++
				return nil
			}

			var r *Round
			crashed := false
			if failAt == 0 {
				var err error
				r, err = Start(context.Background(), Config{ID: "crash", Members: testRoster}, store)
				if !errors.Is(err, errSimulatedPowerLoss) {
					t.Fatalf("Start should lose power, got r=%v err=%v", r, err)
				}
				crashed = true
			} else {
				var err error
				r, err = Start(context.Background(), Config{ID: "crash", Members: testRoster}, store)
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				for op := 0; op < totalOps; op++ {
					if err := fullOperation(t, r, secrets, op); err != nil {
						if !errors.Is(err, errSimulatedPowerLoss) {
							t.Fatalf("op %d: unexpected error %v", op, err)
						}
						crashed = true
						break
					}
				}
				if !crashed && r.Status() != StatusCompleted {
					// The lost fsync was the completion event: all six member
					// events were durable, so the round looks open in memory
					// only because its final append never synced.
					crashed = true
				}
			}
			if !crashed {
				t.Fatalf("expected a simulated power loss at durable event %d", failAt)
			}

			// The in-memory round is now broken and must refuse further writes.
			if failAt != 0 {
				if _, err := r.Seed(); !errors.Is(err, ErrNoSeed) {
					t.Fatalf("seed after crash: %v", err)
				}
				_, err := r.SubmitCommit(context.Background(), CommitMessage{RoundID: "crash", Member: testRoster[0], Commit: Digest{}})
				if !errors.Is(err, ErrStoreBroken) {
					t.Fatalf("write after power loss: %v", err)
				}
			} else if _, err := OpenFileStore(store.Path()); err != nil {
				t.Fatalf("reopen store after total loss: %v", err)
			}

			// Restart: a fresh process opens the same file and replays it.
			reopened, err := OpenFileStore(store.Path())
			if err != nil {
				t.Fatal(err)
			}
			var restarted *Round
			if failAt == 0 {
				// Nothing durable survived; start a fresh round on the file.
				restarted, err = Start(context.Background(), Config{ID: "crash", Members: testRoster}, reopened)
			} else {
				restarted, err = Replay(context.Background(), reopened)
			}
			if err != nil {
				t.Fatalf("Replay after power loss: %v", err)
			}
			if _, err := restarted.Seed(); !errors.Is(err, ErrNoSeed) {
				t.Fatalf("seed must not exist before all members match: %v", err)
			}

			// Re-run the remaining operations from the crash point onwards.
			// Events already durable are simply idempotent duplicates.
			for op := 0; op < totalOps; op++ {
				if err := fullOperation(t, restarted, secrets, op); err != nil {
					t.Fatalf("re-run op %d: %v", op, err)
				}
			}
			seed, err := restarted.Seed()
			if err != nil {
				t.Fatalf("Seed after recovery: %v", err)
			}
			if string(seed) != string(want[:]) {
				t.Fatal("recovered seed differs from the uninterrupted seed")
			}

			// One more full replay must converge to the identical final state.
			finalStore, err := OpenFileStore(store.Path())
			if err != nil {
				t.Fatal(err)
			}
			final, err := Replay(context.Background(), finalStore)
			if err != nil || final.Status() != StatusCompleted {
				t.Fatalf("final replay: status=%s err=%v", final.Status(), err)
			}
			seed3, _ := final.Seed()
			if string(seed3) != string(want[:]) {
				t.Fatal("final replay produced a different seed")
			}
		})
	}
}

var errSimulatedPowerLoss = errors.New("simulated power loss before fsync")

func TestTornTailLineIsDiscarded(t *testing.T) {
	store := newFileStore(t)
	r := newTestRound(t, store, "r1")
	secrets := memberSecrets(t)

	for _, member := range testRoster[:2] {
		commit := Commitment("r1", member, secrets[member])
		if _, err := r.SubmitCommit(context.Background(), CommitMessage{RoundID: "r1", Member: member, Commit: commit}); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate a torn final write: append an unterminated, corrupt line.
	file, err := os.OpenFile(store.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("deadbeef partial-line-without-newline")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenFileStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Replay(context.Background(), reopened)
	if err != nil {
		t.Fatalf("torn tail should be tolerated: %v", err)
	}
	if got := len(replayed.Snapshot().Committed); got != 2 {
		t.Fatalf("committed after replay = %d, want 2", got)
	}

	// Appending after the torn tail must still work and resume sequencing.
	last, _ := reopened.Read(context.Background())
	if len(last) == 0 {
		t.Fatal("no durable events survived")
	}
	nextSeq := last[len(last)-1].Seq + 1
	member := testRoster[2]
	commit := Commitment("r1", member, secrets[member])
	receipt, err := replayed.SubmitCommit(context.Background(), CommitMessage{RoundID: "r1", Member: member, Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EventSeq != nextSeq {
		t.Fatalf("new event seq = %d, want %d", receipt.EventSeq, nextSeq)
	}
}

func TestCorruptMiddleLineRejected(t *testing.T) {
	store := newFileStore(t)
	r := newTestRound(t, store, "r1")
	secrets := memberSecrets(t)
	submitAll(t, r, secrets)

	events, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 4 {
		t.Fatalf("not enough events: %d", len(events))
	}

	// Flip a payload byte inside the first line, keeping framing intact.
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	// First line layout: 64 hex chars, space, payload, newline.
	raw[70] ^= 0xff
	if err := os.WriteFile(store.Path(), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenFileStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(context.Background(), reopened); !errors.Is(err, ErrCorruptLog) {
		t.Fatalf("corrupt middle line: %v", err)
	}
}

func TestStartRequiresEmptyLogAndReplayRequiresEvents(t *testing.T) {
	store := newFileStore(t)
	ctx := context.Background()

	if _, err := Replay(ctx, store); !errors.Is(err, ErrEmptyLog) {
		t.Fatalf("empty replay: %v", err)
	}

	r, err := Start(ctx, Config{ID: "r1", Members: testRoster}, store)
	if err != nil {
		t.Fatal(err)
	}
	secrets := memberSecrets(t)
	submitAll(t, r, secrets)

	if _, err := Start(ctx, Config{ID: "r1", Members: testRoster}, store); !errors.Is(err, ErrLogNotEmpty) {
		t.Fatalf("restart on a live log: %v", err)
	}
}

func TestSeedNeverLeaksFromEventsBeforeCompletion(t *testing.T) {
	store := newFileStore(t)
	r := newTestRound(t, store, "r1")
	secrets := memberSecrets(t)
	ctx := context.Background()

	for i, member := range testRoster {
		commit := Commitment("r1", member, secrets[member])
		if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: commit}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: member, Secret: secrets[member]}); err != nil {
			t.Fatal(err)
		}
		if i < len(testRoster)-1 {
			events, err := store.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range events {
				if ev.Kind == EvRoundCompleted {
					t.Fatal("completion event appeared before all members matched")
				}
				if ev.Seed != (Digest{}) {
					t.Fatal("seed bytes appeared in a non-completion event")
				}
			}
			if _, err := r.Seed(); !errors.Is(err, ErrNoSeed) {
				t.Fatalf("seed leaked early after %d members", i+1)
			}
		}
	}

	if r.Status() != StatusCompleted {
		t.Fatalf("status = %s", r.Status())
	}
	seed, err := r.Seed()
	if err != nil {
		t.Fatal(err)
	}
	events, _ := store.Read(ctx)
	last := events[len(events)-1]
	if last.Kind != EvRoundCompleted || string(last.Seed[:]) != string(seed) {
		t.Fatal("seed must be persisted exactly once, in the completion event")
	}
	var completionEvents int
	for _, ev := range events {
		if ev.Kind == EvRoundCompleted {
			completionEvents++
		}
	}
	if completionEvents != 1 {
		t.Fatalf("completion events = %d, want 1", completionEvents)
	}
}

func TestInvalidConfigs(t *testing.T) {
	ctx := context.Background()
	cases := []Config{
		{ID: "", Members: testRoster},
		{ID: "r1", Members: []MemberID{"only"}},
		{ID: "r1", Members: []MemberID{"a", "a"}},
		{ID: "r1", Members: []MemberID{"a", ""}},
	}
	for i, cfg := range cases {
		if _, err := Start(ctx, cfg, newFileStore(t)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestAbortAfterCompletionRejected(t *testing.T) {
	store := newFileStore(t)
	r := newTestRound(t, store, "r1")
	secrets := memberSecrets(t)
	submitAll(t, r, secrets)

	if err := r.Abort(context.Background(), "too late"); !errors.Is(err, ErrCompleted) {
		t.Fatalf("abort after completion: %v", err)
	}
	if r.Status() != StatusCompleted {
		t.Fatalf("status = %s", r.Status())
	}
}
