package round

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
)

var testRoster = []MemberID{"alice", "bob", "carol"}

// memStore is an in-memory EventStore for tests that can also fail on demand.
type memStore struct {
	mu        sync.Mutex
	events    []Event
	failAfter int
	failErr   error
}

func (m *memStore) Append(_ context.Context, event *Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil && len(m.events) >= m.failAfter {
		return m.failErr
	}
	copied := *event
	m.events = append(m.events, copied)
	return nil
}

func (m *memStore) Read(_ context.Context) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out, nil
}

func newTestRound(t *testing.T, store EventStore, roundID string) *Round {
	t.Helper()
	r, err := Start(context.Background(), Config{ID: roundID, Members: testRoster}, store)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return r
}

func memberSecrets(t *testing.T) map[MemberID][]byte {
	t.Helper()
	secrets := map[MemberID][]byte{}
	for _, member := range testRoster {
		secret, err := NewSecret()
		if err != nil {
			t.Fatalf("NewSecret: %v", err)
		}
		secrets[member] = secret
	}
	return secrets
}

func expectedSeed(roundID string, members []MemberID, secrets map[MemberID][]byte) Digest {
	ordered := append([]MemberID(nil), members...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	seed, err := deriveSeed(roundID, ordered, secrets)
	if err != nil {
		panic(err)
	}
	return seed
}

func submitAll(t *testing.T, r *Round, secrets map[MemberID][]byte) {
	t.Helper()
	ctx := context.Background()
	for _, member := range testRoster {
		commit := Commitment(r.cfg.ID, member, secrets[member])
		receipt, err := r.SubmitCommit(ctx, CommitMessage{RoundID: r.cfg.ID, Member: member, Commit: commit})
		if err != nil || !receipt.Accepted {
			t.Fatalf("commit %s: receipt=%+v err=%v", member, receipt, err)
		}
	}
	for _, member := range testRoster {
		receipt, err := r.SubmitReveal(ctx, RevealMessage{RoundID: r.cfg.ID, Member: member, Secret: secrets[member]})
		if err != nil || !receipt.Accepted {
			t.Fatalf("reveal %s: receipt=%+v err=%v", member, receipt, err)
		}
	}
}

func TestHappyPathProducesSeed(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	secrets := memberSecrets(t)

	if _, err := r.Seed(); !errors.Is(err, ErrNoSeed) {
		t.Fatalf("seed before completion: %v", err)
	}

	submitAll(t, r, secrets)

	if r.Status() != StatusCompleted {
		t.Fatalf("status = %s, want completed", r.Status())
	}
	seed, err := r.Seed()
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	want := expectedSeed("r1", testRoster, secrets)
	if !bytes.Equal(seed, want[:]) {
		t.Fatalf("seed mismatch")
	}
	snap := r.Snapshot()
	if len(snap.Matched) != len(testRoster) || len(snap.Missing) != 0 {
		t.Fatalf("snapshot matched=%v missing=%v", snap.Matched, snap.Missing)
	}
}

func TestCommitmentDomainSeparation(t *testing.T) {
	secret := []byte("the same secret payload")
	base := Commitment("round-a", "alice", secret)
	if Commitment("round-b", "alice", secret) == base {
		t.Fatal("commitments must differ across rounds")
	}
	if Commitment("round-a", "bob", secret) == base {
		t.Fatal("commitments must differ across members")
	}
	if Commitment("round-a", "alice", append([]byte(nil), secret...)) != base {
		t.Fatal("identical inputs must hash identically")
	}
	// Length prefixes must disambiguate concatenations.
	if Commitment("r", "ab", []byte("c")) == Commitment("r", "a", []byte("bc")) {
		t.Fatal("length-prefixed framing must prevent concatenation collisions")
	}
}

func TestRevealMustMatchCommitment(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	alice := testRoster[0]

	real, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	fake, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	commit := Commitment("r1", alice, real)
	if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: alice, Commit: commit}); err != nil {
		t.Fatal(err)
	}

	receipt, err := r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: alice, Secret: fake})
	if err != nil {
		t.Fatalf("mismatch should be a receipt, not a transport error: %v", err)
	}
	if receipt.Accepted {
		t.Fatal("non-matching reveal must not be accepted")
	}
	snap := r.Snapshot()
	if len(snap.Conflicts) != 1 || snap.Conflicts[0].Kind != string(EvRevealMismatch) {
		t.Fatalf("conflicts = %+v", snap.Conflicts)
	}

	// The correct reveal still completes the member afterwards.
	receipt, err = r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: alice, Secret: real})
	if err != nil || !receipt.Accepted {
		t.Fatalf("correct reveal: receipt=%+v err=%v", receipt, err)
	}
}

func TestConflictingCommitIsRefusedWithEvidence(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	member := testRoster[1]

	first := Commitment("r1", member, []byte("first secret"))
	second := Commitment("r1", member, []byte("second secret"))
	if first == second {
		t.Fatal("test setup: distinct commitments expected")
	}
	if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: first}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: second}); err != nil {
		t.Fatal(err)
	}

	snap := r.Snapshot()
	if len(snap.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", snap.Conflicts)
	}
	conflict := snap.Conflicts[0]
	if conflict.Kind != string(EvCommitConflict) || conflict.Member != member {
		t.Fatalf("unexpected conflict %+v", conflict)
	}

	var found bool
	for _, ev := range store.events {
		if ev.Kind == EvCommitConflict {
			found = true
			if ev.Evidence == nil || ev.Evidence.ExistingDigest != first || len(ev.Evidence.Offending) == 0 {
				t.Fatalf("conflict event must retain evidence: %+v", ev)
			}
		}
	}
	if !found {
		t.Fatal("conflict was not persisted")
	}

	replayed, err := Replay(ctx, store)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	rs := replayed.Snapshot()
	if len(rs.Conflicts) != 1 || rs.Conflicts[0] != conflict {
		t.Fatalf("replayed conflicts = %+v, want %+v", rs.Conflicts, conflict)
	}
}

func TestDuplicateMessagesAreIdempotent(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	secrets := memberSecrets(t)

	commitMsg := CommitMessage{
		RoundID: "r1",
		Member:  testRoster[0],
		Commit:  Commitment("r1", testRoster[0], secrets[testRoster[0]]),
	}
	revealMsg := RevealMessage{RoundID: "r1", Member: testRoster[0], Secret: secrets[testRoster[0]]}

	if _, err := r.SubmitCommit(ctx, commitMsg); err != nil {
		t.Fatal(err)
	}
	again, err := r.SubmitCommit(ctx, commitMsg)
	if err != nil || again.Accepted || !again.Duplicate {
		t.Fatalf("duplicate commit: %+v %v", again, err)
	}
	third, err := r.SubmitCommit(ctx, commitMsg)
	if err != nil || !third.Duplicate {
		t.Fatalf("second duplicate commit: %+v %v", third, err)
	}

	if _, err := r.SubmitReveal(ctx, revealMsg); err != nil {
		t.Fatal(err)
	}
	dupReveal, err := r.SubmitReveal(ctx, revealMsg)
	if err != nil || !dupReveal.Duplicate {
		t.Fatalf("duplicate reveal: %+v %v", dupReveal, err)
	}

	counts := map[Kind]int{}
	for _, ev := range store.events {
		counts[ev.Kind]++
	}
	if counts[EvCommitAccepted] != 1 || counts[EvCommitDuplicate] != 2 {
		t.Fatalf("commit event counts = %+v", counts)
	}

	replayed, err := Replay(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	rs := replayed.Snapshot()
	if len(rs.Committed) != 1 || len(rs.Matched) != 1 {
		t.Fatalf("replayed state = %+v", rs)
	}
}

func TestAbsenceNeverBecomesZeroOrSeed(t *testing.T) {
	store := &memStore{}
	r := newTestRound(t, store, "r1")
	ctx := context.Background()
	secrets := memberSecrets(t)

	// carol never participates.
	for _, member := range testRoster[:2] {
		commit := Commitment("r1", member, secrets[member])
		if _, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: member, Commit: commit}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.SubmitReveal(ctx, RevealMessage{RoundID: "r1", Member: member, Secret: secrets[member]}); err != nil {
			t.Fatal(err)
		}
	}
	if r.Status() != StatusOpen {
		t.Fatalf("status = %s, want open", r.Status())
	}
	if _, err := r.Seed(); !errors.Is(err, ErrNoSeed) {
		t.Fatalf("seed with absent member: %v", err)
	}
	snap := r.Snapshot()
	if len(snap.Missing) != 1 || snap.Missing[0] != "carol" || snap.Seed != nil {
		t.Fatalf("snapshot = %+v", snap)
	}

	if err := r.Abort(ctx, "carol absent"); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if err := r.Abort(ctx, "again"); err != nil {
		t.Fatalf("re-abort: %v", err)
	}
	if r.Status() != StatusAborted {
		t.Fatalf("status = %s", r.Status())
	}
	if _, err := r.Seed(); !errors.Is(err, ErrNoSeed) {
		t.Fatalf("seed after abort: %v", err)
	}
	_, err := r.SubmitCommit(ctx, CommitMessage{RoundID: "r1", Member: "carol", Commit: Digest{}})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("write after abort: %v", err)
	}

	// A zero secret must never be treated as carol's missing contribution.
	_, err = deriveSeed("r1", testRoster, map[MemberID][]byte{
		"alice": secrets["alice"],
		"bob":   secrets["bob"],
		"carol": {},
	})
	if !errors.Is(err, ErrNoSeed) {
		t.Fatalf("zero secret substituted for absence: %v", err)
	}
}

func TestRevealsCannotCrossRounds(t *testing.T) {
	storeA := &memStore{}
	storeB := &memStore{}
	roundA := newTestRound(t, storeA, "round-A")
	_ = newTestRound(t, storeB, "round-B")
	ctx := context.Background()
	secret, _ := NewSecret()

	commitA := Commitment("round-A", "alice", secret)
	if _, err := roundA.SubmitCommit(ctx, CommitMessage{RoundID: "round-A", Member: "alice", Commit: commitA}); err != nil {
		t.Fatal(err)
	}

	// A reveal or commit naming another round is rejected and evidenced.
	_, err := roundA.SubmitReveal(ctx, RevealMessage{RoundID: "round-B", Member: "alice", Secret: secret})
	if !errors.Is(err, ErrForeignRound) {
		t.Fatalf("foreign reveal: %v", err)
	}
	_, err = roundA.SubmitCommit(ctx, CommitMessage{RoundID: "round-B", Member: "alice", Commit: Digest{1}})
	if !errors.Is(err, ErrForeignRound) {
		t.Fatalf("foreign commit: %v", err)
	}

	// The foreign reveal must not have altered round A's member state.
	state := roundA.states["alice"]
	if len(state.reveal) != 0 || state.matched {
		t.Fatalf("foreign reveal leaked into state: %+v", state)
	}

	// A reveal computed under round B must not validate round A's commitment.
	foreignCommitment := Commitment("round-B", "alice", secret)
	if foreignCommitment == commitA {
		t.Fatal("domain separation failed across rounds")
	}

	events, _ := roundA.Events(ctx)
	var rejected int
	for _, ev := range events {
		if ev.Kind == EvRejected && ev.Evidence != nil && ev.Evidence.RawHash == (Digest{}) {
			t.Fatal("rejected foreign message must retain a raw hash as evidence")
		}
		if ev.Kind == EvRejected {
			rejected++
		}
	}
	if rejected != 2 {
		t.Fatalf("rejected events = %d, want 2", rejected)
	}
}
