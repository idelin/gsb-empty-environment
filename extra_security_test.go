package kt

import (
	"crypto/ed25519"
	"testing"
)

// TestForgedRootSignatureRejected ensures a tampered signature is refused and
// locks the verifier.
func TestForgedRootSignatureRejected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	log := &MigrationLog{Epoch: 1}
	sr := signRoot(t, w.keys, 1, t1.RootHash(), 0, log.Hash(), Hash{})
	sr.Signature[0] ^= 0x01
	err := w.verif.IngestEpoch(sr, log, nil, t1)
	expectErrIs(t, err, ErrBadSignature)
	if !w.verif.Locked() {
		t.Fatal("forgery must lock the verifier")
	}
}

// TestUntrustedIssuerRejected refuses an authorization from an unknown issuer.
func TestUntrustedIssuerRejected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	_, roguePriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	p010 := mustPrefix(t, "010")
	rogueAuth, err := SignAuth(AuthRecord{
		Epoch: 2, Prefix: p010, Op: OpRevokeOut,
		Nonce: nonce(0x02), Serial: 1,
	}, roguePriv)
	if err != nil {
		t.Fatal(err)
	}
	mig := Migration{
		Epoch:   2,
		Prefix:  p010,
		Op:      OpRevokeOut,
		OldHash: mustSubtree(t, t1, p010),
		Auth:    rogueAuth,
	}
	t2 := referenceTree(t, 2)
	log2 := &MigrationLog{Epoch: 2, Entries: []Migration{mig}}
	sr := signRoot(t, w.keys, 2, t2.RootHash(), 1, log2.Hash(), w.roots[1].Body.Hash())
	expectErrIs(t, w.verif.IngestEpoch(sr, log2, t1, t2), ErrUntrustedIssuer)
}

// TestMergeSiblingRelationshipRejected rejects a merge whose sibling prefix is
// not the real bit-sibling.
func TestMergeSiblingRelationshipRejected(t *testing.T) {
	w := newWorld(t)
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1, Entry{lC, leaf("pk-C", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	t2 := referenceTree(t, 2, Entry{lC, leaf("pk-C", false, 0)})
	bad := w.mergeMigration(2, mustPrefix(t, "110"), t1, t2)
	bad.SiblingPrefix = mustPrefix(t, "100") // not the sibling of 110
	log2 := &MigrationLog{Epoch: 2, Entries: []Migration{bad}}
	sr := signRoot(t, w.keys, 2, t2.RootHash(), 0, log2.Hash(), w.roots[1].Body.Hash())
	expectErrIs(t, w.verif.IngestEpoch(sr, log2, t1, t2), ErrMergeSibling)
}

// TestUnchangedLeafTamperRejected mutates a leaf value carried by an unchanged
// consistency boundary and expects reconstruction/equality failure.
func TestUnchangedLeafTamperRejected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})
	t2 := referenceTree(t, 2, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(2, t2, &MigrationLog{Epoch: 2})

	proof, err := BuildConsistencyProof(t1, t2, w.logsBetween(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := *proof
	for i := range corrupt.Boundaries {
		if corrupt.Boundaries[i].Kind == BoundaryLeaf {
			corrupt.Boundaries[i].Leaf.Value.PublicKey = []byte("tampered")
			break
		}
	}
	err = w.verif.VerifyConsistency(&corrupt)
	if err != ErrRootMismatch && err != ErrLeafChanged {
		t.Fatalf("expected tamper rejection, got %v", err)
	}
}
