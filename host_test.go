package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testBaseTime() time.Time {
	return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
}

func newMemoryHost(t *testing.T) (*Host, time.Time) {
	t.Helper()
	host, err := NewHost(NewMemoryJournal())
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return host, testBaseTime()
}

func startRound(t *testing.T, host *Host, id string, participants []string, deadline, now time.Time) {
	t.Helper()
	if err := host.StartRound(RoundConfig{
		ID:           id,
		Participants: participants,
		Deadline:     deadline,
	}, now); err != nil {
		t.Fatalf("StartRound(%q): %v", id, err)
	}
}

func mustState(t *testing.T, host *Host, id string) RoundState {
	t.Helper()
	state, err := host.State(id)
	if err != nil {
		t.Fatalf("State(%q): %v", id, err)
	}
	return state
}

func manualResult(participants []string, nonces map[string][]byte) [sha256.Size]byte {
	sorted := append([]string(nil), participants...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	h := sha256.New()
	for _, participant := range sorted {
		h.Write(nonces[participant])
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

func TestCommitRevealUsesSortedParticipantOrder(t *testing.T) {
	host, base := newMemoryHost(t)
	const id = "round-sorted"
	deadline := base.Add(time.Minute)
	participants := []string{"zoe", "alice", "bob"}
	startRound(t, host, id, participants, deadline, base)

	nonces := map[string][]byte{
		"alice": testNonce(0xa1),
		"bob":   testNonce(0xb2),
		"zoe":   testNonce(0xc3),
	}

	// A valid reveal is still early until every participant commits.
	if err := host.Reveal(id, "alice", nonces["alice"], base.Add(time.Second)); !errors.Is(err, ErrEarlyReveal) {
		t.Fatalf("early reveal error = %v, want ErrEarlyReveal", err)
	}
	if state := mustState(t, host, id); state.Phase != PhaseCommit || state.Reveals != 0 {
		t.Fatalf("early reveal changed state: %+v", state)
	}

	commitOrder := []string{"bob", "zoe", "alice"}
	for i, participant := range commitOrder {
		commitment := mustCommitment(t, id, participant, nonces[participant])
		if err := host.Commit(id, participant, commitment[:], base.Add(time.Duration(i+2)*time.Second)); err != nil {
			t.Fatalf("Commit(%q): %v", participant, err)
		}
	}
	if state := mustState(t, host, id); state.Phase != PhaseReveal || state.Commitments != 3 {
		t.Fatalf("state after all commitments = %+v", state)
	}

	revealOrder := []string{"zoe", "alice", "bob"}
	for i, participant := range revealOrder {
		if err := host.Reveal(id, participant, nonces[participant], base.Add(time.Duration(i+5)*time.Second)); err != nil {
			t.Fatalf("Reveal(%q): %v", participant, err)
		}
	}

	want := manualResult(participants, nonces)
	got, err := host.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if got != want {
		t.Fatalf("result mismatch:\n got %x\nwant %x", got, want)
	}
	state := mustState(t, host, id)
	if state.Phase != PhaseFinalized || !state.HasResult || state.Commitments != 3 || state.Reveals != 3 {
		t.Fatalf("final state = %+v", state)
	}

	// Exact retransmissions after finalization are accepted idempotently.
	for _, participant := range participants {
		commitment := mustCommitment(t, id, participant, nonces[participant])
		if err := host.Commit(id, participant, commitment[:], deadline); err != nil {
			t.Fatalf("duplicate commit %q: %v", participant, err)
		}
		if err := host.Reveal(id, participant, nonces[participant], deadline); err != nil {
			t.Fatalf("duplicate reveal %q: %v", participant, err)
		}
	}
	if err := host.Timeout(id, deadline.Add(time.Hour)); err != nil {
		t.Fatalf("Timeout after finalization: %v", err)
	}
	again, err := host.Result(id)
	if err != nil || again != want {
		t.Fatalf("result after duplicates = %x, %v", again, err)
	}
}

func TestSingleCommitAndRevealFirstWins(t *testing.T) {
	host, base := newMemoryHost(t)
	const id = "round-first-wins"
	deadline := base.Add(time.Minute)
	startRound(t, host, id, []string{"alice", "bob"}, deadline, base)

	nonce1 := testNonce(1)
	nonce2 := testNonce(2)
	commitment1 := mustCommitment(t, id, "alice", nonce1)
	commitment2 := mustCommitment(t, id, "alice", nonce2)

	if err := host.Commit(id, "alice", commitment1[:], base.Add(time.Second)); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := host.Commit(id, "alice", commitment1[:], base.Add(2*time.Second)); err != nil {
		t.Fatalf("same commit retransmission: %v", err)
	}
	if err := host.Commit(id, "alice", commitment2[:], base.Add(3*time.Second)); !errors.Is(err, ErrCommitmentConflict) {
		t.Fatalf("replacement commit error = %v, want ErrCommitmentConflict", err)
	}
	if state := mustState(t, host, id); state.Commitments != 1 {
		t.Fatalf("commit replacement changed state: %+v", state)
	}

	bobNonce := testNonce(3)
	bobCommitment := mustCommitment(t, id, "bob", bobNonce)
	if err := host.Commit(id, "bob", bobCommitment[:], base.Add(4*time.Second)); err != nil {
		t.Fatalf("bob commit: %v", err)
	}

	if err := host.Reveal(id, "alice", nonce1, base.Add(5*time.Second)); err != nil {
		t.Fatalf("alice reveal: %v", err)
	}
	if err := host.Reveal(id, "alice", nonce1, base.Add(6*time.Second)); err != nil {
		t.Fatalf("same reveal retransmission: %v", err)
	}
	if err := host.Reveal(id, "alice", nonce2, base.Add(7*time.Second)); !errors.Is(err, ErrRevealConflict) {
		t.Fatalf("replacement reveal error = %v, want ErrRevealConflict", err)
	}
	if state := mustState(t, host, id); state.Reveals != 1 {
		t.Fatalf("reveal replacement changed state: %+v", state)
	}

	if err := host.Reveal(id, "bob", bobNonce, base.Add(8*time.Second)); err != nil {
		t.Fatalf("bob reveal: %v", err)
	}
	want := manualResult([]string{"alice", "bob"}, map[string][]byte{"alice": nonce1, "bob": bobNonce})
	got, err := host.Result(id)
	if err != nil || got != want {
		t.Fatalf("result = %x, %v; want %x", got, err, want)
	}
}

func TestEarlyRevealRejected(t *testing.T) {
	host, base := newMemoryHost(t)
	const id = "round-early-reveal"
	deadline := base.Add(time.Minute)
	startRound(t, host, id, []string{"alice", "bob"}, deadline, base)

	aliceNonce := testNonce(0x77)
	bobNonce := testNonce(0x88)
	aliceCommitment := mustCommitment(t, id, "alice", aliceNonce)

	if err := host.Reveal(id, "alice", aliceNonce, base.Add(time.Second)); !errors.Is(err, ErrEarlyReveal) {
		t.Fatalf("reveal before any commit = %v, want ErrEarlyReveal", err)
	}
	if err := host.Commit(id, "alice", aliceCommitment[:], base.Add(2*time.Second)); err != nil {
		t.Fatalf("alice Commit: %v", err)
	}
	if err := host.Reveal(id, "alice", aliceNonce, base.Add(3*time.Second)); !errors.Is(err, ErrEarlyReveal) {
		t.Fatalf("reveal with bob outstanding = %v, want ErrEarlyReveal", err)
	}
	if state := mustState(t, host, id); state.Phase != PhaseCommit || state.Reveals != 0 {
		t.Fatalf("early reveals changed state: %+v", state)
	}

	bobCommitment := mustCommitment(t, id, "bob", bobNonce)
	if err := host.Commit(id, "bob", bobCommitment[:], base.Add(4*time.Second)); err != nil {
		t.Fatalf("bob Commit: %v", err)
	}
	if err := host.Reveal(id, "alice", aliceNonce, base.Add(5*time.Second)); err != nil {
		t.Fatalf("retry after reveal phase: %v", err)
	}
	if err := host.Reveal(id, "bob", bobNonce, base.Add(6*time.Second)); err != nil {
		t.Fatalf("bob Reveal: %v", err)
	}
	if _, err := host.Result(id); err != nil {
		t.Fatalf("Result: %v", err)
	}
}

func TestDeadlineBoundary(t *testing.T) {
	base := testBaseTime()
	deadline := base.Add(10 * time.Second)
	nonce := testNonce(7)
	commitment := mustCommitment(t, "deadline", "alice", nonce)

	tests := []struct {
		name      string
		revealAt  time.Time
		wantErr   error
		wantPhase Phase
	}{
		{name: "one nanosecond before", revealAt: deadline.Add(-time.Nanosecond), wantPhase: PhaseFinalized},
		{name: "exactly at deadline", revealAt: deadline, wantErr: ErrDeadlineExceeded, wantPhase: PhaseAborted},
		{name: "after deadline", revealAt: deadline.Add(time.Nanosecond), wantErr: ErrDeadlineExceeded, wantPhase: PhaseAborted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, err := NewHost(NewMemoryJournal())
			if err != nil {
				t.Fatalf("NewHost: %v", err)
			}
			startRound(t, host, "deadline", []string{"alice"}, deadline, base)
			if err := host.Commit("deadline", "alice", commitment[:], base.Add(time.Second)); err != nil {
				t.Fatalf("Commit: %v", err)
			}

			err = host.Reveal("deadline", "alice", nonce, tt.revealAt)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Reveal: %v", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Reveal error = %v, want %v", err, tt.wantErr)
			}

			state := mustState(t, host, "deadline")
			if state.Phase != tt.wantPhase {
				t.Fatalf("phase = %s, want %s", state.Phase, tt.wantPhase)
			}
			if tt.wantPhase == PhaseAborted {
				if state.AbortReason != AbortTimeout {
					t.Fatalf("abort reason = %q, want %q", state.AbortReason, AbortTimeout)
				}
				if _, err := host.Result("deadline"); !errors.Is(err, ErrRoundAborted) {
					t.Fatalf("Result on aborted round = %v, want ErrRoundAborted", err)
				}
				if state.HasResult || state.Reveals != 0 {
					t.Fatalf("aborted round was zero-filled: %+v", state)
				}
				// Repeated timeouts are idempotent.
				if err := host.Timeout("deadline", deadline.Add(time.Minute)); err != nil {
					t.Fatalf("repeat Timeout: %v", err)
				}
			}
		})
	}
}

func TestTimeoutBeforeDeadlineDoesNothing(t *testing.T) {
	host, base := newMemoryHost(t)
	const id = "timeout-before"
	deadline := base.Add(10 * time.Second)
	startRound(t, host, id, []string{"alice"}, deadline, base)

	if err := host.Timeout(id, deadline.Add(-time.Nanosecond)); !errors.Is(err, ErrNotExpired) {
		t.Fatalf("early Timeout error = %v, want ErrNotExpired", err)
	}
	if state := mustState(t, host, id); state.Phase != PhaseCommit || state.AbortReason != AbortNone {
		t.Fatalf("early timeout changed state: %+v", state)
	}
	if err := host.Timeout(id, deadline); err != nil {
		t.Fatalf("Timeout at deadline: %v", err)
	}
	state := mustState(t, host, id)
	if state.Phase != PhaseAborted || state.AbortReason != AbortTimeout {
		t.Fatalf("state after timeout: %+v", state)
	}
}

func TestRecoveryAndDuplicateMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "round.wal")
	base := testBaseTime()
	deadline := base.Add(time.Minute)
	const id = "round-recovery"
	participants := []string{"p1", "p2"}
	nonces := map[string][]byte{"p1": testNonce(0x11), "p2": testNonce(0x22)}
	commitments := map[string][CommitmentSize]byte{
		"p1": mustCommitment(t, id, "p1", nonces["p1"]),
		"p2": mustCommitment(t, id, "p2", nonces["p2"]),
	}

	journal, err := OpenFileJournal(path)
	if err != nil {
		t.Fatalf("OpenFileJournal: %v", err)
	}
	host, err := NewHost(journal)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	startRound(t, host, id, participants, deadline, base)
	for _, participant := range participants {
		commitment := commitments[participant]
		if err := host.Commit(id, participant, commitment[:], base.Add(time.Second)); err != nil {
			t.Fatalf("Commit(%q): %v", participant, err)
		}
	}
	if err := host.Reveal(id, "p1", nonces["p1"], base.Add(2*time.Second)); err != nil {
		t.Fatalf("Reveal(p1): %v", err)
	}
	if got, want := countJournalRecords(t, path), 4; got != want {
		t.Fatalf("journal records = %d, want %d", got, want)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Restart and retransmit messages already acknowledged before the crash.
	journal, err = OpenFileJournal(path)
	if err != nil {
		t.Fatalf("reopen journal: %v", err)
	}
	host, err = NewHost(journal)
	if err != nil {
		t.Fatalf("replay host: %v", err)
	}
	state := mustState(t, host, id)
	if state.Phase != PhaseReveal || state.Commitments != 2 || state.Reveals != 1 {
		t.Fatalf("replayed state = %+v", state)
	}
	if err := host.StartRound(RoundConfig{ID: id, Participants: []string{"p2", "p1"}, Deadline: deadline}, base); err != nil {
		t.Fatalf("idempotent StartRound: %v", err)
	}
	for _, participant := range participants {
		commitment := commitments[participant]
		if err := host.Commit(id, participant, commitment[:], base.Add(3*time.Second)); err != nil {
			t.Fatalf("duplicate commit after recovery (%q): %v", participant, err)
		}
	}
	if err := host.Reveal(id, "p1", nonces["p1"], base.Add(4*time.Second)); err != nil {
		t.Fatalf("duplicate reveal after recovery: %v", err)
	}
	if got := countJournalRecords(t, path); got != 4 {
		t.Fatalf("duplicates appended events: records = %d, want 4", got)
	}

	if err := host.Reveal(id, "p2", nonces["p2"], base.Add(5*time.Second)); err != nil {
		t.Fatalf("Reveal(p2): %v", err)
	}
	want := manualResult(participants, nonces)
	got, err := host.Result(id)
	if err != nil || got != want {
		t.Fatalf("result = %x, %v; want %x", got, err, want)
	}
	if got := countJournalRecords(t, path); got != 5 {
		t.Fatalf("journal records = %d, want 5", got)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	journal, err = OpenFileJournal(path)
	if err != nil {
		t.Fatalf("second reopen: %v", err)
	}
	defer journal.Close()
	host, err = NewHost(journal)
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	for _, participant := range participants {
		commitment := commitments[participant]
		if err := host.Commit(id, participant, commitment[:], base.Add(time.Minute)); err != nil {
			t.Fatalf("duplicate commit after final replay (%q): %v", participant, err)
		}
		if err := host.Reveal(id, participant, nonces[participant], base.Add(time.Minute)); err != nil {
			t.Fatalf("duplicate reveal after final replay (%q): %v", participant, err)
		}
	}
	got, err = host.Result(id)
	if err != nil || got != want {
		t.Fatalf("result after replay = %x, %v; want %x", got, err, want)
	}
	if got := countJournalRecords(t, path); got != 5 {
		t.Fatalf("final journal records = %d, want 5", got)
	}
}

func TestCrossRoundReplayRejected(t *testing.T) {
	host, base := newMemoryHost(t)
	deadline := base.Add(time.Minute)
	startRound(t, host, "r1", []string{"alice"}, deadline, base)
	startRound(t, host, "r2", []string{"alice"}, deadline, base)

	nonce := testNonce(0x33)
	commitment := mustCommitment(t, "r1", "alice", nonce)
	if err := host.Commit("r1", "alice", commitment[:], base.Add(time.Second)); err != nil {
		t.Fatalf("r1 Commit: %v", err)
	}
	if err := host.Reveal("r1", "alice", nonce, base.Add(2*time.Second)); err != nil {
		t.Fatalf("r1 Reveal: %v", err)
	}

	// The old commitment can be submitted as an opaque value, but the old nonce
	// does not open it once the round ID changes.
	if err := host.Commit("r2", "alice", commitment[:], base.Add(3*time.Second)); err != nil {
		t.Fatalf("r2 Commit: %v", err)
	}
	if err := host.Reveal("r2", "alice", nonce, base.Add(4*time.Second)); !errors.Is(err, ErrRevealMismatch) {
		t.Fatalf("cross-round replay error = %v, want ErrRevealMismatch", err)
	}
	if state := mustState(t, host, "r2"); state.Phase != PhaseReveal || state.Reveals != 0 || state.HasResult {
		t.Fatalf("cross-round replay changed r2: %+v", state)
	}

	// A used round ID cannot be rebound to a new participant set.
	if err := host.StartRound(RoundConfig{ID: "r1", Participants: []string{"bob"}, Deadline: deadline}, base); !errors.Is(err, ErrRoundExists) {
		t.Fatalf("round ID reuse error = %v, want ErrRoundExists", err)
	}
}

func TestConcurrentCommitReplacementHasOneWinner(t *testing.T) {
	host, base := newMemoryHost(t)
	const id = "round-concurrent-replacement"
	deadline := base.Add(time.Minute)
	startRound(t, host, id, []string{"alice"}, deadline, base)

	const attempts = 64
	nonces := make([][]byte, attempts)
	commitments := make([][CommitmentSize]byte, attempts)
	for i := 0; i < attempts; i++ {
		nonces[i] = testNonce(byte(i + 1))
		commitments[i] = mustCommitment(t, id, "alice", nonces[i])
	}

	errs := make([]error, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = host.Commit(id, "alice", commitments[i][:], base.Add(time.Second))
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil:
			if winner != -1 {
				t.Fatalf("multiple commits won: %d and %d", winner, i)
			}
			winner = i
		case errors.Is(err, ErrCommitmentConflict):
		default:
			t.Fatalf("commit %d error = %v, want ErrCommitmentConflict", i, err)
		}
	}
	if winner == -1 {
		t.Fatal("no commitment won")
	}
	state := mustState(t, host, id)
	if state.Commitments != 1 || state.Phase != PhaseReveal {
		t.Fatalf("state after concurrent replacement = %+v", state)
	}

	if err := host.Reveal(id, "alice", nonces[winner], base.Add(2*time.Second)); err != nil {
		t.Fatalf("winner reveal: %v", err)
	}
	want := manualResult([]string{"alice"}, map[string][]byte{"alice": nonces[winner]})
	got, err := host.Result(id)
	if err != nil || got != want {
		t.Fatalf("result = %x, %v; want %x", got, err, want)
	}
}

func TestConcurrentRevealFinalizesOnce(t *testing.T) {
	host, base := newMemoryHost(t)
	const id = "round-concurrent-finalize"
	deadline := base.Add(time.Minute)
	participants := make([]string, MaxParticipants)
	nonces := make(map[string][]byte, MaxParticipants)
	commitments := make(map[string][CommitmentSize]byte, MaxParticipants)
	for i := 0; i < MaxParticipants; i++ {
		participant := fmt.Sprintf("p%02d", i)
		participants[i] = participant
		nonces[participant] = testNonce(byte(i + 1))
		commitments[participant] = mustCommitment(t, id, participant, nonces[participant])
	}
	startRound(t, host, id, participants, deadline, base)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, len(participants))
	for i, participant := range participants {
		wg.Add(1)
		go func(i int, participant string) {
			defer wg.Done()
			<-start
			commitment := commitments[participant]
			errs[i] = host.Commit(id, participant, commitment[:], base.Add(time.Second))
		}(i, participant)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent commit %d: %v", i, err)
		}
	}
	if state := mustState(t, host, id); state.Phase != PhaseReveal || state.Commitments != MaxParticipants {
		t.Fatalf("state after commits = %+v", state)
	}

	start = make(chan struct{})
	for i := range errs {
		errs[i] = nil
	}
	for i, participant := range participants {
		wg.Add(1)
		go func(i int, participant string) {
			defer wg.Done()
			<-start
			errs[i] = host.Reveal(id, participant, nonces[participant], base.Add(2*time.Second))
		}(i, participant)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent reveal %d: %v", i, err)
		}
	}

	want := manualResult(participants, nonces)
	got, err := host.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if got != want {
		t.Fatalf("result mismatch:\n got %x\nwant %x", got, want)
	}
	state := mustState(t, host, id)
	if state.Phase != PhaseFinalized || state.Reveals != MaxParticipants || !state.HasResult {
		t.Fatalf("final state = %+v", state)
	}

	// Concurrent duplicate finalization/read paths must not alter the result.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			participant := participants[i%len(participants)]
			if err := host.Reveal(id, participant, nonces[participant], base.Add(3*time.Second)); err != nil {
				t.Errorf("duplicate reveal: %v", err)
			}
			value, err := host.Result(id)
			if err != nil {
				t.Errorf("Result: %v", err)
				return
			}
			if value != want {
				t.Errorf("result changed: %x", value)
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrentTimeoutAndFinalRevealAreLinearizable(t *testing.T) {
	base := testBaseTime()
	deadline := base.Add(10 * time.Second)
	nonce := testNonce(0x44)
	commitment := mustCommitment(t, "race", "alice", nonce)

	for i := 0; i < 50; i++ {
		host, err := NewHost(NewMemoryJournal())
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		startRound(t, host, "race", []string{"alice"}, deadline, base)
		if err := host.Commit("race", "alice", commitment[:], base.Add(time.Second)); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		var wg sync.WaitGroup
		var revealErr, timeoutErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			revealErr = host.Reveal("race", "alice", nonce, deadline.Add(-time.Nanosecond))
		}()
		go func() {
			defer wg.Done()
			<-start
			timeoutErr = host.Timeout("race", deadline)
		}()
		close(start)
		wg.Wait()

		state := mustState(t, host, "race")
		switch state.Phase {
		case PhaseFinalized:
			if revealErr != nil {
				t.Fatalf("iteration %d: finalized but reveal failed: %v", i, revealErr)
			}
			if timeoutErr != nil {
				t.Fatalf("iteration %d: finalized but timeout failed: %v", i, timeoutErr)
			}
			if _, err := host.Result("race"); err != nil {
				t.Fatalf("iteration %d: finalized result error: %v", i, err)
			}
		case PhaseAborted:
			if !errors.Is(revealErr, ErrRoundAborted) && !errors.Is(revealErr, ErrDeadlineExceeded) {
				t.Fatalf("iteration %d: aborted but reveal error = %v", i, revealErr)
			}
			if timeoutErr != nil {
				t.Fatalf("iteration %d: timeout error = %v", i, timeoutErr)
			}
			if _, err := host.Result("race"); !errors.Is(err, ErrRoundAborted) {
				t.Fatalf("iteration %d: aborted result error = %v", i, err)
			}
		default:
			t.Fatalf("iteration %d: non-terminal state %+v", i, state)
		}
	}
}

func TestParticipantSetFrozenAndBounded(t *testing.T) {
	host, base := newMemoryHost(t)
	deadline := base.Add(time.Minute)

	tooMany := make([]string, MaxParticipants+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("p%02d", i)
	}
	if err := host.StartRound(RoundConfig{ID: "too-many", Participants: tooMany, Deadline: deadline}, base); !errors.Is(err, ErrTooManyParticipants) {
		t.Fatalf("33 participants error = %v, want ErrTooManyParticipants", err)
	}
	if err := host.StartRound(RoundConfig{ID: "empty", Participants: nil, Deadline: deadline}, base); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty participants error = %v, want ErrInvalidConfig", err)
	}
	if err := host.StartRound(RoundConfig{ID: "dup", Participants: []string{"a", "a"}, Deadline: deadline}, base); !errors.Is(err, ErrDuplicateParticipant) {
		t.Fatalf("duplicate participants error = %v, want ErrDuplicateParticipant", err)
	}
	if err := host.StartRound(RoundConfig{ID: "empty-id", Participants: []string{""}, Deadline: deadline}, base); !errors.Is(err, ErrInvalidParticipant) {
		t.Fatalf("empty participant error = %v, want ErrInvalidParticipant", err)
	}

	startRound(t, host, "full", tooMany[:MaxParticipants], deadline, base)
	state := mustState(t, host, "full")
	if len(state.Participants) != MaxParticipants {
		t.Fatalf("participant count = %d", len(state.Participants))
	}
	commitment := mustCommitment(t, "full", "intruder", testNonce(1))
	if err := host.Commit("full", "intruder", commitment[:], base.Add(time.Second)); !errors.Is(err, ErrInvalidParticipant) {
		t.Fatalf("unknown participant error = %v, want ErrInvalidParticipant", err)
	}
	if state := mustState(t, host, "full"); state.Commitments != 0 {
		t.Fatalf("unknown participant changed state: %+v", state)
	}
}

func TestHostInputValidation(t *testing.T) {
	host, base := newMemoryHost(t)
	deadline := base.Add(time.Minute)
	if err := host.StartRound(RoundConfig{ID: "expired", Participants: []string{"alice"}, Deadline: base}, base); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("expired StartRound error = %v, want ErrDeadlineExceeded", err)
	}
	if _, err := host.State("expired"); !errors.Is(err, ErrRoundNotFound) {
		t.Fatalf("expired StartRound created state: %v", err)
	}

	const id = "input-validation"
	startRound(t, host, id, []string{"alice"}, deadline, base)
	nonce := testNonce(0x99)
	commitment := mustCommitment(t, id, "alice", nonce)
	if err := host.Commit(id, "alice", commitment[:CommitmentSize-1], base.Add(time.Second)); !errors.Is(err, ErrInvalidCommitment) {
		t.Fatalf("short commitment error = %v, want ErrInvalidCommitment", err)
	}
	if err := host.Commit(id, "alice", commitment[:], base.Add(2*time.Second)); err != nil {
		t.Fatalf("valid Commit: %v", err)
	}
	if err := host.Reveal(id, "alice", nonce[:NonceSize-1], base.Add(3*time.Second)); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("short nonce error = %v, want ErrInvalidNonce", err)
	}
	if err := host.Reveal(id, "alice", testNonce(0x01), base.Add(4*time.Second)); !errors.Is(err, ErrRevealMismatch) {
		t.Fatalf("wrong nonce error = %v, want ErrRevealMismatch", err)
	}
	if state := mustState(t, host, id); state.Reveals != 0 {
		t.Fatalf("invalid reveals changed state: %+v", state)
	}
	if err := host.Reveal(id, "alice", nonce[:], base.Add(5*time.Second)); err != nil {
		t.Fatalf("valid Reveal: %v", err)
	}
	if _, err := host.Result(id); err != nil {
		t.Fatalf("Result: %v", err)
	}
}

func TestPersistenceFailureDoesNotChangeState(t *testing.T) {
	journal := &failJournal{}
	host, err := NewHost(journal)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	base := testBaseTime()
	deadline := base.Add(time.Minute)
	const id = "round-failure"
	nonce := testNonce(0x55)
	commitment := mustCommitment(t, id, "alice", nonce)
	cfg := RoundConfig{ID: id, Participants: []string{"alice"}, Deadline: deadline}

	injected := errors.New("injected append failure")
	journal.failNext = injected
	if err := host.StartRound(cfg, base); !errors.Is(err, injected) {
		t.Fatalf("failed StartRound error = %v", err)
	}
	if _, err := host.State(id); !errors.Is(err, ErrRoundNotFound) {
		t.Fatalf("failed StartRound created state: %v", err)
	}

	if err := host.StartRound(cfg, base); err != nil {
		t.Fatalf("StartRound: %v", err)
	}
	injected = errors.New("injected append failure")
	journal.failNext = injected
	if err := host.Commit(id, "alice", commitment[:], base.Add(time.Second)); !errors.Is(err, injected) {
		t.Fatalf("failed Commit error = %v", err)
	}
	if state := mustState(t, host, id); state.Commitments != 0 || state.Phase != PhaseCommit {
		t.Fatalf("failed Commit changed state: %+v", state)
	}

	if err := host.Commit(id, "alice", commitment[:], base.Add(2*time.Second)); err != nil {
		t.Fatalf("Commit after failure: %v", err)
	}
	injected = errors.New("injected append failure")
	journal.failNext = injected
	if err := host.Reveal(id, "alice", nonce, base.Add(3*time.Second)); !errors.Is(err, injected) {
		t.Fatalf("failed Reveal error = %v", err)
	}
	if state := mustState(t, host, id); state.Reveals != 0 || state.Phase != PhaseReveal {
		t.Fatalf("failed Reveal changed state: %+v", state)
	}

	if err := host.Reveal(id, "alice", nonce, base.Add(4*time.Second)); err != nil {
		t.Fatalf("Reveal after failure: %v", err)
	}
	got, err := host.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}

	replayed, err := NewHost(journal)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	replayedResult, err := replayed.Result(id)
	if err != nil || replayedResult != got {
		t.Fatalf("replayed result = %x, %v; want %x", replayedResult, err, got)
	}

	// A failed timeout append must not abort a live round.
	const timeoutID = "round-timeout-failure"
	if err := host.StartRound(RoundConfig{ID: timeoutID, Participants: []string{"bob"}, Deadline: deadline}, base); err != nil {
		t.Fatalf("StartRound(timeout): %v", err)
	}
	injected = errors.New("injected timeout failure")
	journal.failNext = injected
	if err := host.Timeout(timeoutID, deadline); !errors.Is(err, injected) {
		t.Fatalf("failed Timeout error = %v", err)
	}
	if state := mustState(t, host, timeoutID); state.Phase != PhaseCommit || state.AbortReason != AbortNone {
		t.Fatalf("failed Timeout changed state: %+v", state)
	}
	if err := host.Timeout(timeoutID, deadline); err != nil {
		t.Fatalf("Timeout after failed append: %v", err)
	}
	if state := mustState(t, host, timeoutID); state.Phase != PhaseAborted {
		t.Fatalf("state after timeout = %+v", state)
	}
	replayed, err = NewHost(journal)
	if err != nil {
		t.Fatalf("replay after timeout: %v", err)
	}
	if state := mustState(t, replayed, timeoutID); state.Phase != PhaseAborted || state.AbortReason != AbortTimeout {
		t.Fatalf("replayed timeout state = %+v", state)
	}
}

func countJournalRecords(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	records, valid, err := splitRecords(data)
	if err != nil {
		t.Fatalf("split journal: %v", err)
	}
	if valid != len(data) {
		t.Fatalf("journal has %d trailing bytes", len(data)-valid)
	}
	return len(records)
}

type failJournal struct {
	mu       sync.Mutex
	records  [][]byte
	failNext error
}

func (j *failJournal) Append(record []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failNext != nil {
		err := j.failNext
		j.failNext = nil
		return err
	}
	j.records = append(j.records, append([]byte(nil), record...))
	return nil
}

func (j *failJournal) Replay(fn func(record []byte) error) error {
	j.mu.Lock()
	records := make([][]byte, len(j.records))
	for i, record := range j.records {
		records[i] = append([]byte(nil), record...)
	}
	j.mu.Unlock()
	for _, record := range records {
		if err := fn(record); err != nil {
			return err
		}
	}
	return nil
}
