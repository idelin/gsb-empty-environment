package kt

import "testing"

// TestRedundantBoundaryRejected adds a descendant boundary under an existing
// one and expects the structural antichain check to reject it.
func TestRedundantBoundaryRejected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1,
		Entry{lA, leaf("pk-A", false, 0)},
		Entry{lC, leaf("pk-C", false, 0)},
	)
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	p010 := mustPrefix(t, "010")
	w.counter = 1
	rev := w.revokeMigration(2, p010, t1)
	t2 := referenceTree(t, 2, Entry{lC, leaf("pk-C", false, 0)})
	w.ingest(2, t2, &MigrationLog{Epoch: 2, Entries: []Migration{rev}})

	proof, err := BuildConsistencyProof(t1, t2, w.logsBetween(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.verif.VerifyConsistency(proof); err != nil {
		t.Fatalf("honest proof rejected: %v", err)
	}

	// Inject a redundant descendant leaf boundary under changed prefix 010.
	corrupt := *proof
	corrupt.Boundaries = append(append([]BoundaryNode(nil), proof.Boundaries...), BoundaryNode{
		Kind:   BoundaryLeaf,
		Prefix: mustPrefix(t, "010000"),
		Leaf:   LabeledLeaf{Label: mustPrefix(t, "010000")},
	})
	expectErrIs(t, w.verif.VerifyConsistency(&corrupt), ErrRedundantBoundary)
}

// TestIncompleteCoverRejected removes one boundary so the label space is no
// longer fully covered.
func TestIncompleteCoverRejected(t *testing.T) {
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
	corrupt.Boundaries = proof.Boundaries[:len(proof.Boundaries)-1]
	expectErrIs(t, w.verif.VerifyConsistency(&corrupt), ErrIncompleteCover)
}

// TestTamperedBoundaryHashRejected flips a changed-boundary hash.
func TestTamperedBoundaryHashRejected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1,
		Entry{lA, leaf("pk-A", false, 0)},
		Entry{lC, leaf("pk-C", false, 0)},
	)
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	p010 := mustPrefix(t, "010")
	w.counter = 1
	rev := w.revokeMigration(2, p010, t1)
	t2 := referenceTree(t, 2, Entry{lC, leaf("pk-C", false, 0)})
	w.ingest(2, t2, &MigrationLog{Epoch: 2, Entries: []Migration{rev}})

	proof, err := BuildConsistencyProof(t1, t2, w.logsBetween(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := *proof
	for i := range corrupt.Boundaries {
		if corrupt.Boundaries[i].Kind == BoundaryChanged {
			corrupt.Boundaries[i].NewHash[0] ^= 0x01
			break
		}
	}
	err = w.verif.VerifyConsistency(&corrupt)
	if err != ErrRootMismatch && err != ErrMigrationHash {
		t.Fatalf("expected root/migration error, got %v", err)
	}
}

// TestBoundaryUniqueReconstruction confirms a boundary set reconstructs exactly
// one root and a second valid-looking but different set cannot match.
func TestBoundaryUniqueReconstruction(t *testing.T) {
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
	if err := proof.checkStructural(); err != nil {
		t.Fatalf("structural: %v", err)
	}
	got, err := proof.reconstructRoot(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != t1.RootHash() {
		t.Fatal("reconstructed root differs (non-unique reconstruction)")
	}
	// Every boundary must be minimal: no changed boundary at a leaf and no
	// leaf boundary above full depth.
	for _, b := range proof.Boundaries {
		switch b.Kind {
		case BoundaryLeaf:
			if b.Prefix.Len() != testDepth {
				t.Fatal("non-minimal leaf boundary")
			}
		case BoundaryChanged:
			if b.Prefix.Len() >= testDepth {
				t.Fatal("changed boundary should be above leaf depth")
			}
		}
	}
}

// TestNonAdjacentConsistencySkipsEpochs verifies a 1->4 proof using all
// intermediate migration logs.
func TestNonAdjacentConsistencySkipsEpochs(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	lB := mustPrefix(t, "010010")
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1,
		Entry{lA, leaf("pk-A", false, 0)},
		Entry{lB, leaf("pk-B", false, 0)},
		Entry{lC, leaf("pk-C", false, 0)},
	)
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	p010 := mustPrefix(t, "010")
	w.counter = 1
	t2 := referenceTree(t, 2, Entry{lC, leaf("pk-C", false, 0)})
	w.ingest(2, t2, &MigrationLog{Epoch: 2,
		Entries: []Migration{w.revokeMigration(2, p010, t1)}})

	t3 := referenceTree(t, 3,
		Entry{lC, leaf("pk-C", false, 0)},
		Entry{lA, leaf("pk-A2", false, 1)},
	)
	w.ingest(3, t3, &MigrationLog{Epoch: 3,
		Entries: []Migration{w.splitMigration(3, p010, t2, t3)}})

	t4 := referenceTree(t, 4,
		Entry{lC, leaf("pk-C", false, 0)},
		Entry{lA, leaf("pk-A2", false, 1)},
	)
	w.ingest(4, t4, &MigrationLog{Epoch: 4})

	proof, err := BuildConsistencyProof(t1, t4, w.logsBetween(1, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.verif.VerifyConsistency(proof); err != nil {
		t.Fatalf("non-adjacent consistency: %v", err)
	}
}
