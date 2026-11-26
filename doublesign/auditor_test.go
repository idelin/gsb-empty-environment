package doublesign

import (
	"crypto/ed25519"
	"testing"
)

func newTestAuditor(t *testing.T, members ...Member) (*Auditor, testKey, ed25519.PublicKey, []testKey) {
	t.Helper()
	authID, authPriv, authPub := genAuthority(t)
	keys := make([]testKey, 0, len(members))
	for _, m := range members {
		keys = append(keys, genKey(t, m.ID))
	}
	// Rebuild members with generated keys.
	setMembers := make([]Member, len(keys))
	for i, k := range keys {
		setMembers[i] = Member{ID: k.id, PubKey: k.pub, Weight: members[i].Weight}
	}
	set := EpochSet{Epoch: 1, ChainID: testChain, Members: setMembers}
	anchor := AnchorRoot(authID, authPriv, set)
	a := NewAuditor(testChain)
	mustRegister(t, a, set, anchor, authPub)
	return a, keys[0], authPub, keys
}

func memberKeys(keys []testKey, weights ...uint64) []Member {
	out := make([]Member, len(keys))
	for i, k := range keys {
		w := uint64(1)
		if i < len(weights) {
			w = weights[i]
		}
		out[i] = k.member(w)
	}
	return out
}

// A benign stream of prepare->commit at consecutive heights must be accepted.
func TestHonestStreamAccepted(t *testing.T) {
	a, k, _, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1}, Member{ID: "v2", Weight: 2})
	g0 := digest(0xA0)
	g1 := digest(0xA1)

	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, g0, nil, 0)), VerdictAccepted)
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhaseCommit, g0, nil, 0)), VerdictAccepted)
	expectVerdict(t, a.Observe(share(k, 1, 1, 1, PhasePrepare, g1, g0, 0)), VerdictAccepted)
	expectVerdict(t, a.Observe(share(k, 1, 1, 1, PhaseCommit, g1, g0, 0)), VerdictAccepted)

	if a.IsFrozen(k.id) {
		t.Fatal("honest member must not be frozen")
	}
}

// Out-of-order arrival: commit before its prepare is staged, then activated.
func TestOutOfOrderStaging(t *testing.T) {
	a, k, _, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	g0 := digest(0xB0)

	commit := share(k, 1, 0, 0, PhaseCommit, g0, nil, 0)
	expectVerdict(t, a.Observe(commit), VerdictStaged)

	prepare := share(k, 1, 0, 0, PhasePrepare, g0, nil, 0)
	r := a.Observe(prepare)
	expectVerdict(t, r, VerdictAccepted)

	// The staged commit must now have been activated automatically.
	expectVerdict(t, a.Observe(commit), VerdictDuplicate)
	if a.IsFrozen(k.id) {
		t.Fatal("staged honest flow must not freeze")
	}
}

// Rule 1: same epoch/height/round/phase, two different blocks => conviction,
// and the resulting evidence verifies fully offline.
func TestEquivocationConvictsAndEvidenceVerifies(t *testing.T) {
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	b1 := digest(0xC1)
	b2 := digest(0xC2)

	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, b1, nil, 0)), VerdictAccepted)
	r := a.Observe(share(k, 1, 0, 0, PhasePrepare, b2, nil, 0))
	expectVerdict(t, r, VerdictConvicted)
	if r.Evidence == nil || r.Evidence.Violation != ViolationEquivocation {
		t.Fatal("expected equivocation evidence")
	}
	if err := VerifyEvidence(*r.Evidence, authPub); err != nil {
		t.Fatalf("offline evidence verification failed: %v", err)
	}
	if !a.IsFrozen(k.id) {
		t.Fatal("member must be frozen after conviction")
	}
	// Subsequent shares and old staged shares cannot advance.
	later := a.Observe(share(k, 1, 1, 1, PhasePrepare, digest(0xC3), b1, 0))
	expectVerdict(t, later, VerdictFrozen)
}

// Rule 2: a commit with only conflicting prepares at round <= commit round is
// an immediate commit-without-prepare conviction.
func TestCommitWithoutPrepareConvicts(t *testing.T) {
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	b1 := digest(0xD1)
	b2 := digest(0xD2)

	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, b1, nil, 0)), VerdictAccepted)
	r := a.Observe(share(k, 1, 0, 2, PhaseCommit, b2, nil, 0))
	expectVerdict(t, r, VerdictConvicted)
	if r.Evidence.Violation != ViolationCommitNoPrepare {
		t.Fatalf("got %s", r.Evidence.Violation)
	}
	if err := VerifyEvidence(*r.Evidence, authPub); err != nil {
		t.Fatalf("evidence: %v", err)
	}
}

// Rule 3: after acknowledging a high lock, switching to a higher round with a
// conflicting lower-lock block is a LockedQC rollback conviction.
func TestLockedQCRollbackConvicts(t *testing.T) {
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	blockX := digest(0xE1)
	blockY := digest(0xE2)

	// Round 1 prepare locks round 1 on block X.
	expectVerdict(t, a.Observe(share(k, 1, 5, 1, PhasePrepare, blockX, nil, 1)), VerdictAccepted)
	// Round 3 prepare falls back to lock 0 on conflicting block Y.
	r := a.Observe(share(k, 1, 5, 3, PhasePrepare, blockY, nil, 0))
	expectVerdict(t, r, VerdictConvicted)
	if r.Evidence.Violation != ViolationLockedQCRollback {
		t.Fatalf("got %s", r.Evidence.Violation)
	}
	if err := VerifyEvidence(*r.Evidence, authPub); err != nil {
		t.Fatalf("evidence: %v", err)
	}
}

// Rule 4: a prepare whose parent does not extend the last commit is convicted.
func TestParentLinkMismatchConvicts(t *testing.T) {
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	g0 := digest(0xF0)
	wrong := digest(0xF9)

	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, g0, nil, 0)), VerdictAccepted)
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhaseCommit, g0, nil, 0)), VerdictAccepted)
	r := a.Observe(share(k, 1, 1, 1, PhasePrepare, digest(0xF1), wrong, 0))
	expectVerdict(t, r, VerdictConvicted)
	if r.Evidence.Violation != ViolationParentMismatch {
		t.Fatalf("got %s", r.Evidence.Violation)
	}
	if err := VerifyEvidence(*r.Evidence, authPub); err != nil {
		t.Fatalf("evidence: %v", err)
	}
}

// A view_change claiming a prepare QC that does not match the member's prepare
// is a forged-QC conviction.
func TestViewQCForgedConvicts(t *testing.T) {
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	g0 := digest(0x10)

	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, g0, nil, 0)), VerdictAccepted)
	// Claims QC on a different block at the same height/round.
	r := a.Observe(viewShare(k, 1, 1, 1, 0, 0, 0, digest(0x11)))
	expectVerdict(t, r, VerdictConvicted)
	if r.Evidence.Violation != ViolationViewQCForged {
		t.Fatalf("got %s", r.Evidence.Violation)
	}
	if err := VerifyEvidence(*r.Evidence, authPub); err != nil {
		t.Fatalf("evidence: %v", err)
	}
}

// Cross-epoch rotation: weight changes and member removal are allowed; a
// removed member's new shares (including replays) are rejected, while its
// honest shares from the old epoch remain valid history.
func TestCrossEpochRemovalAndReplay(t *testing.T) {
	authID, authPriv, authPub := genAuthority(t)
	k1 := genKey(t, "v1")
	k2 := genKey(t, "v2")

	epoch1 := EpochSet{Epoch: 1, ChainID: testChain, Members: []Member{
		k1.member(1), k2.member(2),
	}}
	epoch2 := EpochSet{Epoch: 2, ChainID: testChain, Members: []Member{
		k2.member(5), // v1 removed; v2 weight changed 2 -> 5
	}}
	a := NewAuditor(testChain)
	mustRegister(t, a, epoch1, AnchorRoot(authID, authPriv, epoch1), authPub)
	mustRegister(t, a, epoch2, AnchorRoot(authID, authPriv, epoch2), authPub)

	g0 := digest(0x20)
	oldShare := share(k1, 1, 0, 0, PhasePrepare, g0, nil, 0)
	expectVerdict(t, a.Observe(oldShare), VerdictAccepted)

	// Replaying the identical old-epoch share is a harmless duplicate.
	expectVerdict(t, a.Observe(oldShare), VerdictDuplicate)

	// A "new" share from removed v1, even with a valid signature on epoch 2,
	// is rejected because v1 is no longer in the epoch-2 set.
	replayed := share(k1, 2, 0, 0, PhasePrepare, g0, nil, 0)
	r := a.Observe(replayed)
	expectVerdict(t, r, VerdictRejected)
	if a.IsFrozen(k1.id) {
		t.Fatal("removed-member rejection must not freeze the member")
	}

	// Reusing an old-epoch message byte-for-byte but mislabeled via epoch
	// boundary cannot authenticate: signature covers the epoch.
	forge := oldShare
	forge.Epoch = 2
	expectVerdict(t, a.Observe(forge), VerdictRejected)

	// v2 continues normally in epoch 2.
	expectVerdict(t, a.Observe(share(k2, 2, 0, 0, PhasePrepare, g0, nil, 0)), VerdictAccepted)
}

// A share that does not declare a registered epoch is rejected.
func TestUnknownEpochRejected(t *testing.T) {
	a, k, _, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	r := a.Observe(share(k, 99, 0, 0, PhasePrepare, digest(0x30), nil, 0))
	expectVerdict(t, r, VerdictRejected)
}
