package round

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestUnknownMemberIsRejected(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()

	_, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: "mallory", Commit: Digest{9}})
	if !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("unknown member commit: %v", err)
	}
	_, err = r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: "mallory", Secret: []byte("x")})
	if !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("unknown member reveal: %v", err)
	}
	if len(r.Snapshot().Conflicts) != 2 {
		t.Fatalf("conflicts = %+v", r.Snapshot().Conflicts)
	}
}

func TestZeroSecretRevealRejected(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	member := testRoster[0]

	zeroCommit := Commitment("r1", member, make([]byte, SecretSize))
	if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: zeroCommit}); err != nil {
		t.Fatal(err)
	}
	for _, secret := range [][]byte{nil, {}, make([]byte, SecretSize)} {
		_, err := r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: member, Secret: secret})
		if !errors.Is(err, ErrZeroSecret) {
			t.Fatalf("zero secret %v: %v", secret, err)
		}
	}
	if r.states[member].matched {
		t.Fatal("zero secret must never match")
	}
	if r.Status() != StatusOpen {
		t.Fatalf("status = %s", r.Status())
	}
}

func TestRevealBeforeCommitStillMatches(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	secrets := memberSecrets(t)

	// Every reveal arrives before any commitment; the round must still
	// complete once commitments catch up, in reverse roster order.
	for i := len(testRoster) - 1; i >= 0; i-- {
		member := testRoster[i]
		receipt, err := r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: member, Secret: secrets[member]})
		if err != nil || receipt.Accepted {
			t.Fatalf("early reveal %s: receipt=%+v err=%v", member, receipt, err)
		}
	}
	if _, err := r.Seed(); !errors.Is(err, ErrNoSeed) {
		t.Fatalf("seed before commits: %v", err)
	}
	for i := len(testRoster) - 1; i >= 0; i-- {
		member := testRoster[i]
		commit := Commitment("r1", member, secrets[member])
		if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: commit}); err != nil {
			t.Fatal(err)
		}
	}

	if r.Status() != StatusCompleted {
		t.Fatalf("status = %s", r.Status())
	}
	seed, err := r.Seed()
	if err != nil {
		t.Fatal(err)
	}
	want := expectedSeed("r1", testRoster, secrets)
	if string(seed) != string(want[:]) {
		t.Fatal("seed differs when reveals arrive first")
	}

	replayed, err := Replay(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status() != StatusCompleted {
		t.Fatalf("replayed status = %s", replayed.Status())
	}
	seed2, _ := replayed.Seed()
	if string(seed2) != string(seed) {
		t.Fatal("replayed seed differs")
	}
}

func TestPendingRevealMismatchRecordsEvidence(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	member := testRoster[0]

	wrong, _ := NewSecret()
	right, _ := NewSecret()

	// Reveal lands first, then a commitment for a different secret arrives.
	if _, err := r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: member, Secret: wrong}); err != nil {
		t.Fatal(err)
	}
	commit := Commitment("r1", member, right)
	if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: commit}); err != nil {
		t.Fatal(err)
	}

	state := r.states[member]
	if state.matched {
		t.Fatal("pending reveal must not match a commitment for another secret")
	}
	snap := r.Snapshot()
	var hasMismatch bool
	for _, conflict := range snap.Conflicts {
		if conflict.Kind == string(EvRevealMismatch) && conflict.Member == member {
			hasMismatch = true
		}
	}
	if !hasMismatch {
		t.Fatalf("expected pending-reveal mismatch evidence: %+v", snap.Conflicts)
	}

	// The correct reveal still resolves the member.
	receipt, err := r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: member, Secret: right})
	if err != nil || !receipt.Accepted {
		t.Fatalf("correct reveal after mismatch: %+v %v", receipt, err)
	}
	if !r.states[member].matched {
		t.Fatal("member should now be matched")
	}

	replayed, err := Replay(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.states[member].matched {
		t.Fatal("replayed member must stay matched")
	}
	if len(replayed.Snapshot().Conflicts) != len(snap.Conflicts) {
		t.Fatalf("replayed conflict count = %d, want %d", len(replayed.Snapshot().Conflicts), len(snap.Conflicts))
	}
}

func TestStatusStrings(t *testing.T) {
	cases := map[Status]string{
		StatusOpen:      "open",
		StatusCompleted: "completed",
		StatusAborted:   "aborted",
		Status(99):      "unknown",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", status, got, want)
		}
	}
}

func TestConcurrentSubmissions(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	secrets := memberSecrets(t)

	var wg sync.WaitGroup
	for round := 0; round < 5; round++ {
		for _, member := range testRoster {
			wg.Add(1)
			go func(member MemberID) {
				defer wg.Done()
				commit := Commitment("r1", member, secrets[member])
				_, _ = r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: commit})
			}(member)
		}
	}
	wg.Wait()

	// Reveals arrive concurrently, repeated and reordered. They must all
	// converge to the single deterministic seed.
	for round := 0; round < 5; round++ {
		for _, member := range testRoster {
			wg.Add(1)
			go func(member MemberID) {
				defer wg.Done()
				_, _ = r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: member, Secret: secrets[member]})
			}(member)
		}
	}
	wg.Wait()

	if r.Status() != StatusCompleted {
		snap := r.Snapshot()
		t.Fatalf("status = %s; matched=%v missing=%v", r.Status(), snap.Matched, snap.Missing)
	}
	seed, err := r.Seed()
	if err != nil {
		t.Fatal(err)
	}
	want := expectedSeed("r1", testRoster, secrets)
	if string(seed) != string(want[:]) {
		t.Fatal("concurrent submissions produced the wrong seed")
	}

	// Concurrent conflicting commitments must never replace the first one.
	altSecret, _ := NewSecret()
	for _, member := range testRoster {
		wg.Add(1)
		go func(member MemberID) {
			defer wg.Done()
			alt := Commitment("r1", member, altSecret)
			_, _ = r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: alt})
		}(member)
	}
	wg.Wait()

	if r.Status() != StatusCompleted {
		t.Fatalf("status = %s after adversarial traffic", r.Status())
	}
	seedAfter, err := r.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if string(seedAfter) != string(want[:]) {
		t.Fatal("conflicting traffic changed the derived seed")
	}
	conflicts := r.Snapshot().Conflicts
	if len(conflicts) < len(testRoster) {
		t.Fatalf("rejected-after-close conflicts = %+v", conflicts)
	}
	rejected := map[MemberID]bool{}
	for _, conflict := range conflicts {
		if conflict.Kind != string(EvRejected) {
			t.Fatalf("unexpected conflict kind %q: %+v", conflict.Kind, conflict)
		}
		rejected[conflict.Member] = true
	}
	for _, member := range testRoster {
		if !rejected[member] {
			t.Fatalf("member %s has no rejection evidence: %+v", member, conflicts)
		}
	}

	// The log must be a gap-free, sequentially numbered chain.
	events, _ := r.Events(ctx)
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i+1, ev.Seq)
		}
	}

	// Replay under the same concurrent history must be byte-identical.
	replayed, err := Replay(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	seed2, _ := replayed.Seed()
	if string(seed2) != string(seed) {
		t.Fatal("replay diverged from live state")
	}
}

func TestStoreFailureMarksRoundBroken(t *testing.T) {
	store := &memStore{failAfter: 1, failErr: errors.New("disk melted")}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()

	_, err := r.SubmitCommit(ctx, CommitMessage{
		RoundID: "r1",
		Member:  testRoster[0],
		Commit:  Commitment("r1", testRoster[0], []byte("x")),
	})
	if err == nil {
		t.Fatal("expected store failure")
	}
	if _, err := r.Seed(); !errors.Is(err, ErrNoSeed) {
		t.Fatalf("seed after failure: %v", err)
	}
	_, err = r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: testRoster[0], Commit: Digest{}})
	if !errors.Is(err, ErrStoreBroken) {
		t.Fatalf("write after store failure: %v", err)
	}

	// The durable log is still replayable; the failed event never landed.
	replayed, err := Replay(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.Snapshot().Committed) != 0 {
		t.Fatal("failed append must not appear in the log")
	}
}
