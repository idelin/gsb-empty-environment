package kt

import "testing"

// TestDualAuditorForkDetected builds two conflicting signed views for the same
// epoch and asserts a verifiable fork proof is produced.
func TestDualAuditorForkDetected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	logA := &MigrationLog{Epoch: 2}
	rootA := signRoot(t, w.keys, 2, t1.RootHash(), 0, logA.Hash(), w.roots[1].Body.Hash())

	// Auditor B is shown a different tree root for the same epoch number.
	t1alt := referenceTree(t, 2, Entry{lA, leaf("pk-A-EVIL", false, 0)})
	logB := &MigrationLog{Epoch: 2}
	rootB := signRoot(t, w.keys, 2, t1alt.RootHash(), 0, logB.Hash(), w.roots[1].Body.Hash())

	viewA := &AuditorView{AuditorID: "auditor-A", Signed: rootA, Migrations: logA}
	viewB := &AuditorView{AuditorID: "auditor-B", Signed: rootB, Migrations: logB}

	proof, err := CompareAuditorViews(viewA, viewB, w.keys.dirPub)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if proof == nil {
		t.Fatal("fork not detected")
	}
	if proof.Reason != "tree-root fork" {
		t.Fatalf("reason = %q", proof.Reason)
	}
	if err := proof.Verify(w.keys.dirPub); err != nil {
		t.Fatalf("fork proof not independently verifiable: %v", err)
	}
}

// TestMigrationHistoryForkDetected catches equal tree roots but conflicting
// migration-history commitments.
func TestMigrationHistoryForkDetected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	logA := &MigrationLog{Epoch: 2}
	rootA := signRoot(t, w.keys, 2, t1.RootHash(), 0, logA.Hash(), w.roots[1].Body.Hash())

	// Same tree root, but a non-empty migration log commitment for B.
	p010 := mustPrefix(t, "010")
	auth := signAuth(t, w.keys, 2, p010, OpSplit, t1.RootHash(), nonce(0x07), w.nextSerial())
	logB := &MigrationLog{Epoch: 2, Entries: []Migration{{
		Epoch: 2, Prefix: p010, Op: OpSplit,
		OldHash: mustSubtree(t, t1, p010),
		NewHash: t1.RootHash(),
		Auth:    auth,
	}}}
	rootB := signRoot(t, w.keys, 2, t1.RootHash(), 0, logB.Hash(), w.roots[1].Body.Hash())

	proof, err := CompareAuditorViews(
		&AuditorView{AuditorID: "A", Signed: rootA, Migrations: logA},
		&AuditorView{AuditorID: "B", Signed: rootB, Migrations: logB},
		w.keys.dirPub,
	)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if proof == nil || proof.Reason != "migration-history fork" {
		t.Fatalf("expected migration-history fork, got %+v", proof)
	}
	if err := proof.Verify(w.keys.dirPub); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestConsistentAuditorViewsAgree returns no proof for identical views.
func TestConsistentAuditorViewsAgree(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})
	log := &MigrationLog{Epoch: 2}
	sr := signRoot(t, w.keys, 2, t1.RootHash(), 0, log.Hash(), w.roots[1].Body.Hash())
	proof, err := CompareAuditorViews(
		&AuditorView{AuditorID: "A", Signed: sr, Migrations: log},
		&AuditorView{AuditorID: "B", Signed: sr, Migrations: log},
		w.keys.dirPub,
	)
	if err != nil {
		t.Fatal(err)
	}
	if proof != nil {
		t.Fatal("identical views must not yield a fork proof")
	}
}
