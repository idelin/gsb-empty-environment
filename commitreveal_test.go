package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newEngine(t *testing.T, store Store, clock Clock) *Engine {
	t.Helper()
	engine, err := New(store, WithClock(clock))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

func mustStart(t *testing.T, engine *Engine, cfg RoundConfig) RoundView {
	t.Helper()
	view, outcome, err := engine.StartRound(cfg)
	if err != nil {
		t.Fatalf("StartRound: %v", err)
	}
	if outcome != Accepted {
		t.Fatalf("StartRound outcome = %v, want Accepted", outcome)
	}
	return view
}

func mustCommit(t *testing.T, engine *Engine, roundID, participant string, commitment Commitment) {
	t.Helper()
	outcome, err := engine.Commit(roundID, participant, commitment)
	if err != nil {
		t.Fatalf("Commit(%s): %v", participant, err)
	}
	if outcome != Accepted {
		t.Fatalf("Commit(%s) outcome = %v, want Accepted", participant, outcome)
	}
}

func mustReveal(t *testing.T, engine *Engine, roundID, participant string, nonce Nonce) {
	t.Helper()
	outcome, err := engine.Reveal(roundID, participant, nonce)
	if err != nil {
		t.Fatalf("Reveal(%s): %v", participant, err)
	}
	if outcome != Accepted {
		t.Fatalf("Reveal(%s) outcome = %v, want Accepted", participant, outcome)
	}
}

func testNonce(fill byte) Nonce {
	var nonce Nonce
	for i := range nonce {
		nonce[i] = fill + byte(i)
	}
	return nonce
}

func expectedCommitment(roundID, participant string, nonce Nonce) Commitment {
	var length [8]byte
	h := sha256.New()
	binary.BigEndian.PutUint64(length[:], uint64(len(roundID)))
	h.Write(length[:])
	h.Write([]byte(roundID))
	binary.BigEndian.PutUint64(length[:], uint64(len(participant)))
	h.Write(length[:])
	h.Write([]byte(participant))
	h.Write(nonce[:])
	var out Commitment
	copy(out[:], h.Sum(nil))
	return out
}

func expectedResult(nonces map[string]Nonce, sortedParticipants []string) Result {
	h := sha256.New()
	for _, participant := range sortedParticipants {
		nonce := nonces[participant]
		h.Write(nonce[:])
	}
	var out Result
	copy(out[:], h.Sum(nil))
	return out
}

func TestCommitmentEncoding(t *testing.T) {
	roundID := "round/2026-11-15"
	participant := "参加过\x00参与者"
	nonce := testNonce(0x40)

	got := CommitmentFor(roundID, participant, nonce)
	want := expectedCommitment(roundID, participant, nonce)
	if got != want {
		t.Fatalf("CommitmentFor = %x, want %x", got, want)
	}
	if !VerifyCommitment(got, roundID, participant, nonce) {
		t.Fatal("VerifyCommitment rejected the correct opening")
	}
	if VerifyCommitment(got, roundID+"x", participant, nonce) {
		t.Fatal("commitment verified against a different round")
	}
	if VerifyCommitment(got, roundID, participant+"x", nonce) {
		t.Fatal("commitment verified against a different participant")
	}
}

func TestRoundFlowAndResultOrder(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	store := NewMemoryStore()
	engine := newEngine(t, store, clock)

	cfg := RoundConfig{
		RoundID:       "round-A",
		Participants:  []string{"zeta", "alpha", "mid"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	view := mustStart(t, engine, cfg)
	if got, want := strings.Join(view.Participants, ","), "alpha,mid,zeta"; got != want {
		t.Fatalf("participants = %s, want %s", got, want)
	}
	view2, outcome, err := engine.StartRound(cfg)
	if err != nil || outcome != Duplicate || view2.RoundID != cfg.RoundID {
		t.Fatalf("duplicate StartRound = (%+v, %v, %v)", view2, outcome, err)
	}
	changed := cfg
	changed.CommitTimeout++
	if _, _, err := engine.StartRound(changed); !errors.Is(err, ErrRoundExists) {
		t.Fatalf("conflicting StartRound error = %v, want ErrRoundExists", err)
	}

	nonces := map[string]Nonce{
		"alpha": testNonce(0x10),
		"mid":   testNonce(0x20),
		"zeta":  testNonce(0x30),
	}
	mustCommit(t, engine, cfg.RoundID, "zeta", CommitmentFor(cfg.RoundID, "zeta", nonces["zeta"]))
	mustCommit(t, engine, cfg.RoundID, "mid", CommitmentFor(cfg.RoundID, "mid", nonces["mid"]))

	if _, err := engine.Reveal(cfg.RoundID, "zeta", nonces["zeta"]); !errors.Is(err, ErrRevealNotOpen) {
		t.Fatalf("early reveal error = %v, want ErrRevealNotOpen", err)
	}
	view, _ = engine.Round(cfg.RoundID)
	if view.Phase != PhaseCommit || view.RevealCount != 0 {
		t.Fatalf("early reveal changed state: %+v", view)
	}

	mustCommit(t, engine, cfg.RoundID, "alpha", CommitmentFor(cfg.RoundID, "alpha", nonces["alpha"]))
	view, _ = engine.Round(cfg.RoundID)
	if view.Phase != PhaseReveal || !view.RevealDeadline.Equal(start.Add(cfg.RevealTimeout)) {
		t.Fatalf("reveal phase state = %+v", view)
	}

	mustReveal(t, engine, cfg.RoundID, "zeta", nonces["zeta"])
	mustReveal(t, engine, cfg.RoundID, "alpha", nonces["alpha"])
	mustReveal(t, engine, cfg.RoundID, "mid", nonces["mid"])

	view, _ = engine.Round(cfg.RoundID)
	if view.Phase != PhaseFinalized || !view.HasResult {
		t.Fatalf("final state = %+v", view)
	}
	want := expectedResult(nonces, []string{"alpha", "mid", "zeta"})
	if view.Result != want {
		t.Fatalf("result = %x, want %x", view.Result, want)
	}

	if outcome, err := engine.Commit(cfg.RoundID, "alpha", CommitmentFor(cfg.RoundID, "alpha", nonces["alpha"])); err != nil || outcome != Duplicate {
		t.Fatalf("duplicate commit = (%v, %v)", outcome, err)
	}
	if outcome, err := engine.Reveal(cfg.RoundID, "alpha", nonces["alpha"]); err != nil || outcome != Duplicate {
		t.Fatalf("duplicate reveal = (%v, %v)", outcome, err)
	}
	if _, err := engine.Commit(cfg.RoundID, "alpha", CommitmentFor(cfg.RoundID, "alpha", testNonce(0x99))); !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("replacement commit error = %v, want ErrCommitConflict", err)
	}
	if _, err := engine.Reveal(cfg.RoundID, "alpha", testNonce(0x99)); !errors.Is(err, ErrRevealConflict) {
		t.Fatalf("replacement reveal error = %v, want ErrRevealConflict", err)
	}
}

func TestDeadlineBoundaries(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)

	t.Run("events immediately before deadlines are accepted", func(t *testing.T) {
		clock := newFakeClock(t0)
		engine := newEngine(t, NewMemoryStore(), clock)
		cfg := RoundConfig{
			RoundID:       "just-in-time",
			Participants:  []string{"alice"},
			CommitTimeout: 10 * time.Second,
			RevealTimeout: 5 * time.Second,
		}
		mustStart(t, engine, cfg)

		commitAt := t0.Add(cfg.CommitTimeout - time.Nanosecond)
		clock.Set(commitAt)
		nonce := testNonce(0x01)
		mustCommit(t, engine, cfg.RoundID, "alice", CommitmentFor(cfg.RoundID, "alice", nonce))

		view, _ := engine.Round(cfg.RoundID)
		wantRevealDeadline := commitAt.Add(cfg.RevealTimeout)
		if !view.RevealDeadline.Equal(wantRevealDeadline) {
			t.Fatalf("reveal deadline = %s, want %s", view.RevealDeadline, wantRevealDeadline)
		}
		if due, err := engine.Advance(wantRevealDeadline.Add(-time.Nanosecond)); err != nil || len(due) != 0 {
			t.Fatalf("Advance before deadline = (%v, %v)", due, err)
		}
		clock.Set(wantRevealDeadline.Add(-time.Nanosecond))
		mustReveal(t, engine, cfg.RoundID, "alice", nonce)
		view, _ = engine.Round(cfg.RoundID)
		if view.Phase != PhaseFinalized {
			t.Fatalf("phase = %s, want finalized", view.Phase)
		}
	})

	t.Run("commitment at the deadline is rejected and aborts", func(t *testing.T) {
		clock := newFakeClock(t0)
		engine := newEngine(t, NewMemoryStore(), clock)
		cfg := RoundConfig{
			RoundID:       "late-commit",
			Participants:  []string{"alice"},
			CommitTimeout: 10 * time.Second,
			RevealTimeout: 5 * time.Second,
		}
		mustStart(t, engine, cfg)
		clock.Set(t0.Add(cfg.CommitTimeout))
		if _, err := engine.Commit(cfg.RoundID, "alice", CommitmentFor(cfg.RoundID, "alice", testNonce(2))); !errors.Is(err, ErrDeadlinePassed) {
			t.Fatalf("commit at deadline error = %v, want ErrDeadlinePassed", err)
		}
		view, _ := engine.Round(cfg.RoundID)
		if view.CommitCount != 0 {
			t.Fatalf("late commit changed state: %+v", view)
		}
		due, err := engine.Advance(t0.Add(cfg.CommitTimeout))
		if err != nil || len(due) != 1 || due[0] != cfg.RoundID {
			t.Fatalf("Advance at deadline = (%v, %v)", due, err)
		}
		view, _ = engine.Round(cfg.RoundID)
		if view.Phase != PhaseAborted || view.AbortReason != AbortCommitTimeout || view.HasResult {
			t.Fatalf("aborted state = %+v", view)
		}
	})

	t.Run("reveal at the deadline is rejected and aborts", func(t *testing.T) {
		clock := newFakeClock(t0)
		engine := newEngine(t, NewMemoryStore(), clock)
		cfg := RoundConfig{
			RoundID:       "late-reveal",
			Participants:  []string{"alice"},
			CommitTimeout: 10 * time.Second,
			RevealTimeout: 5 * time.Second,
		}
		mustStart(t, engine, cfg)
		nonce := testNonce(3)
		mustCommit(t, engine, cfg.RoundID, "alice", CommitmentFor(cfg.RoundID, "alice", nonce))
		view, _ := engine.Round(cfg.RoundID)
		deadline := view.RevealDeadline
		clock.Set(deadline)
		if _, err := engine.Reveal(cfg.RoundID, "alice", nonce); !errors.Is(err, ErrDeadlinePassed) {
			t.Fatalf("reveal at deadline error = %v, want ErrDeadlinePassed", err)
		}
		due, err := engine.Advance(deadline)
		if err != nil || len(due) != 1 {
			t.Fatalf("Advance at reveal deadline = (%v, %v)", due, err)
		}
		view, _ = engine.Round(cfg.RoundID)
		if view.Phase != PhaseAborted || view.AbortReason != AbortRevealTimeout || view.HasResult {
			t.Fatalf("aborted state = %+v", view)
		}
		if _, err := engine.Reveal(cfg.RoundID, "alice", nonce); !errors.Is(err, ErrRoundAborted) {
			t.Fatalf("reveal after abort error = %v, want ErrRoundAborted", err)
		}
	})
}

func TestMissingRevealAbortsWithoutZeroFill(t *testing.T) {
	start := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	engine := newEngine(t, NewMemoryStore(), clock)
	cfg := RoundConfig{
		RoundID:       "missing-reveal",
		Participants:  []string{"alice", "bob", "carol"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg)

	nonces := map[string]Nonce{
		"alice": testNonce(0x50),
		"bob":   testNonce(0x60),
		"carol": testNonce(0x70),
	}
	for _, participant := range cfg.Participants {
		mustCommit(t, engine, cfg.RoundID, participant, CommitmentFor(cfg.RoundID, participant, nonces[participant]))
	}
	mustReveal(t, engine, cfg.RoundID, "alice", nonces["alice"])
	mustReveal(t, engine, cfg.RoundID, "bob", nonces["bob"])

	view, _ := engine.Round(cfg.RoundID)
	if due, err := engine.Advance(view.RevealDeadline.Add(-time.Nanosecond)); err != nil || len(due) != 0 {
		t.Fatalf("Advance before deadline = (%v, %v)", due, err)
	}

	due, err := engine.Advance(view.RevealDeadline)
	if err != nil || len(due) != 1 || due[0] != cfg.RoundID {
		t.Fatalf("Advance at deadline = (%v, %v)", due, err)
	}
	view, _ = engine.Round(cfg.RoundID)
	if view.Phase != PhaseAborted || view.AbortReason != AbortRevealTimeout {
		t.Fatalf("final state = %+v", view)
	}
	if view.HasResult {
		t.Fatal("aborted round unexpectedly has a result")
	}
	if _, err := engine.Reveal(cfg.RoundID, "carol", nonces["carol"]); !errors.Is(err, ErrRoundAborted) {
		t.Fatalf("reveal after abort error = %v, want ErrRoundAborted", err)
	}
}

func TestPreemptiveAndInvalidRevealsDoNotConsumeSlot(t *testing.T) {
	start := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	engine := newEngine(t, NewMemoryStore(), clock)
	cfg := RoundConfig{
		RoundID:       "preemptive",
		Participants:  []string{"alice", "bob"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg)

	aliceNonce := testNonce(0x80)
	bobNonce := testNonce(0x90)
	mustCommit(t, engine, cfg.RoundID, "alice", CommitmentFor(cfg.RoundID, "alice", aliceNonce))
	if _, err := engine.Reveal(cfg.RoundID, "alice", aliceNonce); !errors.Is(err, ErrRevealNotOpen) {
		t.Fatalf("preemptive reveal error = %v, want ErrRevealNotOpen", err)
	}
	view, _ := engine.Round(cfg.RoundID)
	if view.RevealCount != 0 {
		t.Fatalf("preemptive reveal consumed a slot: %+v", view)
	}

	mustCommit(t, engine, cfg.RoundID, "bob", CommitmentFor(cfg.RoundID, "bob", bobNonce))
	if _, err := engine.Reveal(cfg.RoundID, "alice", testNonce(0xEE)); !errors.Is(err, ErrRevealMismatch) {
		t.Fatalf("invalid reveal error = %v, want ErrRevealMismatch", err)
	}
	view, _ = engine.Round(cfg.RoundID)
	if view.RevealCount != 0 {
		t.Fatalf("invalid reveal consumed a slot: %+v", view)
	}
	mustReveal(t, engine, cfg.RoundID, "alice", aliceNonce)
}

func TestCrossRoundReplayCannotFinalize(t *testing.T) {
	start := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	engine := newEngine(t, NewMemoryStore(), clock)
	base := []string{"alice", "bob"}
	roundA := RoundConfig{RoundID: "round-A", Participants: base, CommitTimeout: time.Minute, RevealTimeout: time.Minute}
	roundB := RoundConfig{RoundID: "round-B", Participants: base, CommitTimeout: time.Minute, RevealTimeout: time.Minute}
	mustStart(t, engine, roundA)
	mustStart(t, engine, roundB)

	aliceA := testNonce(0xA0)
	bobA := testNonce(0xA1)
	bobB := testNonce(0xA2)
	commitmentA := CommitmentFor(roundA.RoundID, "alice", aliceA)
	mustCommit(t, engine, roundA.RoundID, "alice", commitmentA)
	mustCommit(t, engine, roundA.RoundID, "bob", CommitmentFor(roundA.RoundID, "bob", bobA))

	// The digest is not self-describing, so it can be submitted in round B.
	// The round/participant binding makes the transported opening unusable.
	mustCommit(t, engine, roundB.RoundID, "alice", commitmentA)
	mustCommit(t, engine, roundB.RoundID, "bob", CommitmentFor(roundB.RoundID, "bob", bobB))
	if _, err := engine.Reveal(roundB.RoundID, "alice", aliceA); !errors.Is(err, ErrRevealMismatch) {
		t.Fatalf("cross-round reveal error = %v, want ErrRevealMismatch", err)
	}
	view, _ := engine.Round(roundB.RoundID)
	if view.RevealCount != 0 || view.HasResult {
		t.Fatalf("cross-round replay changed round B: %+v", view)
	}
}

func TestConcurrentCommitReplacementFirstWins(t *testing.T) {
	start := time.Date(2026, 11, 15, 15, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	engine := newEngine(t, NewMemoryStore(), clock)
	cfg := RoundConfig{
		RoundID:       "concurrent-commit",
		Participants:  []string{"alice"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg)

	const attempts = 64
	commitments := make([]Commitment, attempts)
	for i := range commitments {
		commitments[i] = CommitmentFor(cfg.RoundID, "alice", testNonce(byte(i)))
	}

	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for i := range commitments {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outcome, err := engine.Commit(cfg.RoundID, "alice", commitments[i])
			switch {
			case err == nil && outcome == Accepted:
				results <- nil
			case err != nil && errors.Is(err, ErrCommitConflict):
				results <- nil
			default:
				results <- fmt.Errorf("attempt %d: outcome=%v err=%v", i, outcome, err)
			}
		}(i)
	}
	wg.Wait()
	close(results)

	accepted := 0
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}

	// The state contains exactly one commitment, and it is one of the proposals.
	engine.mu.Lock()
	stored := engine.rounds[cfg.RoundID].commits["alice"]
	engine.mu.Unlock()
	for _, commitment := range commitments {
		if stored == commitment {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("stored commitment matched %d proposals, want exactly 1", accepted)
	}
}

func TestConcurrentFinalization(t *testing.T) {
	start := time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	engine := newEngine(t, NewMemoryStore(), clock)

	participants := make([]string, MaxParticipants)
	nonces := make(map[string]Nonce, MaxParticipants)
	for i := range participants {
		participants[i] = fmt.Sprintf("p%02d", i)
		nonces[participants[i]] = testNonce(byte(i * 3))
	}
	cfg := RoundConfig{
		RoundID:       "concurrent-finalize",
		Participants:  participants,
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg)
	for _, participant := range participants {
		mustCommit(t, engine, cfg.RoundID, participant, CommitmentFor(cfg.RoundID, participant, nonces[participant]))
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2*len(participants))
	for _, participant := range participants {
		for copyIndex := 0; copyIndex < 2; copyIndex++ {
			wg.Add(1)
			go func(participant string) {
				defer wg.Done()
				outcome, err := engine.Reveal(cfg.RoundID, participant, nonces[participant])
				if err != nil {
					errs <- fmt.Errorf("%s: %w", participant, err)
					return
				}
				if outcome != Accepted && outcome != Duplicate {
					errs <- fmt.Errorf("%s: outcome %v", participant, outcome)
				}
			}(participant)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	view, _ := engine.Round(cfg.RoundID)
	if view.Phase != PhaseFinalized || !view.HasResult || view.RevealCount != len(participants) {
		t.Fatalf("final state = %+v", view)
	}
	want := expectedResult(nonces, participants)
	if view.Result != want {
		t.Fatalf("result = %x, want %x", view.Result, want)
	}
	if due, err := engine.Advance(start.Add(time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("Advance after finalization = (%v, %v)", due, err)
	}
}

func TestConcurrentTimeoutAndFinalizationHasSingleOutcome(t *testing.T) {
	start := time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC)

	for iteration := 0; iteration < 50; iteration++ {
		clock := newFakeClock(start)
		engine := newEngine(t, NewMemoryStore(), clock)
		roundID := fmt.Sprintf("race-%02d", iteration)
		cfg := RoundConfig{
			RoundID:       roundID,
			Participants:  []string{"alice", "bob"},
			CommitTimeout: time.Minute,
			RevealTimeout: time.Minute,
		}
		mustStart(t, engine, cfg)
		alice := testNonce(0x11)
		bob := testNonce(0x22)
		mustCommit(t, engine, roundID, "alice", CommitmentFor(roundID, "alice", alice))
		mustCommit(t, engine, roundID, "bob", CommitmentFor(roundID, "bob", bob))
		mustReveal(t, engine, roundID, "alice", alice)

		view, _ := engine.Round(roundID)
		deadline := view.RevealDeadline
		clock.Set(deadline.Add(-time.Nanosecond))

		var wg sync.WaitGroup
		wg.Add(2)
		revealErr := make(chan error, 1)
		go func() {
			defer wg.Done()
			_, err := engine.Reveal(roundID, "bob", bob)
			revealErr <- err
		}()
		go func() {
			defer wg.Done()
			_, _ = engine.Advance(deadline)
		}()
		wg.Wait()
		close(revealErr)
		for err := range revealErr {
			if err != nil && !errors.Is(err, ErrRoundAborted) {
				t.Fatalf("iteration %d: reveal error = %v", iteration, err)
			}
		}

		view, _ = engine.Round(roundID)
		switch view.Phase {
		case PhaseFinalized:
			want := expectedResult(map[string]Nonce{"alice": alice, "bob": bob}, []string{"alice", "bob"})
			if !view.HasResult || view.Result != want {
				t.Fatalf("iteration %d: finalized state = %+v", iteration, view)
			}
		case PhaseAborted:
			if view.HasResult || view.AbortReason != AbortRevealTimeout {
				t.Fatalf("iteration %d: aborted state = %+v", iteration, view)
			}
		default:
			t.Fatalf("iteration %d: non-terminal phase %s", iteration, view.Phase)
		}
	}
}

func TestRecoveryReplayAndDuplicateMessages(t *testing.T) {
	start := time.Date(2026, 11, 15, 17, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	store := NewMemoryStore()
	engine := newEngine(t, store, clock)

	cfg := RoundConfig{
		RoundID:       "recover",
		Participants:  []string{"bob", "alice"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg)
	nonces := map[string]Nonce{
		"alice": testNonce(0xB0),
		"bob":   testNonce(0xC0),
	}
	commitments := map[string]Commitment{
		"alice": CommitmentFor(cfg.RoundID, "alice", nonces["alice"]),
		"bob":   CommitmentFor(cfg.RoundID, "bob", nonces["bob"]),
	}
	mustCommit(t, engine, cfg.RoundID, "alice", commitments["alice"])
	mustCommit(t, engine, cfg.RoundID, "bob", commitments["bob"])
	mustReveal(t, engine, cfg.RoundID, "alice", nonces["alice"])

	recovered := newEngine(t, store, clock)
	view, _ := recovered.Round(cfg.RoundID)
	if view.Phase != PhaseReveal || view.CommitCount != 2 || view.RevealCount != 1 {
		t.Fatalf("recovered state = %+v", view)
	}
	if outcome, err := recovered.Commit(cfg.RoundID, "alice", commitments["alice"]); err != nil || outcome != Duplicate {
		t.Fatalf("recovered duplicate commit = (%v, %v)", outcome, err)
	}
	if outcome, err := recovered.Reveal(cfg.RoundID, "alice", nonces["alice"]); err != nil || outcome != Duplicate {
		t.Fatalf("recovered duplicate reveal = (%v, %v)", outcome, err)
	}
	mustReveal(t, recovered, cfg.RoundID, "bob", nonces["bob"])

	finalized := newEngine(t, store, clock)
	view, _ = finalized.Round(cfg.RoundID)
	want := expectedResult(nonces, []string{"alice", "bob"})
	if view.Phase != PhaseFinalized || view.Result != want {
		t.Fatalf("recovered final state = %+v", view)
	}
	if outcome, err := finalized.Reveal(cfg.RoundID, "bob", nonces["bob"]); err != nil || outcome != Duplicate {
		t.Fatalf("duplicate reveal after recovery = (%v, %v)", outcome, err)
	}

	// A journal containing exact duplicate records is still replayable.
	data, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	doubled := duplicateJournalLines(t, data)
	duplicateStore := NewMemoryStore()
	if err := duplicateStore.Save(doubled); err != nil {
		t.Fatal(err)
	}
	fromDoubled := newEngine(t, duplicateStore, clock)
	view, _ = fromDoubled.Round(cfg.RoundID)
	if view.Phase != PhaseFinalized || view.Result != want {
		t.Fatalf("doubled journal state = %+v", view)
	}
}

func TestReplayRejectsConflictingRecords(t *testing.T) {
	start := time.Date(2026, 11, 15, 17, 30, 0, 0, time.UTC)
	clock := newFakeClock(start)
	store := NewMemoryStore()
	engine := newEngine(t, store, clock)

	cfg := RoundConfig{
		RoundID:       "conflicting-journal",
		Participants:  []string{"alice"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg)
	mustCommit(t, engine, cfg.RoundID, "alice", CommitmentFor(cfg.RoundID, "alice", testNonce(0x31)))

	data, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	conflict := CommitmentFor(cfg.RoundID, "alice", testNonce(0x32))
	record := journalRecord{
		Version:     journalVersion,
		Kind:        recordCommit,
		Round:       cfg.RoundID,
		Participant: "alice",
		Data:        append([]byte(nil), conflict[:]...),
		At:          clock.Now().UnixNano(),
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, line...)
	data = append(data, '\n')
	if err := store.Save(data); err != nil {
		t.Fatal(err)
	}

	if _, err := New(store, WithClock(clock)); !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("New with conflicting journal error = %v, want ErrCommitConflict", err)
	}
}

func duplicateJournalLines(t *testing.T, data []byte) []byte {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var out strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return []byte(out.String())
}

type faultStore struct {
	mu      sync.Mutex
	inner   *MemoryStore
	fail    bool
	lastErr error
}

func newFaultStore() *faultStore {
	return &faultStore{inner: NewMemoryStore()}
}

func (s *faultStore) Load() ([]byte, error) {
	return s.inner.Load()
}

func (s *faultStore) Save(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		s.fail = false
		s.lastErr = errors.New("injected write failure")
		return s.lastErr
	}
	return s.inner.Save(data)
}

func (s *faultStore) FailNext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = true
}

func TestPersistenceFailureLeavesStateUnchanged(t *testing.T) {
	start := time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	store := newFaultStore()
	engine := newEngine(t, store, clock)

	cfg := RoundConfig{
		RoundID:       "write-failure",
		Participants:  []string{"alice"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}

	store.FailNext()
	if _, _, err := engine.StartRound(cfg); !errors.Is(err, ErrPersistence) {
		t.Fatalf("failed StartRound error = %v, want ErrPersistence", err)
	}
	if _, err := engine.Round(cfg.RoundID); !errors.Is(err, ErrRoundNotFound) {
		t.Fatalf("round exists after failed start: %v", err)
	}
	mustStart(t, engine, cfg)

	nonce := testNonce(0xD0)
	commitment := CommitmentFor(cfg.RoundID, "alice", nonce)
	store.FailNext()
	if _, err := engine.Commit(cfg.RoundID, "alice", commitment); !errors.Is(err, ErrPersistence) {
		t.Fatalf("failed Commit error = %v, want ErrPersistence", err)
	}
	view, _ := engine.Round(cfg.RoundID)
	if view.CommitCount != 0 || view.Phase != PhaseCommit {
		t.Fatalf("failed commit changed state: %+v", view)
	}
	mustCommit(t, engine, cfg.RoundID, "alice", commitment)

	store.FailNext()
	if _, err := engine.Reveal(cfg.RoundID, "alice", nonce); !errors.Is(err, ErrPersistence) {
		t.Fatalf("failed Reveal error = %v, want ErrPersistence", err)
	}
	view, _ = engine.Round(cfg.RoundID)
	if view.RevealCount != 0 || view.Phase != PhaseReveal {
		t.Fatalf("failed reveal changed state: %+v", view)
	}
	mustReveal(t, engine, cfg.RoundID, "alice", nonce)

	finalView, _ := engine.Round(cfg.RoundID)
	if finalView.Phase != PhaseFinalized || !finalView.HasResult {
		t.Fatalf("final state = %+v", finalView)
	}

	// A failed timeout write must leave the round active; retrying aborts it.
	cfg2 := RoundConfig{
		RoundID:       "timeout-write-failure",
		Participants:  []string{"bob"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg2)
	mustCommit(t, engine, cfg2.RoundID, "bob", CommitmentFor(cfg2.RoundID, "bob", testNonce(0xE0)))
	view, _ = engine.Round(cfg2.RoundID)
	deadline := view.RevealDeadline

	store.FailNext()
	if _, err := engine.Advance(deadline); !errors.Is(err, ErrPersistence) {
		t.Fatalf("failed Advance error = %v, want ErrPersistence", err)
	}
	view, _ = engine.Round(cfg2.RoundID)
	if view.Phase != PhaseReveal || view.AbortReason != "" {
		t.Fatalf("failed Advance changed state: %+v", view)
	}
	due, err := engine.Advance(deadline)
	if err != nil || len(due) != 1 || due[0] != cfg2.RoundID {
		t.Fatalf("retried Advance = (%v, %v)", due, err)
	}
	view, _ = engine.Round(cfg2.RoundID)
	if view.Phase != PhaseAborted || view.AbortReason != AbortRevealTimeout {
		t.Fatalf("state after retried Advance: %+v", view)
	}

	// Reopening the store must not reveal any failed event.
	recovered := newEngine(t, store.inner, clock)
	view, _ = recovered.Round(cfg.RoundID)
	if view.Phase != PhaseFinalized || !view.HasResult {
		t.Fatalf("recovered final state = %+v", view)
	}
	view, _ = recovered.Round(cfg2.RoundID)
	if view.Phase != PhaseAborted || view.AbortReason != AbortRevealTimeout {
		t.Fatalf("recovered aborted state = %+v", view)
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	start := time.Date(2026, 11, 15, 19, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	path := filepath.Join(t.TempDir(), "journal", "rounds.jsonl")
	store := NewFileStore(path)
	engine := newEngine(t, store, clock)

	cfg := RoundConfig{
		RoundID:       "file-backed",
		Participants:  []string{"alice", "bob"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}
	mustStart(t, engine, cfg)
	nonces := map[string]Nonce{
		"alice": testNonce(0xF0),
		"bob":   testNonce(0xF1),
	}
	for _, participant := range cfg.Participants {
		mustCommit(t, engine, cfg.RoundID, participant, CommitmentFor(cfg.RoundID, participant, nonces[participant]))
	}
	mustReveal(t, engine, cfg.RoundID, "alice", nonces["alice"])
	mustReveal(t, engine, cfg.RoundID, "bob", nonces["bob"])

	reopened := newEngine(t, NewFileStore(path), clock)
	view, _ := reopened.Round(cfg.RoundID)
	want := expectedResult(nonces, []string{"alice", "bob"})
	if view.Phase != PhaseFinalized || view.Result != want {
		t.Fatalf("reopened state = %+v", view)
	}
}

func TestConfigValidation(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 11, 15, 20, 0, 0, 0, time.UTC))
	engine := newEngine(t, NewMemoryStore(), clock)

	tooMany := make([]string, MaxParticipants+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("p%d", i)
	}
	cases := []RoundConfig{
		{RoundID: "", Participants: []string{"a"}, CommitTimeout: time.Second, RevealTimeout: time.Second},
		{RoundID: "r", Participants: nil, CommitTimeout: time.Second, RevealTimeout: time.Second},
		{RoundID: "r", Participants: []string{"a"}, CommitTimeout: 0, RevealTimeout: time.Second},
		{RoundID: "r", Participants: []string{"a"}, CommitTimeout: time.Second, RevealTimeout: 0},
		{RoundID: "r", Participants: []string{"a", "a"}, CommitTimeout: time.Second, RevealTimeout: time.Second},
		{RoundID: "r", Participants: []string{""}, CommitTimeout: time.Second, RevealTimeout: time.Second},
		{RoundID: "r", Participants: tooMany, CommitTimeout: time.Second, RevealTimeout: time.Second},
	}
	for i, cfg := range cases {
		if _, _, err := engine.StartRound(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("case %d error = %v, want ErrInvalidConfig", i, err)
		}
	}
}
