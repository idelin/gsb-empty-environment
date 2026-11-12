package netsec

import (
	"crypto/ed25519"
	"errors"
	"reflect"
	"testing"
)

type testSigner struct {
	id  string
	key ed25519.PrivateKey
	pub ed25519.PublicKey
}

func newTestSigner(t *testing.T, id string) testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return testSigner{id: id, key: priv, pub: pub}
}

func (s testSigner) sign(proof Proof) Proof {
	return SignProof(proof, s.id, s.key)
}

func newTestAuthenticator(signers ...testSigner) *Ed25519Authenticator {
	keys := make(map[string]ed25519.PublicKey, len(signers))
	for _, signer := range signers {
		keys[signer.id] = signer.pub
	}
	return NewEd25519Authenticator(keys)
}

// diamondDAG 构造共享块菱形图：
//
//	branch-a: a1 -> a2 -> X(目标)
//	branch-b: b1 -> b2 -> X(目标)
//
// X 是被两条分支共享的块，是本作业“共享块不能按单分支删除计数”的核心场景。
func diamondDAG() *DAG {
	return &DAG{
		Blocks: map[BlockID]Block{
			"X":  {ID: "X"},
			"a2": {ID: "a2", Parents: []BlockID{"X"}},
			"a1": {ID: "a1", Parents: []BlockID{"a2"}},
			"b2": {ID: "b2", Parents: []BlockID{"X"}},
			"b1": {ID: "b1", Parents: []BlockID{"b2"}},
		},
		Branches: []Branch{
			{Name: "branch-a", Head: "a1"},
			{Name: "branch-b", Head: "b1"},
		},
	}
}

func pathStrings(paths []Path) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, path.String())
	}
	return out
}

func TestSharedBlockBranchScopeDoesNotGloballyDelete(t *testing.T) {
	dag := diamondDAG()
	targets := NewBlockSet("X")
	signer := newTestSigner(t, "backup-host-1")
	auth := newTestAuthenticator(signer)

	verifier, err := NewVerifier(dag, targets, auth)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	// 仅出具 branch-a 的分支作用域删除证明，branch-b 仍可达 X。
	if err := verifier.Add(signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: "branch-a"})); err != nil {
		t.Fatalf("add branch-a proof: %v", err)
	}
	report := verifier.Verify()
	if report.Complete {
		t.Fatal("job must not be complete while branch-b still reaches shared block X")
	}
	wantUncovered := []string{"branch-b: b1->b2->X"}
	if got := pathStrings(report.UncoveredPaths); !reflect.DeepEqual(got, wantUncovered) {
		t.Fatalf("uncovered paths = %v, want %v", got, wantUncovered)
	}
	if !report.CrossCheck.Matched {
		t.Fatalf("cross-check mismatch: %+v", report.CrossCheck)
	}

	// 补齐 branch-b 的分支作用域证明后，两个可达引用都被覆盖。
	if err := verifier.Add(signer.sign(Proof{BlockID: "X", Kind: KindUndecryptable, Scope: "branch-b"})); err != nil {
		t.Fatalf("add branch-b proof: %v", err)
	}
	report = verifier.Verify()
	if !report.Complete {
		t.Fatalf("job should be complete, uncovered = %v", pathStrings(report.UncoveredPaths))
	}
	if report.Stats.ReachablePairs != 2 || report.Stats.UncoveredPairs != 0 {
		t.Fatalf("unexpected stats: %+v", report.Stats)
	}
	if !report.CrossCheck.Matched {
		t.Fatalf("cross-check mismatch after completion: %+v", report.CrossCheck)
	}
}

func TestGlobalProofCoversAllBranches(t *testing.T) {
	dag := diamondDAG()
	targets := NewBlockSet("X")
	signer := newTestSigner(t, "kms-1")
	verifier, err := NewVerifier(dag, targets, newTestAuthenticator(signer))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if err := verifier.Add(signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: GlobalScope})); err != nil {
		t.Fatalf("add global proof: %v", err)
	}
	report := verifier.Verify()
	if !report.Complete {
		t.Fatalf("global proof must cover every branch, uncovered = %v", pathStrings(report.UncoveredPaths))
	}
}

func TestTamperedAndUnknownSignerProofsAreRejected(t *testing.T) {
	dag := diamondDAG()
	targets := NewBlockSet("X")
	signer := newTestSigner(t, "host")
	verifier, err := NewVerifier(dag, targets, newTestAuthenticator(signer))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	tampered := signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: GlobalScope})
	tampered.Signature[0] ^= 0xFF
	if err := verifier.Add(tampered); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("tampered signature error = %v, want ErrProofRejected", err)
	}

	unknown := newTestSigner(t, "rogue")
	if err := verifier.Add(unknown.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: GlobalScope})); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("unknown signer error = %v, want ErrProofRejected", err)
	}

	if err := verifier.Add(Proof{BlockID: "X", Kind: KindDeletion, Scope: GlobalScope, SignerID: "host"}); !errors.Is(err, ErrProofInvalid) {
		t.Fatalf("unsigned proof error = %v, want ErrProofInvalid", err)
	}
	if err := verifier.Add(signer.sign(Proof{BlockID: "ghost", Kind: KindDeletion, Scope: GlobalScope})); !errors.Is(err, ErrProofUnknownBlock) {
		t.Fatalf("unknown block error = %v, want ErrProofUnknownBlock", err)
	}
	if err := verifier.Add(signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: "branch-x"})); !errors.Is(err, ErrProofUnknownBranch) {
		t.Fatalf("unknown branch error = %v, want ErrProofUnknownBranch", err)
	}

	report := verifier.Verify()
	if report.Complete {
		t.Fatal("rejected proofs must not count toward coverage")
	}
	if report.Stats.RejectedProofs != 5 {
		t.Fatalf("rejected count = %d, want 5", report.Stats.RejectedProofs)
	}
	if !report.CrossCheck.Matched {
		t.Fatalf("cross-check mismatch: %+v", report.CrossCheck)
	}
}

func TestConflictingProofsAreRejectedAndIdempotent(t *testing.T) {
	dag := diamondDAG()
	targets := NewBlockSet("X")
	signer := newTestSigner(t, "host")
	verifier, err := NewVerifier(dag, targets, newTestAuthenticator(signer))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	first := signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: "branch-a", Reference: "ticket-1"})
	if err := verifier.Add(first); err != nil {
		t.Fatalf("add first proof: %v", err)
	}
	if err := verifier.Add(first); err != nil {
		t.Fatalf("identical resubmission should be idempotent, got %v", err)
	}
	conflict := signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: "branch-a", Reference: "ticket-2"})
	if err := verifier.Add(conflict); !errors.Is(err, ErrProofConflict) {
		t.Fatalf("conflict error = %v, want ErrProofConflict", err)
	}
	if report := verifier.Verify(); report.Stats.AcceptedProofs != 1 {
		t.Fatalf("accepted proofs = %d, want 1", report.Stats.AcceptedProofs)
	}
}

func TestUnreachableTargetAndIrrelevantProof(t *testing.T) {
	dag := diamondDAG()
	targets := NewBlockSet("X", "orphan")
	dag.Blocks["orphan"] = Block{ID: "orphan"}
	signer := newTestSigner(t, "host")
	verifier, err := NewVerifier(dag, targets, newTestAuthenticator(signer))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	// 非目标块的有效证明不参与覆盖，仅作为无关证明留档。
	if err := verifier.Add(signer.sign(Proof{BlockID: "a2", Kind: KindDeletion, Scope: GlobalScope})); err != nil {
		t.Fatalf("add irrelevant proof: %v", err)
	}
	for _, branch := range []string{"branch-a", "branch-b"} {
		if err := verifier.Add(signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: branch})); err != nil {
			t.Fatalf("add %s proof: %v", branch, err)
		}
	}

	report := verifier.Verify()
	if !report.Complete {
		t.Fatalf("all reachable targets covered, but report not complete: %v", pathStrings(report.UncoveredPaths))
	}
	if !reflect.DeepEqual(report.UnreachableTargets, []BlockID{"orphan"}) {
		t.Fatalf("unreachable targets = %v, want [orphan]", report.UnreachableTargets)
	}
	if report.Stats.IrrelevantProofs != 1 {
		t.Fatalf("irrelevant proofs = %d, want 1", report.Stats.IrrelevantProofs)
	}
}

func TestDAGValidationRejectsCyclesAndMissingParent(t *testing.T) {
	signer := newTestSigner(t, "host")

	cyclic := &DAG{
		Blocks: map[BlockID]Block{
			"a": {ID: "a", Parents: []BlockID{"b"}},
			"b": {ID: "b", Parents: []BlockID{"a"}},
		},
		Branches: []Branch{{Name: "br", Head: "a"}},
	}
	if _, err := NewVerifier(cyclic, NewBlockSet("a"), newTestAuthenticator(signer)); !errors.Is(err, ErrDAGInvalid) {
		t.Fatalf("cycle error = %v, want ErrDAGInvalid", err)
	}

	missing := &DAG{
		Blocks:   map[BlockID]Block{"a": {ID: "a", Parents: []BlockID{"gone"}}},
		Branches: []Branch{{Name: "br", Head: "a"}},
	}
	if _, err := NewVerifier(missing, NewBlockSet("a"), newTestAuthenticator(signer)); !errors.Is(err, ErrDAGInvalid) {
		t.Fatalf("missing parent error = %v, want ErrDAGInvalid", err)
	}
}

func TestEnumeratePathsFindsAllDiamondRoutes(t *testing.T) {
	// 真菱形：两条分支路径在同一块 M 汇合后再到目标 T，同一分支应枚举两条路径。
	dag := &DAG{
		Blocks: map[BlockID]Block{
			"T": {ID: "T"},
			"M": {ID: "M", Parents: []BlockID{"T"}},
			"l": {ID: "l", Parents: []BlockID{"M"}},
			"r": {ID: "r", Parents: []BlockID{"M"}},
			"H": {ID: "H", Parents: []BlockID{"l", "r"}},
		},
		Branches: []Branch{{Name: "br", Head: "H"}},
	}
	paths, err := EnumeratePaths(dag, dag.Branches[0], NewBlockSet("T"))
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	want := []string{"br: H->l->M->T", "br: H->r->M->T"}
	if got := pathStrings(paths); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestFullScanAndIncrementalAgree(t *testing.T) {
	dag := diamondDAG()
	targets := NewBlockSet("X")
	signer := newTestSigner(t, "host")
	auth := newTestAuthenticator(signer)
	proofs := []Proof{
		signer.sign(Proof{BlockID: "X", Kind: KindDeletion, Scope: "branch-a"}),
		signer.sign(Proof{BlockID: "X", Kind: KindUndecryptable, Scope: "branch-b"}),
	}

	verifier, err := NewVerifier(dag, targets, auth)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if err := verifier.AddBatch(proofs); err != nil {
		t.Fatalf("add batch: %v", err)
	}
	incremental := verifier.Verify()
	full, fullErr := FullScan(dag, targets, auth, proofs)
	if fullErr != nil {
		t.Fatalf("full scan: %v", fullErr)
	}
	if !incremental.Complete || !full.Complete {
		t.Fatalf("both should be complete: inc=%v full=%v", incremental.Complete, full.Complete)
	}
	if got := pathStrings(full.UncoveredPaths); len(got) != 0 {
		t.Fatalf("full scan uncovered = %v, want none", got)
	}
}
