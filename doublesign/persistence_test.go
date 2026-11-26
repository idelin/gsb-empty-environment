package doublesign

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func freshDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "doublesign-test", t.Name())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildEpoch(t *testing.T, epoch uint64, keys []testKey, weights []uint64,
	authID string, authPriv ed25519.PrivateKey) (EpochSet, AuthorityAnchor) {
	members := make([]Member, len(keys))
	for i, k := range keys {
		members[i] = k.member(weights[i])
	}
	set := EpochSet{Epoch: epoch, ChainID: testChain, Members: members}
	return set, AnchorRoot(authID, authPriv, set)
}

// A conviction that occurs across a crash is still detected after replay, and
// the recovered auditor starts with the member already frozen.
func TestCrashRecoveryDoesNotMissConflict(t *testing.T) {
	dir := freshDir(t)
	authID, authPriv, authPub := genAuthority(t)
	k := genKey(t, "v1")
	set, anchor := buildEpoch(t, 1, []testKey{k}, []uint64{1}, authID, authPriv)

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := NewAuditor(testChain)
	a.PinAuthority(authPub)
	if err := a.WithStore(store); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, a, set, anchor, authPub)

	b1 := digest(0x40)
	b2 := digest(0x41)
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, b1, nil, 0)), VerdictAccepted)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash: a brand new auditor reopens the same directory.
	store2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	recovered := NewAuditor(testChain)
	recovered.PinAuthority(authPub)
	if err := recovered.WithStore(store2); err != nil {
		t.Fatal(err)
	}

	// Replay the conflicting share; it must still convict.
	r := recovered.Observe(share(k, 1, 0, 0, PhasePrepare, b2, nil, 0))
	expectVerdict(t, r, VerdictConvicted)
	if err := VerifyEvidence(*r.Evidence, authPub); err != nil {
		t.Fatalf("recovered evidence invalid: %v", err)
	}
	if err := store2.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen once more: the freeze and evidence file survive a second crash.
	store3, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	recovered2 := NewAuditor(testChain)
	recovered2.PinAuthority(authPub)
	if err := recovered2.WithStore(store3); err != nil {
		t.Fatal(err)
	}
	if !recovered2.IsFrozen(k.id) {
		t.Fatal("freeze must survive crash recovery")
	}
	evs, err := LoadEvidenceDir(filepath.Join(dir, "evidence"))
	if err != nil || len(evs) != 1 {
		t.Fatalf("expected one persisted evidence, got %d (err=%v)", len(evs), err)
	}
	if err := store3.Close(); err != nil {
		t.Fatal(err)
	}
}

// Out-of-order staging survives a crash: a staged commit is re-staged on
// replay and activates once the prepare arrives, with no false conviction.
func TestStagingSurvivesCrash(t *testing.T) {
	dir := freshDir(t)
	authID, authPriv, authPub := genAuthority(t)
	k := genKey(t, "v1")
	set, anchor := buildEpoch(t, 1, []testKey{k}, []uint64{1}, authID, authPriv)

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := NewAuditor(testChain)
	a.PinAuthority(authPub)
	if err := a.WithStore(store); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, a, set, anchor, authPub)
	g0 := digest(0x50)
	commit := share(k, 1, 0, 0, PhaseCommit, g0, nil, 0)
	expectVerdict(t, a.Observe(commit), VerdictStaged)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := NewAuditor(testChain)
	rec.PinAuthority(authPub)
	if err := rec.WithStore(store2); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, rec.Observe(share(k, 1, 0, 0, PhasePrepare, g0, nil, 0)), VerdictAccepted)
	// Commit now active; replay is a duplicate, and no freeze occurred.
	expectVerdict(t, rec.Observe(commit), VerdictDuplicate)
	if rec.IsFrozen(k.id) {
		t.Fatal("honest staged flow must not freeze after recovery")
	}
	if err := store2.Close(); err != nil {
		t.Fatal(err)
	}
}

// When a member is frozen, previously staged shares never reactivate and
// cannot mutate state after the conviction.
func TestFrozenStagedSharesNeverActivate(t *testing.T) {
	a, k, _, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	g0 := digest(0x60)

	// Trigger an equivocation on prepares to freeze the member.
	expectVerdict(t, a.Observe(share(k, 1, 0, 1, PhasePrepare, digest(0x61), nil, 0)), VerdictAccepted)
	r := a.Observe(share(k, 1, 0, 1, PhasePrepare, digest(0x62), nil, 0))
	expectVerdict(t, r, VerdictConvicted)

	// All subsequent shares of any phase are refused and cannot advance state.
	expectVerdict(t, a.Observe(share(k, 1, 0, 2, PhasePrepare, g0, nil, 0)), VerdictFrozen)
	expectVerdict(t, a.Observe(share(k, 1, 0, 2, PhaseCommit, g0, nil, 0)), VerdictFrozen)
	expectVerdict(t, a.Observe(viewShare(k, 1, 1, 2, 0, 0, 1, g0)), VerdictFrozen)

	// Internal staging for the frozen member must be empty: nothing old can
	// reactivate even if dependencies later arrive.
	a.mu.Lock()
	for _, m := range a.staged {
		if m.MemberID == k.id {
			t.Fatal("frozen member still has staged shares")
		}
	}
	a.mu.Unlock()
}

// A genuinely staged share (held before any conviction) is purged when the
// member is later frozen at another height, and can never activate afterward.
func TestPreExistingStagingPurgedOnFreeze(t *testing.T) {
	a, k, _, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	g0 := digest(0x66)
	g1 := digest(0x67)

	// Build an honest commit tip at height 0.
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, g0, nil, 0)), VerdictAccepted)
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhaseCommit, g0, nil, 0)), VerdictAccepted)

	// A prepare at height 2 is staged (height 1 commit tip is missing).
	staged := share(k, 1, 2, 1, PhasePrepare, g1, g0, 0)
	expectVerdict(t, a.Observe(staged), VerdictStaged)

	// Freeze the member with an equivocation at height 1.
	expectVerdict(t, a.Observe(share(k, 1, 1, 1, PhasePrepare, digest(0x68), g0, 0)), VerdictAccepted)
	expectVerdict(t, a.Observe(share(k, 1, 1, 1, PhasePrepare, digest(0x69), g0, 0)), VerdictConvicted)

	// The old staged share is gone and cannot be fed back to advance.
	expectVerdict(t, a.Observe(share(k, 1, 2, 2, PhasePrepare, digest(0x6A), g0, 0)), VerdictFrozen)
	a.mu.Lock()
	for _, m := range a.staged {
		if m.MemberID == k.id {
			t.Fatal("staged share was not purged on freeze")
		}
	}
	a.mu.Unlock()
}
