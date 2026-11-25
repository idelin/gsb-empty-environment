package admission

import (
	"errors"
	"os"
	"testing"
)

type admissionFailStore struct {
	state TrustState
	fail  bool
}

func (s *admissionFailStore) Load() (TrustState, error) { return s.state.Clone(), nil }
func (s *admissionFailStore) Save(state TrustState) error {
	if s.fail {
		return errors.New("simulated disk failure")
	}
	s.state = state.Clone()
	return nil
}

func TestImplementationFailedCommitLeavesOldTrustState(t *testing.T) {
	pub, tree := admissionTestPublication(t, 8, 6)
	leaves := admissionTestLeavesWithModule(8, 6, pub.Module)
	old := NewCheckpoint(4, NewTree(leaves[:4]).Root())
	proof, err := tree.ConsistencyProof(4, 8)
	if err != nil {
		t.Fatal(err)
	}
	edges := []EvidenceEdge{NewEvidenceEdge(old, pub.Target, proof, nil)}

	store := &admissionFailStore{state: TrustState{Checkpoints: []Checkpoint{old}}}
	client, err := NewClient(store, 0)
	if err != nil {
		t.Fatal(err)
	}

	store.fail = true
	result, err := client.Admit(pub, edges)
	if err == nil {
		t.Fatal("expected commit failure")
	}
	if result.Status != StatusAdmitted {
		t.Fatalf("evaluation status=%s", result.Status)
	}
	if got := client.Checkpoints(); len(got) != 1 || !got[0].Equal(old) {
		t.Fatalf("in-memory state changed after failed commit: %v", got)
	}
	if got, _ := store.Load(); len(got.Checkpoints) != 1 || !got.Checkpoints[0].Equal(old) {
		t.Fatalf("store state changed after failed commit: %v", got)
	}

	store.fail = false
	result, err = client.Admit(pub, edges)
	if err != nil || result.Status != StatusAdmitted {
		t.Fatalf("retry failed: status=%s err=%v", result.Status, err)
	}
	got := client.Checkpoints()
	if len(got) != 2 || !got[0].Equal(old) || !got[1].Equal(pub.Target) {
		t.Fatalf("accepted set=%v, older root must be retained", got)
	}
}

func TestImplementationFileStoreKeepsOldFileWhenCommitCannotStart(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/trust.json"
	old := NewCheckpoint(4, MerkleRoot(admissionTestLeaves(4)))
	store := NewFileStore(path)
	if err := store.Save(TrustState{Checkpoints: []Checkpoint{old}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(TrustState{Checkpoints: []Checkpoint{old, NewCheckpoint(8, MerkleRoot(admissionTestLeaves(8)))}}); err == nil {
		t.Skip("filesystem did not reject temporary-file creation")
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Checkpoints) != 1 || !after.Checkpoints[0].Equal(loaded.Checkpoints[0]) {
		t.Fatalf("old trust state was not preserved: %v", after)
	}
}
