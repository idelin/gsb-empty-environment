package kt

import "testing"

// TestRedundantEmptyBoundaryRejected inserts an empty subtree boundary that
// overlaps an existing changed boundary and expects rejection.
func TestRedundantEmptyBoundaryRejected(t *testing.T) {
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
	// Add a descendant empty boundary under the changed prefix 010.
	corrupt := *proof
	corrupt.Boundaries = append(append([]BoundaryNode(nil), proof.Boundaries...), BoundaryNode{
		Kind:   BoundaryEmpty,
		Prefix: mustPrefix(t, "010111"),
	})
	expectErrIs(t, w.verif.VerifyConsistency(&corrupt), ErrRedundantBoundary)
}

// TestUnknownBoundaryKindRejected ensures the structural check refuses
// fabricated boundary roles.
func TestUnknownBoundaryKindRejected(t *testing.T) {
	p := &ConsistencyProof{
		FromEpoch:  1,
		ToEpoch:    2,
		LabelDepth: testDepth,
		FromRoot:   sum256([]byte("a")),
		ToRoot:     sum256([]byte("b")),
		Boundaries: []BoundaryNode{{
			Kind:   BoundaryKind(99),
			Prefix: mustPrefix(t, "000000"),
		}},
	}
	expectErrIs(t, p.checkStructural(), ErrBoundaryKind)
}
