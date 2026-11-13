package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type testSigner struct {
	member Member
	priv   ed25519.PrivateKey
}

func newTestSigner(t *testing.T, id string, seed byte) testSigner {
	t.Helper()
	seedBytes := bytes.Repeat([]byte{seed}, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seedBytes)
	pub := priv.Public().(ed25519.PublicKey)
	return testSigner{member: NewMember(id, pub), priv: priv}
}

func testDigest(label string) Digest {
	return Digest(sha256.Sum256([]byte(label)))
}

func (s testSigner) share(t *testing.T, chain string, height, round uint64, phase Phase, block, parent Digest) Share {
	t.Helper()
	share := Share{
		ChainID:      chain,
		MemberID:     s.member.ID,
		Height:       height,
		Round:        round,
		Phase:        phase,
		BlockDigest:  block,
		ParentDigest: parent,
	}
	signed, err := SignShare(s.priv, share)
	if err != nil {
		t.Fatalf("sign share: %v", err)
	}
	if err := signed.Verify(s.member.PublicKey); err != nil {
		t.Fatalf("verify generated share: %v", err)
	}
	return signed
}

func testLedger(t *testing.T, signers ...testSigner) (*Ledger, string) {
	t.Helper()
	members := make([]Member, len(signers))
	for i, signer := range signers {
		members[i] = signer.member
	}
	ledger, err := NewLedger(Config{
		ChainID:       "chain-test",
		Members:       members,
		GenesisDigest: testDigest("genesis"),
	})
	if err != nil {
		t.Fatalf("new ledger: %v", err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	return ledger, "chain-test"
}

func requireStatus(t *testing.T, result Result, err error, want Status) {
	t.Helper()
	if err != nil {
		t.Fatalf("submit returned error: %v (status %s)", err, result.Status)
	}
	if result.Status != want {
		t.Fatalf("status = %s, want %s (err=%v)", result.Status, want, result.Err)
	}
}

func TestOutOfOrderPrepareCommitRelease(t *testing.T) {
	alice := newTestSigner(t, "alice", 1)
	bob := newTestSigner(t, "bob", 2)
	ledger, chain := testLedger(t, alice, bob)
	genesis := testDigest("genesis")
	blockA := testDigest("height-1/A")
	blockB := testDigest("height-2/B")

	prepare2 := alice.share(t, chain, 2, 2, Prepare, blockB, blockA)
	commit2 := alice.share(t, chain, 2, 2, Commit, blockB, blockA)
	prepare1 := alice.share(t, chain, 1, 1, Prepare, blockA, genesis)
	commit1 := alice.share(t, chain, 1, 1, Commit, blockA, genesis)

	result, err := ledger.Submit(prepare2)
	requireStatus(t, result, err, StatusPending)
	result, err = ledger.Submit(commit2)
	requireStatus(t, result, err, StatusPending)
	result, err = ledger.Submit(commit1)
	requireStatus(t, result, err, StatusPending)
	pending, _ := ledger.Pending(alice.member.ID)
	if len(pending) != 3 {
		t.Fatalf("pending = %d, want 3", len(pending))
	}

	result, err = ledger.Submit(prepare1)
	requireStatus(t, result, err, StatusPublished)
	if len(result.Published) != 4 {
		t.Fatalf("published = %d shares, want 4", len(result.Published))
	}
	wantOrder := []string{
		prepare1.EventID(),
		commit1.EventID(),
		prepare2.EventID(),
		commit2.EventID(),
	}
	for i, want := range wantOrder {
		if got := result.Published[i].EventID(); got != want {
			t.Fatalf("published[%d] = %s, want %s", i, got, want)
		}
	}
	if pending, _ = ledger.Pending(alice.member.ID); len(pending) != 0 {
		t.Fatalf("pending after release = %d, want 0", len(pending))
	}
	if ledger.IsFrozen(alice.member.ID) {
		t.Fatal("honest ordering was frozen")
	}

	duplicate, err := ledger.Submit(prepare1)
	requireStatus(t, duplicate, err, StatusDuplicate)
	if len(duplicate.Published) != 0 {
		t.Fatalf("duplicate republished %d shares", len(duplicate.Published))
	}
}

func TestSameHeightSamePhaseDifferentRoundsIsDoubleSign(t *testing.T) {
	alice := newTestSigner(t, "alice", 3)
	bob := newTestSigner(t, "bob", 4)
	ledger, chain := testLedger(t, alice, bob)
	genesis := testDigest("genesis")
	blockA := testDigest("block-A")
	blockB := testDigest("block-B")
	blockC := testDigest("block-C")

	first := alice.share(t, chain, 1, 1, Prepare, blockA, genesis)
	second := alice.share(t, chain, 1, 2, Prepare, blockB, genesis)
	result, err := ledger.Submit(first)
	requireStatus(t, result, err, StatusPublished)

	result, err = ledger.Submit(second)
	requireStatus(t, result, err, StatusDoubleSigned)
	if result.Evidence == nil {
		t.Fatal("double sign result has no evidence")
	}
	if err := ledger.VerifyEvidence(*result.Evidence); err != nil {
		t.Fatalf("verify evidence: %v", err)
	}
	if result.Evidence.Height() != 1 || result.Evidence.Phase() != Prepare ||
		result.Evidence.MemberID() != alice.member.ID {
		t.Fatalf("unexpected evidence metadata: %+v", result.Evidence)
	}
	if result.Evidence.ShareA.BlockDigest == result.Evidence.ShareB.BlockDigest {
		t.Fatal("evidence has identical block digests")
	}

	frozen, ok := ledger.FrozenEvidence(alice.member.ID)
	if !ok {
		t.Fatal("member was not frozen")
	}
	evidenceID := frozen.ID()

	late := alice.share(t, chain, 1, 3, Prepare, blockC, genesis)
	result, err = ledger.Submit(late)
	requireStatus(t, result, err, StatusFrozen)
	if result.Evidence == nil || result.Evidence.ID() != evidenceID {
		t.Fatal("later conflicting share overwrote frozen evidence")
	}
	again, ok := ledger.FrozenEvidence(alice.member.ID)
	if !ok || again.ID() != evidenceID {
		t.Fatal("stored evidence changed after late share")
	}
	if err := ledger.VerifyEvidence(again); err != nil {
		t.Fatalf("verify frozen evidence: %v", err)
	}
}

func TestDifferentPhasesDoNotConstituteDoubleSign(t *testing.T) {
	alice := newTestSigner(t, "alice", 5)
	ledger, chain := testLedger(t, alice)
	genesis := testDigest("genesis")
	blockA := testDigest("block-A")
	blockB := testDigest("block-B")

	prepare := alice.share(t, chain, 1, 1, Prepare, blockA, genesis)
	commitOther := alice.share(t, chain, 1, 1, Commit, blockB, genesis)
	result, err := ledger.Submit(prepare)
	requireStatus(t, result, err, StatusPublished)
	result, err = ledger.Submit(commitOther)
	requireStatus(t, result, err, StatusPending)
	if ledger.IsFrozen(alice.member.ID) {
		t.Fatal("prepare/commit at the same height was treated as double signing")
	}
}

func TestCommitRequiresMatchingPrepare(t *testing.T) {
	alice := newTestSigner(t, "alice", 6)
	ledger, chain := testLedger(t, alice)
	genesis := testDigest("genesis")
	blockA := testDigest("block-A")

	commit := alice.share(t, chain, 1, 1, Commit, blockA, genesis)
	result, err := ledger.Submit(commit)
	requireStatus(t, result, err, StatusPending)

	prepare := alice.share(t, chain, 1, 1, Prepare, blockA, genesis)
	result, err = ledger.Submit(prepare)
	requireStatus(t, result, err, StatusPublished)
	if len(result.Published) != 2 {
		t.Fatalf("published = %d, want prepare and commit", len(result.Published))
	}
	if result.Published[1].EventID() != commit.EventID() {
		t.Fatal("stashed commit was not released after matching prepare")
	}
}

func TestWrongParentAcrossHeightIsRejected(t *testing.T) {
	alice := newTestSigner(t, "alice", 7)
	ledger, chain := testLedger(t, alice)
	genesis := testDigest("genesis")
	blockA := testDigest("block-A")
	blockB := testDigest("block-B")
	blockC := testDigest("block-C")
	wrong := testDigest("wrong-parent")

	p1 := alice.share(t, chain, 1, 1, Prepare, blockA, genesis)
	c1 := alice.share(t, chain, 1, 1, Commit, blockA, genesis)
	result, err := ledger.SubmitBatch([]Share{p1, c1})
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, result[0], nil, StatusPublished)
	requireStatus(t, result[1], nil, StatusPublished)

	bad := alice.share(t, chain, 2, 2, Prepare, blockB, wrong)
	resultOne, err := ledger.Submit(bad)
	requireStatus(t, resultOne, err, StatusRejected)
	if !errors.Is(resultOne.Err, ErrWrongParent) {
		t.Fatalf("rejection = %v, want ErrWrongParent", resultOne.Err)
	}
	if ledger.IsFrozen(alice.member.ID) {
		t.Fatal("wrong parent alone was treated as double signing")
	}

	// Committing height 1 and then preparing height 3 is legal; heights 2 is
	// allowed to be absent.
	p3 := alice.share(t, chain, 3, 3, Prepare, blockC, blockA)
	resultOne, err = ledger.Submit(p3)
	requireStatus(t, resultOne, err, StatusPublished)
	c3 := alice.share(t, chain, 3, 3, Commit, blockC, blockA)
	resultOne, err = ledger.Submit(c3)
	requireStatus(t, resultOne, err, StatusPublished)
}

func TestRoundRegressionIsRejected(t *testing.T) {
	alice := newTestSigner(t, "alice", 8)
	ledger, chain := testLedger(t, alice)
	genesis := testDigest("genesis")
	blockA := testDigest("block-A")

	prepare := alice.share(t, chain, 1, 2, Prepare, blockA, genesis)
	result, err := ledger.Submit(prepare)
	requireStatus(t, result, err, StatusPublished)

	oldCommit := alice.share(t, chain, 1, 1, Commit, blockA, genesis)
	result, err = ledger.Submit(oldCommit)
	requireStatus(t, result, err, StatusRejected)
	if !errors.Is(result.Err, ErrRoundRegression) {
		t.Fatalf("rejection = %v, want ErrRoundRegression", result.Err)
	}

	validCommit := alice.share(t, chain, 1, 2, Commit, blockA, genesis)
	result, err = ledger.Submit(validCommit)
	requireStatus(t, result, err, StatusPublished)

	// Higher rounds remain legal, including another prepare in a later round.
	nextPrepare := alice.share(t, chain, 1, 3, Prepare, blockA, genesis)
	result, err = ledger.Submit(nextPrepare)
	requireStatus(t, result, err, StatusPublished)
	if ledger.IsFrozen(alice.member.ID) {
		t.Fatal("same-block later round was treated as double signing")
	}
}

func TestNoPrivateKeyCannotForgeEvidence(t *testing.T) {
	victim := newTestSigner(t, "victim", 9)
	attacker := newTestSigner(t, "attacker", 10)
	ledger, chain := testLedger(t, victim, attacker)
	genesis := testDigest("genesis")
	blockA := testDigest("block-A")
	blockB := testDigest("block-B")

	valid := victim.share(t, chain, 1, 1, Prepare, blockA, genesis)
	tampered := valid.clone()
	tampered.Signature[0] ^= 0xff
	result, err := ledger.Submit(tampered)
	requireStatus(t, result, err, StatusRejected)
	if !errors.Is(result.Err, ErrInvalidShare) {
		t.Fatalf("tampered share error = %v, want ErrInvalidShare", result.Err)
	}

	// Attacker signs with their own key but claims to be the victim.
	forgedA := Share{
		ChainID: chain, MemberID: victim.member.ID,
		Height: 1, Round: 1, Phase: Prepare,
		BlockDigest: blockA, ParentDigest: genesis,
	}
	forgedA, err = SignShare(attacker.priv, forgedA)
	if err != nil {
		t.Fatal(err)
	}
	result, err = ledger.Submit(forgedA)
	requireStatus(t, result, err, StatusRejected)

	forgedB := Share{
		ChainID: chain, MemberID: victim.member.ID,
		Height: 1, Round: 2, Phase: Prepare,
		BlockDigest: blockB, ParentDigest: genesis,
	}
	forgedB, err = SignShare(attacker.priv, forgedB)
	if err != nil {
		t.Fatal(err)
	}
	forgedEvidence := DoubleSignEvidence{ShareA: forgedA, ShareB: forgedB}
	if err := VerifyEvidence([]Member{victim.member, attacker.member}, forgedEvidence); err == nil {
		t.Fatal("attacker-forged evidence verified")
	}
	if ledger.IsFrozen(victim.member.ID) {
		t.Fatal("attacker froze the victim")
	}
}

func TestOrdinaryForkOnlyConvictsActualDoubleSigner(t *testing.T) {
	alice := newTestSigner(t, "alice", 11)
	bob := newTestSigner(t, "bob", 12)
	carol := newTestSigner(t, "carol", 13)
	ledger, chain := testLedger(t, alice, bob, carol)
	genesis := testDigest("genesis")
	blockA := testDigest("fork-A")
	blockB := testDigest("fork-B")

	a := alice.share(t, chain, 1, 1, Prepare, blockA, genesis)
	b := bob.share(t, chain, 1, 1, Prepare, blockB, genesis)
	result, err := ledger.Submit(a)
	requireStatus(t, result, err, StatusPublished)
	result, err = ledger.Submit(b)
	requireStatus(t, result, err, StatusPublished)

	for _, signer := range []testSigner{alice, bob, carol} {
		if ledger.IsFrozen(signer.member.ID) {
			t.Fatalf("member %s convicted on an ordinary fork", signer.member.ID)
		}
	}

	// Alice actually double-signs; only she is frozen.
	conflict := alice.share(t, chain, 1, 2, Prepare, blockB, genesis)
	result, err = ledger.Submit(conflict)
	requireStatus(t, result, err, StatusDoubleSigned)
	if !ledger.IsFrozen(alice.member.ID) || ledger.IsFrozen(bob.member.ID) {
		t.Fatal("wrong member was frozen")
	}
}

func TestPersistenceRestartAndDuplicate(t *testing.T) {
	alice := newTestSigner(t, "alice", 14)
	dir := t.TempDir()
	cfg := Config{
		ChainID:       "chain-persist",
		Members:       []Member{alice.member},
		GenesisDigest: testDigest("genesis"),
	}
	genesis := cfg.GenesisDigest
	blockA := testDigest("block-A")
	blockB := testDigest("block-B")

	ledger, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	p1 := alice.share(t, cfg.ChainID, 1, 1, Prepare, blockA, genesis)
	c1 := alice.share(t, cfg.ChainID, 1, 1, Commit, blockA, genesis)
	// Height 2 is absent. A height-3 prepare whose parent is not the height-1
	// commit must wait for a possible height-2 commit.
	pending := alice.share(t, cfg.ChainID, 3, 3, Prepare, blockB, testDigest("block-B"))
	conflict := alice.share(t, cfg.ChainID, 1, 2, Prepare, blockB, genesis)
	result, err := ledger.SubmitBatch([]Share{p1, c1, pending})
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, result[0], nil, StatusPublished)
	requireStatus(t, result[1], nil, StatusPublished)
	requireStatus(t, result[2], nil, StatusPending)
	resultOne, err := ledger.Submit(conflict)
	requireStatus(t, resultOne, err, StatusDoubleSigned)
	evidenceID := resultOne.Evidence.ID()
	duplicate, err := ledger.Submit(conflict)
	requireStatus(t, duplicate, err, StatusDuplicate)
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.IsFrozen(alice.member.ID) {
		t.Fatal("frozen evidence did not survive restart")
	}
	recovered, ok := reopened.FrozenEvidence(alice.member.ID)
	if !ok || recovered.ID() != evidenceID {
		t.Fatal("evidence changed across restart")
	}
	if err := reopened.VerifyEvidence(recovered); err != nil {
		t.Fatalf("recovered evidence does not verify: %v", err)
	}
	if pendingAfter, _ := reopened.Pending(alice.member.ID); len(pendingAfter) != 1 ||
		pendingAfter[0].EventID() != pending.EventID() {
		t.Fatal("pending share did not survive restart")
	}
	index, _ := reopened.SignedIndex(alice.member.ID)
	if len(index) != 2 {
		t.Fatalf("signed index entries = %d, want prepare and commit", len(index))
	}
}

func TestLogReplayRecoversAfterLostSnapshotAndTornTail(t *testing.T) {
	alice := newTestSigner(t, "alice", 15)
	dir := t.TempDir()
	cfg := Config{
		ChainID:       "chain-replay",
		Members:       []Member{alice.member},
		GenesisDigest: testDigest("genesis"),
	}
	genesis := cfg.GenesisDigest
	blockA := testDigest("block-A")
	blockB := testDigest("block-B")

	ledger, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	p1 := alice.share(t, cfg.ChainID, 1, 1, Prepare, blockA, genesis)
	p2 := alice.share(t, cfg.ChainID, 1, 2, Prepare, blockB, genesis)
	result, err := ledger.Submit(p1)
	requireStatus(t, result, err, StatusPublished)
	result, err = ledger.Submit(p2)
	requireStatus(t, result, err, StatusDoubleSigned)
	evidenceID := result.Evidence.ID()
	statePath := ledger.memberStatePath(alice.member.ID)
	logPath := ledger.memberLogPath(alice.member.ID)
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash before the snapshot was durable: the log is the truth.
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	// Simulate a torn record at the tail. Recovery must ignore it.
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 100, '{', '"'}); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, ok := reopened.FrozenEvidence(alice.member.ID)
	if !ok || recovered.ID() != evidenceID {
		t.Fatal("log replay did not recover evidence")
	}
	if err := reopened.VerifyEvidence(recovered); err != nil {
		t.Fatalf("recovered evidence does not verify: %v", err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("log was unexpectedly emptied")
	}

	duplicate, err := reopened.Submit(p2)
	requireStatus(t, duplicate, err, StatusDuplicate)
}

func TestJournaledBatchReplaysAfterCrashBeforeProcessing(t *testing.T) {
	alice := newTestSigner(t, "alice", 24)
	dir := t.TempDir()
	cfg := Config{
		ChainID:       "chain-journal",
		Members:       []Member{alice.member},
		GenesisDigest: testDigest("genesis"),
	}
	genesis := cfg.GenesisDigest
	p1 := alice.share(t, cfg.ChainID, 1, 1, Prepare, testDigest("A"), genesis)
	p2 := alice.share(t, cfg.ChainID, 1, 2, Prepare, testDigest("B"), genesis)

	ledger, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// This is the state immediately after a batch's durable WAL append but
	// before the state machine or snapshot ran. A crash here must still report.
	if err := ledger.journal([]journalEntry{
		{MemberID: alice.member.ID, Share: p1},
		{MemberID: alice.member.ID, Share: p2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	if ledger.IsFrozen(alice.member.ID) {
		t.Fatal("test setup processed the shares before the simulated crash")
	}

	reopened, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	evidence, ok := reopened.FrozenEvidence(alice.member.ID)
	if !ok {
		t.Fatal("journal-only batch did not produce evidence after recovery")
	}
	if err := reopened.VerifyEvidence(evidence); err != nil {
		t.Fatalf("recovered evidence: %v", err)
	}
}

func TestEvidenceJSONRoundTripAndTamper(t *testing.T) {
	alice := newTestSigner(t, "alice", 16)
	ledger, chain := testLedger(t, alice)
	genesis := testDigest("genesis")
	blockA := testDigest("block-A")
	blockB := testDigest("block-B")
	p1 := alice.share(t, chain, 5, 1, Prepare, blockA, genesis)
	p2 := alice.share(t, chain, 5, 2, Prepare, blockB, genesis)
	if _, err := ledger.Submit(p1); err != nil {
		t.Fatal(err)
	}
	result, err := ledger.Submit(p2)
	requireStatus(t, result, err, StatusDoubleSigned)

	encoded, err := json.Marshal(result.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DoubleSignEvidence
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvidence([]Member{alice.member}, decoded); err != nil {
		t.Fatalf("decoded evidence: %v", err)
	}
	if decoded.ID() != result.Evidence.ID() {
		t.Fatal("evidence ID changed across JSON round trip")
	}

	swapped := DoubleSignEvidence{ShareA: decoded.ShareB, ShareB: decoded.ShareA}
	if err := VerifyEvidence([]Member{alice.member}, swapped); err != nil {
		t.Fatalf("swapped evidence should verify: %v", err)
	}
	if swapped.ID() != decoded.ID() {
		t.Fatal("evidence ID depends on share order")
	}

	tampered := decoded
	tampered.ShareB.BlockDigest = testDigest("tampered")
	if err := VerifyEvidence([]Member{alice.member}, tampered); err == nil {
		t.Fatal("tampered evidence verified")
	}
}

func TestConcurrentConflictingSubmissionsFreezeOnce(t *testing.T) {
	alice := newTestSigner(t, "alice", 17)
	ledger, chain := testLedger(t, alice)
	genesis := testDigest("genesis")
	shares := []Share{
		alice.share(t, chain, 9, 1, Prepare, testDigest("A"), genesis),
		alice.share(t, chain, 9, 2, Prepare, testDigest("B"), genesis),
		alice.share(t, chain, 9, 3, Prepare, testDigest("C"), genesis),
	}

	var wg sync.WaitGroup
	results := make([]Result, len(shares))
	errs := make([]error, len(shares))
	for i := range shares {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = ledger.Submit(shares[i])
		}(i)
	}
	wg.Wait()

	doubleCount := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("submit %d: %v", i, errs[i])
		}
		if results[i].Status == StatusDoubleSigned {
			doubleCount++
		}
	}
	if doubleCount != 1 {
		t.Fatalf("double-sign detections = %d, want exactly 1", doubleCount)
	}
	frozen, ok := ledger.FrozenEvidence(alice.member.ID)
	if !ok {
		t.Fatal("member not frozen")
	}
	if err := ledger.VerifyEvidence(frozen); err != nil {
		t.Fatalf("verify evidence: %v", err)
	}
}

func TestReturnedSharesCannotCorruptInternalState(t *testing.T) {
	alice := newTestSigner(t, "alice", 25)
	ledger, chain := testLedger(t, alice)
	genesis := testDigest("genesis")
	p1 := alice.share(t, chain, 1, 1, Prepare, testDigest("A"), genesis)
	p2 := alice.share(t, chain, 1, 2, Prepare, testDigest("B"), genesis)

	result, err := ledger.Submit(p1)
	requireStatus(t, result, err, StatusPublished)
	// Mutating the returned share must not mutate the claim stored internally.
	result.Share.Signature[0] ^= 0xff
	result.Published[0].Signature[0] ^= 0xff

	result, err = ledger.Submit(p2)
	requireStatus(t, result, err, StatusDoubleSigned)
	if err := ledger.VerifyEvidence(*result.Evidence); err != nil {
		t.Fatalf("internal claim was corrupted by returned share: %v", err)
	}
	result.Evidence.ShareA.Signature[0] ^= 0xff
	frozen, ok := ledger.FrozenEvidence(alice.member.ID)
	if !ok {
		t.Fatal("member not frozen")
	}
	if err := ledger.VerifyEvidence(frozen); err != nil {
		t.Fatalf("internal evidence was corrupted by returned evidence: %v", err)
	}
}

func TestRejectBadConfigAndForeignKey(t *testing.T) {
	alice := newTestSigner(t, "alice", 18)
	bob := newTestSigner(t, "bob", 19)
	if _, err := NewLedger(Config{ChainID: "x", Members: []Member{alice.member, alice.member}}); err == nil {
		t.Fatal("duplicate member ID accepted")
	}
	if _, err := NewLedger(Config{ChainID: "x", Members: []Member{
		{ID: "alice", PublicKey: alice.member.PublicKey},
		{ID: "alias", PublicKey: alice.member.PublicKey},
	}}); err == nil {
		t.Fatal("duplicate public key accepted")
	}

	ledger, chain := testLedger(t, alice, bob)
	valid := alice.share(t, chain, 1, 1, Prepare, testDigest("A"), testDigest("genesis"))
	other := valid.clone()
	other.ChainID = "other-chain"
	result, err := ledger.Submit(other)
	requireStatus(t, result, err, StatusRejected)
	if !errors.Is(result.Err, ErrChainMismatch) {
		t.Fatalf("error = %v, want ErrChainMismatch", result.Err)
	}
	unknown := valid.clone()
	unknown.MemberID = "mallory"
	result, err = ledger.Submit(unknown)
	requireStatus(t, result, err, StatusRejected)
	if !errors.Is(result.Err, ErrUnknownMember) {
		t.Fatalf("error = %v, want ErrUnknownMember", result.Err)
	}
}

func TestOpenRejectsChangedFixedCommittee(t *testing.T) {
	alice := newTestSigner(t, "alice", 20)
	bob := newTestSigner(t, "bob", 21)
	dir := t.TempDir()
	cfg := Config{ChainID: "fixed", Members: []Member{alice.member}, GenesisDigest: testDigest("g")}
	ledger, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	changed := Config{ChainID: "fixed", Members: []Member{bob.member}, GenesisDigest: testDigest("g")}
	if _, err := OpenLedger(dir, changed); err == nil || !strings.Contains(err.Error(), "member") {
		t.Fatalf("changed member list error = %v", err)
	}
}

func TestMemberStateFilesArePerMember(t *testing.T) {
	alice := newTestSigner(t, "alice", 22)
	bob := newTestSigner(t, "bob", 23)
	dir := t.TempDir()
	cfg := Config{ChainID: "files", Members: []Member{alice.member, bob.member}, GenesisDigest: testDigest("g")}
	ledger, err := OpenLedger(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	share := alice.share(t, cfg.ChainID, 1, 1, Prepare, testDigest("A"), cfg.GenesisDigest)
	if _, err := ledger.Submit(share); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	for _, signer := range []testSigner{alice, bob} {
		statePath := filepath.Join(dir, "members", memberHash(signer.member.ID), memberHash(signer.member.ID)+".state.json")
		if _, err := os.Stat(statePath); err != nil {
			t.Fatalf("state for %s missing: %v", signer.member.ID, err)
		}
	}
}
