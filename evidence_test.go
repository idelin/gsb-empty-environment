package admission

import (
	"errors"
	"fmt"
	"testing"
)

func admissionTestPublication(t *testing.T, size int, moduleIndex int) (Publication, *Tree) {
	t.Helper()
	module := Module{
		Path:          "example.com/dependency",
		Version:       "v1.2.3",
		ContentDigest: []byte("content-digest"),
	}
	leaves := admissionTestLeaves(size)
	if moduleIndex >= size {
		t.Fatalf("module index %d out of range for size %d", moduleIndex, size)
	}
	leaves[moduleIndex] = module.LeafData()
	tree := NewTree(leaves)
	proof, err := tree.InclusionProof(uint64(moduleIndex))
	if err != nil {
		t.Fatal(err)
	}
	return Publication{
		Module:         module,
		Target:         NewCheckpoint(uint64(size), tree.Root()),
		LeafIndex:      uint64(moduleIndex),
		InclusionProof: proof,
	}, tree
}

func admissionTestSignatures(cp Checkpoint, threshold int) []WitnessSignature {
	sigs := make([]WitnessSignature, 0, threshold)
	for i := 0; i < threshold; i++ {
		sigs = append(sigs, admissionTestSignedFor(fmt.Sprintf("admin-%d", i), fmt.Sprintf("alias-%d", i), cp))
	}
	return sigs
}

func TestImplementationAdmissionWithOutOfOrderEvidence(t *testing.T) {
	pub, tree := admissionTestPublication(t, 8, 6)
	anchor2 := NewCheckpoint(2, NewTree(admissionTestLeavesWithModule(8, 6, pub.Module)[:2]).Root())
	anchor5 := NewCheckpoint(5, NewTree(admissionTestLeavesWithModule(8, 6, pub.Module)[:5]).Root())

	proof24, err := tree.ConsistencyProof(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	proof48, err := tree.ConsistencyProof(4, 8)
	if err != nil {
		t.Fatal(err)
	}
	proof58, err := tree.ConsistencyProof(5, 8)
	if err != nil {
		t.Fatal(err)
	}

	cp4 := NewCheckpoint(4, NewTree(admissionTestLeavesWithModule(8, 6, pub.Module)[:4]).Root())
	cp8 := pub.Target

	// Deliberately arrive out of order: the target edges are supplied before
	// the intermediate edge.
	edges := []EvidenceEdge{
		NewEvidenceEdge(cp4, cp8, proof48, admissionTestSignatures(cp8, 2)),
		NewEvidenceEdge(anchor5, cp8, proof58, admissionTestSignatures(cp8, 2)),
		NewEvidenceEdge(anchor2, cp4, proof24, admissionTestSignatures(cp4, 2)),
	}

	e := NewEvaluator([]Checkpoint{anchor2, anchor5}, Policy{Threshold: 2})
	result := e.Evaluate(pub, edges)
	if result.Status != StatusAdmitted {
		t.Fatalf("status=%s reason=%s", result.Status, result.Reason)
	}

	// Removing the intermediate edge leaves exactly one anchor disconnected;
	// this is missing evidence, not a rejection.
	result = e.Evaluate(pub, edges[:2])
	if result.Status != StatusUnknown {
		t.Fatalf("missing evidence status=%s want unknown", result.Status)
	}
	if len(result.Missing) != 1 || !result.Missing[0].Equal(anchor2) {
		t.Fatalf("missing anchors=%v", result.Missing)
	}
}

func admissionTestLeavesWithModule(size, moduleIndex int, module Module) [][]byte {
	leaves := admissionTestLeaves(size)
	leaves[moduleIndex] = module.LeafData()
	return leaves
}

func TestImplementationObservedForkAtSameSize(t *testing.T) {
	pub, _ := admissionTestPublication(t, 8, 6)
	otherRoot := append([]byte(nil), pub.Target.Root...)
	otherRoot[0] ^= 0xff
	other := NewCheckpoint(8, otherRoot)

	e := NewEvaluator([]Checkpoint{other}, Policy{Threshold: 0})
	result := e.Evaluate(pub, nil)
	if result.Status != StatusFork {
		t.Fatalf("status=%s want observed fork", result.Status)
	}
	if result.Fork == nil || result.Fork.Size != 8 {
		t.Fatalf("fork=%+v", result.Fork)
	}
}

func TestImplementationAlreadyCommittedCheckpointNeedsNoFreshWitnessForModule(t *testing.T) {
	pub, _ := admissionTestPublication(t, 8, 6)
	e := NewEvaluator([]Checkpoint{pub.Target}, Policy{Threshold: 3})
	result := e.Evaluate(pub, nil)
	if result.Status != StatusAdmitted {
		t.Fatalf("status=%s reason=%s, committed checkpoint should be trusted", result.Status, result.Reason)
	}
}

func TestImplementationClientDoesNotReplaceOlderCheckpoint(t *testing.T) {
	var state TrustState
	older := NewCheckpoint(4, MerkleRoot(admissionTestLeaves(4)))
	if err := state.AddCheckpoint(older); err != nil {
		t.Fatal(err)
	}
	conflicting := NewCheckpoint(8, cloneBytes(older.Root))
	if err := state.AddCheckpoint(conflicting); err != nil {
		t.Fatal(err)
	}
	if !state.Contains(older) {
		t.Fatal("older checkpoint was removed")
	}
	if len(state.Checkpoints) != 2 {
		t.Fatalf("accepted set length=%d want 2", len(state.Checkpoints))
	}

	otherRoot := cloneBytes(older.Root)
	otherRoot[0] ^= 0xff
	if err := state.AddCheckpoint(NewCheckpoint(8, otherRoot)); !errors.Is(err, ErrObservedFork) {
		t.Fatalf("same-size different root error=%v", err)
	}
	if !state.Contains(conflicting) {
		t.Fatal("existing root was replaced by a conflicting one")
	}
}
