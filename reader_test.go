package netsec_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
	"testing"

	netsec "example.org/netsec/q0500"
)

type testSigner struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func newTestSigner(t *testing.T) *testSigner {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &testSigner{public: public, private: private}
}

func (s *testSigner) sign(head netsec.TreeHead) netsec.SignedTreeHead {
	return netsec.SignedTreeHead{
		Head:      head,
		Signature: ed25519.Sign(s.private, head.SigningBytes()),
	}
}

func (s *testSigner) verify(head netsec.TreeHead, signature []byte) error {
	if !ed25519.Verify(s.public, head.SigningBytes(), signature) {
		return errors.New("bad signature")
	}
	return nil
}

func (s *testSigner) verifier() netsec.SignatureVerifier {
	return s.verify
}

func leaves(prefix string, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(fmt.Sprintf("%s-block-%d", prefix, i))
	}
	return out
}

func headFor(fork uint64, data [][]byte) netsec.TreeHead {
	return netsec.TreeHead{
		Fork:   fork,
		Length: uint64(len(data)),
		Root:   netsec.MerkleRoot(data),
	}
}

func proofFor(t *testing.T, data [][]byte, index uint64) []netsec.Hash {
	t.Helper()
	proof, err := netsec.ProveInclusion(data, index)
	if err != nil {
		t.Fatalf("prove inclusion: %v", err)
	}
	return proof
}

func TestRollbackTreeHeadRejected(t *testing.T) {
	signer := newTestSigner(t)
	reader := netsec.NewReader(signer.verifier())
	data := leaves("main", 4)

	head4 := headFor(0, data)
	if err := reader.Accept(signer.sign(head4)); err != nil {
		t.Fatalf("accept initial head: %v", err)
	}

	// A validly signed older head must not be allowed to truncate history.
	head3 := headFor(0, data[:3])
	err := reader.Accept(signer.sign(head3))
	if !errors.Is(err, netsec.ErrRollback) {
		t.Fatalf("rollback error = %v, want ErrRollback", err)
	}
	var rollback *netsec.RollbackError
	if !errors.As(err, &rollback) {
		t.Fatalf("rollback error type = %T, want *RollbackError", err)
	}
	if rollback.KnownLength != 4 || rollback.ReceivedLength != 3 {
		t.Fatalf("rollback details = %+v", rollback)
	}

	fork, length, root, ok := reader.State()
	if !ok || fork != head4.Fork || length != head4.Length || root != head4.Root {
		t.Fatalf("state changed after rollback: fork=%d length=%d root=%s ok=%v", fork, length, root, ok)
	}
	if reader.Frozen() {
		t.Fatal("length rollback must not freeze the reader")
	}
	if got := reader.Evidence(); got != nil {
		t.Fatalf("rollback produced evidence: %+v", got)
	}

	// Replaying the already verified head is harmless and idempotent.
	if err := reader.Accept(signer.sign(head4)); err != nil {
		t.Fatalf("accept identical head: %v", err)
	}
}

func TestSameLengthDifferentRootFreezesAndRetainsEvidence(t *testing.T) {
	signer := newTestSigner(t)
	reader := netsec.NewReader(signer.verifier())
	originalData := leaves("main", 4)
	alternateData := leaves("alternate", 4)

	original := signer.sign(headFor(0, originalData))
	alternate := signer.sign(headFor(0, alternateData))
	if original.Head.Root == alternate.Head.Root {
		t.Fatal("test setup produced identical roots")
	}

	if err := reader.Accept(original); err != nil {
		t.Fatalf("accept original head: %v", err)
	}
	err := reader.Accept(alternate)
	if !errors.Is(err, netsec.ErrEquivocation) {
		t.Fatalf("equivocation error = %v, want ErrEquivocation", err)
	}
	var equivocation *netsec.EquivocationError
	if !errors.As(err, &equivocation) {
		t.Fatalf("equivocation error type = %T, want *EquivocationError", err)
	}
	if !equivocation.Existing.Head.Equal(original.Head) || !equivocation.Incoming.Head.Equal(alternate.Head) {
		t.Fatalf("equivocation heads = %+v / %+v", equivocation.Existing.Head, equivocation.Incoming.Head)
	}

	if !reader.Frozen() {
		t.Fatal("reader must freeze after same-length equivocation")
	}
	fork, length, root, ok := reader.State()
	if !ok || fork != original.Head.Fork || length != original.Head.Length || root != original.Head.Root {
		t.Fatalf("state changed after equivocation: fork=%d length=%d root=%s ok=%v", fork, length, root, ok)
	}

	evidence := reader.Evidence()
	if len(evidence) != 2 {
		t.Fatalf("evidence length = %d, want 2", len(evidence))
	}
	if !reflect.DeepEqual(evidence[0], original) || !reflect.DeepEqual(evidence[1], alternate) {
		t.Fatalf("evidence does not contain both signed heads: %+v", evidence)
	}
	if err := signer.verify(evidence[0].Head, evidence[0].Signature); err != nil {
		t.Fatalf("first evidence signature invalid: %v", err)
	}
	if err := signer.verify(evidence[1].Head, evidence[1].Signature); err != nil {
		t.Fatalf("second evidence signature invalid: %v", err)
	}

	// Returning evidence must not expose the Reader's internal copies.
	evidence[0].Signature[0] ^= 0xff
	again := reader.Evidence()
	if !reflect.DeepEqual(again[0], original) {
		t.Fatal("mutating returned evidence changed the stored evidence")
	}

	// Freeze is terminal, even for a valid extension of the original branch.
	extension := signer.sign(headFor(0, leaves("main", 5)))
	if err := reader.Accept(extension); !errors.Is(err, netsec.ErrFrozen) {
		t.Fatalf("post-freeze accept error = %v, want ErrFrozen", err)
	}
	if err := reader.AddBlock(0, originalData[0], proofFor(t, originalData, 0)); !errors.Is(err, netsec.ErrFrozen) {
		t.Fatalf("post-freeze block error = %v, want ErrFrozen", err)
	}
}

func TestForkSwitchRequiresSignedForkAndResetsFromZero(t *testing.T) {
	signer := newTestSigner(t)
	reader := netsec.NewReader(signer.verifier())
	oldData := leaves("fork-zero", 4)

	oldHead := headFor(0, oldData)
	if err := reader.Accept(signer.sign(oldHead)); err != nil {
		t.Fatalf("accept fork 0 head: %v", err)
	}
	if err := reader.AddBlock(1, oldData[1], proofFor(t, oldData, 1)); err != nil {
		t.Fatalf("store fork 0 block: %v", err)
	}

	// Relabeling an old signature as a newer fork must fail because the fork
	// number is part of the signed bytes.
	relabelled := netsec.SignedTreeHead{
		Head: netsec.TreeHead{
			Fork:   1,
			Length: oldHead.Length,
			Root:   oldHead.Root,
		},
		Signature: signer.sign(oldHead).Signature,
	}
	if err := reader.Accept(relabelled); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("relabelled fork signature error = %v, want ErrInvalidSignature", err)
	}
	fork, _, _, _ := reader.State()
	if fork != 0 {
		t.Fatalf("state changed after invalid fork signature: fork=%d", fork)
	}

	// A properly signed fork increase is accepted and discards all old-fork
	// verification state, including stored blocks.
	newData := leaves("fork-one", 2)
	newHead := headFor(1, newData)
	if err := reader.Accept(signer.sign(newHead)); err != nil {
		t.Fatalf("accept signed fork 1 head: %v", err)
	}
	fork, length, root, ok := reader.State()
	if !ok || fork != 1 || length != 2 || root != newHead.Root {
		t.Fatalf("fork switch state = fork=%d length=%d root=%s ok=%v", fork, length, root, ok)
	}
	if reader.BlockCount() != 0 {
		t.Fatalf("fork switch retained %d old blocks", reader.BlockCount())
	}
	if _, ok := reader.Block(1); ok {
		t.Fatal("fork switch retained old block 1")
	}

	// Blocks for the new fork are re-established from proofs against its own
	// signed root, starting at index zero.
	if err := reader.AddBlock(0, newData[0], proofFor(t, newData, 0)); err != nil {
		t.Fatalf("store fork 1 block 0: %v", err)
	}
	if err := reader.AddBlock(1, oldData[1], proofFor(t, oldData, 1)); !errors.Is(err, netsec.ErrInclusionProof) {
		t.Fatalf("old-fork block proof error = %v, want ErrInclusionProof", err)
	}

	// Going back to the previous fork is never allowed.
	if err := reader.Accept(signer.sign(oldHead)); !errors.Is(err, netsec.ErrForkRollback) {
		t.Fatalf("fork rollback error = %v, want ErrForkRollback", err)
	}
	fork, _, _, _ = reader.State()
	if fork != 1 {
		t.Fatalf("fork rollback changed state: fork=%d", fork)
	}

	// A later empty fork also resets blocks and installs the RFC 9162 empty
	// root only because the caller signed exactly that head.
	emptyHead := netsec.TreeHead{Fork: 2, Length: 0, Root: netsec.EmptyRoot}
	if err := reader.Accept(signer.sign(emptyHead)); err != nil {
		t.Fatalf("accept empty fork 2 head: %v", err)
	}
	if reader.BlockCount() != 0 {
		t.Fatalf("second fork switch retained %d blocks", reader.BlockCount())
	}
}

func TestBadInclusionProofNeverStoresBlock(t *testing.T) {
	signer := newTestSigner(t)
	reader := netsec.NewReader(signer.verifier())
	data := leaves("proofs", 4)
	head := headFor(0, data)

	if err := reader.Accept(signer.sign(head)); err != nil {
		t.Fatalf("accept head: %v", err)
	}

	valid := proofFor(t, data, 2)
	if err := reader.AddBlock(2, data[2], valid); err != nil {
		t.Fatalf("store valid block: %v", err)
	}
	stored, ok := reader.Block(2)
	if !ok || !bytes.Equal(stored, data[2]) {
		t.Fatalf("stored block = %q, %v", stored, ok)
	}

	badProof := append([]netsec.Hash(nil), valid...)
	badProof[0][0] ^= 0xff
	if err := reader.AddBlock(2, data[2], badProof); !errors.Is(err, netsec.ErrInclusionProof) {
		t.Fatalf("tampered proof error = %v, want ErrInclusionProof", err)
	}

	if err := reader.AddBlock(3, data[3], valid); !errors.Is(err, netsec.ErrInclusionProof) {
		t.Fatalf("wrong-index proof error = %v, want ErrInclusionProof", err)
	}
	if err := reader.AddBlock(2, []byte("wrong body"), valid); !errors.Is(err, netsec.ErrInclusionProof) {
		t.Fatalf("wrong-body proof error = %v, want ErrInclusionProof", err)
	}
	if err := reader.AddBlock(4, []byte("outside"), nil); !errors.Is(err, netsec.ErrBlockRange) {
		t.Fatalf("out-of-range error = %v, want ErrBlockRange", err)
	}

	if got := reader.BlockCount(); got != 1 {
		t.Fatalf("block count = %d, want only the one valid block", got)
	}
	if _, ok := reader.Block(3); ok {
		t.Fatal("failed block 3 was stored")
	}

	// Caller mutation of the submitted body must not mutate stored bytes.
	mutable := append([]byte(nil), data[0]...)
	if err := reader.AddBlock(0, mutable, proofFor(t, data, 0)); err != nil {
		t.Fatalf("store block 0: %v", err)
	}
	mutable[0] = 'X'
	stored, _ = reader.Block(0)
	if !bytes.Equal(stored, data[0]) {
		t.Fatal("stored block aliases the caller's slice")
	}
}

func TestSignatureFailureAndMissingVerifier(t *testing.T) {
	signer := newTestSigner(t)
	reader := netsec.NewReader(signer.verifier())
	data := leaves("signature", 3)
	head := headFor(0, data)

	valid := signer.sign(head)
	valid.Signature[0] ^= 0xff
	if err := reader.Accept(valid); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("bad signature error = %v, want ErrInvalidSignature", err)
	}
	if _, ok := reader.Head(); ok {
		t.Fatal("bad signature installed a head")
	}
	if reader.Frozen() {
		t.Fatal("bad signature froze the reader")
	}

	noVerifier := netsec.NewReader(nil)
	if err := noVerifier.Accept(signer.sign(head)); !errors.Is(err, netsec.ErrNoVerifier) {
		t.Fatalf("nil verifier error = %v, want ErrNoVerifier", err)
	}
}

func TestBlocksRequireVerifiedHead(t *testing.T) {
	reader := netsec.NewReader(newTestSigner(t).verifier())
	if err := reader.AddBlock(0, []byte("block"), nil); !errors.Is(err, netsec.ErrNoVerifiedHead) {
		t.Fatalf("block before head error = %v, want ErrNoVerifiedHead", err)
	}
}
