package netsec

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
)

func newTestState(t *testing.T) EpochState {
	t.Helper()
	return EpochState{
		GroupID:                []byte("group-0200"),
		Epoch:                  7,
		InitSecret:             bytes.Repeat([]byte{0x11}, EpochSecretSize),
		TreeHash:               []byte("tree-epoch-7"),
		ConfirmationTranscript: []byte("transcript-epoch-7"),
		Leaves:                 []LeafID{0, 1, 2},
	}
}

func newTestCommit(t *testing.T, state EpochState) Commit {
	t.Helper()
	return Commit{
		GroupID:                state.GroupID,
		Epoch:                  state.Epoch,
		ParentTreeHash:         state.TreeHash,
		ParentTranscriptHash:   ComputeTranscriptHash(state.ConfirmationTranscript),
		TreeHash:               []byte("tree-epoch-8"),
		ConfirmationTranscript: []byte("commit-confirmation-transcript"),
		RemovedLeaves:          []LeafID{2},
		Contributions: []LeafContribution{
			{LeafID: 0, Secret: bytes.Repeat([]byte{0xa0}, 32)},
			{LeafID: 1, Secret: bytes.Repeat([]byte{0xa1}, 32)},
		},
	}
}

func TestProcessCommitAdvancesEpochAndBindsGroupContext(t *testing.T) {
	state := newTestState(t)
	commit := newTestCommit(t, state)

	advance, err := ProcessCommit(&state, commit)
	if err != nil {
		t.Fatalf("ProcessCommit: %v", err)
	}

	if state.Epoch != 8 || advance.Epoch != 8 || advance.PreviousEpoch != 7 {
		t.Fatalf("epoch did not increment by one: state=%d advance=%d previous=%d",
			state.Epoch, advance.Epoch, advance.PreviousEpoch)
	}
	if got, want := state.Leaves, []LeafID{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active leaves = %v, want %v", got, want)
	}
	if !bytes.Equal(state.TreeHash, commit.TreeHash) {
		t.Fatalf("tree hash = %q, want %q", state.TreeHash, commit.TreeHash)
	}
	if !bytes.Equal(state.InitSecret, advance.NextInitSecret) {
		t.Fatal("state did not rotate to the next init secret")
	}
	if bytes.Equal(state.InitSecret, newTestState(t).InitSecret) {
		t.Fatal("init secret was reused across epochs")
	}

	wantContextHash := ComputeGroupContextHash(
		8, commit.TreeHash, commit.ConfirmationTranscript,
	)
	if !bytes.Equal(advance.GroupContextHash, wantContextHash) {
		t.Fatalf("group context hash = %x, want %x", advance.GroupContextHash, wantContextHash)
	}
	if !bytes.Equal(state.LastGroupContextHash, wantContextHash) {
		t.Fatal("state did not record the accepted group context")
	}

	wantExtracted, err := hkdf.Extract(sha256.New, newTestState(t).InitSecret, advance.CommitSecret)
	if err != nil {
		t.Fatalf("HKDF extract: %v", err)
	}
	if !bytes.Equal(advance.ExtractedSecret, wantExtracted) {
		t.Fatalf("extracted secret = %x, want %x", advance.ExtractedSecret, wantExtracted)
	}
	if !bytes.Equal(advance.EpochSecret, wantExtracted) {
		t.Fatalf("epoch secret = %x, want raw extract output %x", advance.EpochSecret, wantExtracted)
	}
	wantBoundEpochSecret, err := hkdf.Expand(
		sha256.New, wantExtracted, string(wantContextHash), EpochSecretSize,
	)
	if err != nil {
		t.Fatalf("HKDF expand: %v", err)
	}
	if !bytes.Equal(advance.BoundEpochSecret, wantBoundEpochSecret) {
		t.Fatalf("bound epoch secret = %x, want %x", advance.BoundEpochSecret, wantBoundEpochSecret)
	}
	if len(advance.EpochAuthenticator) != sha256.Size {
		t.Fatalf("epoch authenticator size = %d, want %d", len(advance.EpochAuthenticator), sha256.Size)
	}
}

func TestRemovedLeafDoesNotContributeCommitSecret(t *testing.T) {
	removed := []LeafID{2}
	active := []LeafContribution{
		{LeafID: 0, Secret: []byte("leaf-0-secret")},
		{LeafID: 1, Secret: []byte("leaf-1-secret")},
	}
	withRemoved := append(active, LeafContribution{
		LeafID: 2,
		Secret: []byte("removed-member-old-epoch-secret"),
	})

	withoutSecret, err := DeriveCommitSecret(active, removed)
	if err != nil {
		t.Fatalf("DeriveCommitSecret(active): %v", err)
	}
	withSecret, err := DeriveCommitSecret(withRemoved, removed)
	if err != nil {
		t.Fatalf("DeriveCommitSecret(with removed): %v", err)
	}
	if !bytes.Equal(withoutSecret, withSecret) {
		t.Fatal("removed leaf influenced the commit secret")
	}

	// The same leaf set in a different order must also produce the same
	// commit secret.
	reordered := []LeafContribution{active[1], active[0]}
	reorderedSecret, err := DeriveCommitSecret(reordered, removed)
	if err != nil {
		t.Fatalf("DeriveCommitSecret(reordered): %v", err)
	}
	if !bytes.Equal(withoutSecret, reorderedSecret) {
		t.Fatal("commit secret depended on contribution order")
	}

	// The whole commit process must ignore a removed leaf's retained old
	// epoch contribution.
	stateA := newTestState(t)
	stateB := newTestState(t)
	commitA := newTestCommit(t, stateA)
	commitB := newTestCommit(t, stateB)
	commitB.Contributions = append(commitB.Contributions, LeafContribution{
		LeafID: 2,
		Secret: []byte("removed-member-old-epoch-secret"),
	})

	advanceA, err := ProcessCommit(&stateA, commitA)
	if err != nil {
		t.Fatalf("ProcessCommit(A): %v", err)
	}
	advanceB, err := ProcessCommit(&stateB, commitB)
	if err != nil {
		t.Fatalf("ProcessCommit(B): %v", err)
	}
	if !bytes.Equal(advanceA.CommitSecret, advanceB.CommitSecret) {
		t.Fatal("removed leaf changed the derived commit secret")
	}
	if !bytes.Equal(advanceA.EpochSecret, advanceB.EpochSecret) {
		t.Fatal("removed leaf changed the derived epoch secret")
	}
	if !reflect.DeepEqual(stateA.Leaves, stateB.Leaves) {
		t.Fatalf("active leaves differ: %v vs %v", stateA.Leaves, stateB.Leaves)
	}
}

func TestOldEpochReplayRejected(t *testing.T) {
	state := newTestState(t)
	commit := newTestCommit(t, state)
	if err := ApplyCommit(&state, commit); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	after := state.Clone()

	// A removed member retains the epoch-7 keys and replays the commit that
	// removed it. It must not move the service back to epoch 7.
	if _, err := ProcessCommit(&state, commit); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("replay error = %v, want ErrStaleEpoch", err)
	}
	if !reflect.DeepEqual(state, after) {
		t.Fatal("state changed after rejected replay")
	}
}

func TestForkedCommitRejected(t *testing.T) {
	base := newTestState(t)
	commit := newTestCommit(t, base)
	after, advance, err := AdvanceEpoch(base, commit)
	if err != nil {
		t.Fatalf("AdvanceEpoch: %v", err)
	}
	if after.Epoch != 8 {
		t.Fatalf("after epoch = %d, want 8", after.Epoch)
	}

	treeFork := commit
	treeFork.ParentTreeHash = []byte("different-parent-tree")
	if _, err := ProcessCommit(&base, treeFork); !errors.Is(err, ErrForkedCommit) {
		t.Fatalf("tree fork error = %v, want ErrForkedCommit", err)
	}

	transcriptFork := commit
	transcriptFork.ParentTranscriptHash = []byte("different-parent-transcript")
	if _, err := ProcessCommit(&base, transcriptFork); !errors.Is(err, ErrForkedCommit) {
		t.Fatalf("transcript fork error = %v, want ErrForkedCommit", err)
	}

	commitFork := commit
	commitFork.ParentCommitID = []byte("different-parent-commit")
	if _, err := ProcessCommit(&base, commitFork); !errors.Is(err, ErrForkedCommit) {
		t.Fatalf("commit fork error = %v, want ErrForkedCommit", err)
	}

	contextFork := commit
	contextFork.ParentGroupContextHash = []byte("different-parent-context")
	if _, err := ProcessCommit(&base, contextFork); !errors.Is(err, ErrForkedCommit) {
		t.Fatalf("context fork error = %v, want ErrForkedCommit", err)
	}

	// A second valid commit built on the same base epoch is the other
	// branch of a fork. Once the first branch has been accepted, the
	// service must not accept the second branch.
	otherBranch := commit
	otherBranch.TreeHash = []byte("other-branch-tree")
	otherBranch.ConfirmationTranscript = []byte("other-branch-transcript")
	otherBranch.Contributions = []LeafContribution{
		{LeafID: 0, Secret: bytes.Repeat([]byte{0xb0}, 32)},
		{LeafID: 1, Secret: bytes.Repeat([]byte{0xb1}, 32)},
	}
	if _, err := ProcessCommit(&after, otherBranch); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("forked branch error = %v, want ErrStaleEpoch", err)
	}
	if !bytes.Equal(after.LastCommitID, advance.CommitID) {
		t.Fatal("accepted commit ID was not recorded")
	}
}

func TestGroupContextBindsEveryField(t *testing.T) {
	tree := []byte("tree")
	transcript := []byte("confirmation-transcript")
	base := ComputeGroupContextHash(8, tree, transcript)

	cases := map[string][]byte{
		"epoch":      ComputeGroupContextHash(9, tree, transcript),
		"tree":       ComputeGroupContextHash(8, []byte("other-tree"), transcript),
		"transcript": ComputeGroupContextHash(8, tree, []byte("other-transcript")),
	}
	for name, changed := range cases {
		if bytes.Equal(base, changed) {
			t.Fatalf("group context hash did not change when %s changed", name)
		}
	}

	initSecret := bytes.Repeat([]byte{0x01}, 32)
	commitSecret := bytes.Repeat([]byte{0x02}, 32)
	boundBase, err := DeriveEpochSecret(initSecret, commitSecret, base)
	if err != nil {
		t.Fatalf("DeriveEpochSecret: %v", err)
	}
	boundOther, err := DeriveEpochSecret(initSecret, commitSecret, cases["tree"])
	if err != nil {
		t.Fatalf("DeriveEpochSecret: %v", err)
	}
	if bytes.Equal(boundBase, boundOther) {
		t.Fatal("context-bound epoch secret did not change with the tree hash")
	}
}

func TestTransitionErrorsAreAtomic(t *testing.T) {
	base := newTestState(t)
	commit := newTestCommit(t, base)

	missing := commit
	missing.Contributions = commit.Contributions[:1]
	if _, err := ProcessCommit(&base, missing); !errors.Is(err, ErrMissingContribution) {
		t.Fatalf("missing contribution error = %v, want ErrMissingContribution", err)
	}
	if !reflect.DeepEqual(base, newTestState(t)) {
		t.Fatal("state changed after missing-contribution failure")
	}

	unknown := commit
	unknown.Contributions = append(unknown.Contributions, LeafContribution{
		LeafID: 9,
		Secret: []byte("unknown"),
	})
	if _, err := ProcessCommit(&base, unknown); !errors.Is(err, ErrUnknownLeaf) {
		t.Fatalf("unknown leaf error = %v, want ErrUnknownLeaf", err)
	}
	if !reflect.DeepEqual(base, newTestState(t)) {
		t.Fatal("state changed after unknown-leaf failure")
	}

	duplicate := commit
	duplicate.Contributions = append(duplicate.Contributions, commit.Contributions[0])
	if _, err := ProcessCommit(&base, duplicate); !errors.Is(err, ErrDuplicateLeaf) {
		t.Fatalf("duplicate contribution error = %v, want ErrDuplicateLeaf", err)
	}
	if !reflect.DeepEqual(base, newTestState(t)) {
		t.Fatal("state changed after duplicate-contribution failure")
	}

	notMember := commit
	notMember.RemovedLeaves = []LeafID{9}
	if _, err := ProcessCommit(&base, notMember); !errors.Is(err, ErrInvalidCommit) {
		t.Fatalf("non-member removal error = %v, want ErrInvalidCommit", err)
	}
	if !reflect.DeepEqual(base, newTestState(t)) {
		t.Fatal("state changed after non-member removal failure")
	}

	future := commit
	future.Epoch = base.Epoch + 1
	if _, err := ProcessCommit(&base, future); !errors.Is(err, ErrFutureEpoch) {
		t.Fatalf("future commit error = %v, want ErrFutureEpoch", err)
	}
	if !reflect.DeepEqual(base, newTestState(t)) {
		t.Fatal("state changed after future-epoch failure")
	}

	wrongGroup := commit
	wrongGroup.GroupID = []byte("other-group")
	if _, err := ProcessCommit(&base, wrongGroup); !errors.Is(err, ErrGroupMismatch) {
		t.Fatalf("group mismatch error = %v, want ErrGroupMismatch", err)
	}
}

func TestCommitIDIsOrderIndependent(t *testing.T) {
	state := newTestState(t)
	commit := newTestCommit(t, state)
	reordered := commit
	reordered.RemovedLeaves = []LeafID{2}
	reordered.Contributions = []LeafContribution{commit.Contributions[1], commit.Contributions[0]}
	if !bytes.Equal(commit.ID(), reordered.ID()) {
		t.Fatal("commit ID depended on contribution order")
	}

	changed := commit
	changed.TreeHash = []byte("different-tree")
	if bytes.Equal(commit.ID(), changed.ID()) {
		t.Fatal("commit ID did not bind the tree hash")
	}
}

func TestPreAggregatedCommitSecret(t *testing.T) {
	state := newTestState(t)
	commit := newTestCommit(t, state)
	commit.Contributions = nil
	commit.CommitSecret = mustDeriveCommitSecret(t,
		[]LeafContribution{
			{LeafID: 0, Secret: bytes.Repeat([]byte{0xa0}, 32)},
			{LeafID: 1, Secret: bytes.Repeat([]byte{0xa1}, 32)},
		},
		[]LeafID{2},
	)

	advance, err := ProcessCommit(&state, commit)
	if err != nil {
		t.Fatalf("ProcessCommit with pre-aggregated secret: %v", err)
	}
	if !bytes.Equal(advance.CommitSecret, commit.CommitSecret) {
		t.Fatal("pre-aggregated commit secret was not used")
	}
	if got, want := state.Leaves, []LeafID{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active leaves = %v, want %v", got, want)
	}

	ambiguousState := newTestState(t)
	ambiguous := newTestCommit(t, ambiguousState)
	ambiguous.CommitSecret = []byte("direct")
	if _, err := ProcessCommit(&ambiguousState, ambiguous); !errors.Is(err, ErrInvalidCommit) {
		t.Fatalf("ambiguous secret error = %v, want ErrInvalidCommit", err)
	}
}

func TestEpochSecretWithoutContextIsHKDFExtract(t *testing.T) {
	initSecret := bytes.Repeat([]byte{0x01}, 32)
	commitSecret := bytes.Repeat([]byte{0x02}, 32)
	got, err := DeriveEpochSecret(initSecret, commitSecret)
	if err != nil {
		t.Fatalf("DeriveEpochSecret: %v", err)
	}
	want, err := hkdf.Extract(sha256.New, initSecret, commitSecret)
	if err != nil {
		t.Fatalf("HKDF extract: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("derived secret = %x, want extract output %x", got, want)
	}
}

func mustDeriveCommitSecret(t *testing.T, contributions []LeafContribution, removed []LeafID) []byte {
	t.Helper()
	secret, err := DeriveCommitSecret(contributions, removed)
	if err != nil {
		t.Fatalf("DeriveCommitSecret: %v", err)
	}
	return secret
}
