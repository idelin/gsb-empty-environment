package netsec

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type failingStore struct {
	base *MemoryStore
	fail bool
}

func (s *failingStore) Load() (TrustedState, error) {
	return s.base.Load()
}

func (s *failingStore) Commit(next TrustedState) error {
	if s.fail {
		return errors.New("injected final persistence failure")
	}
	return s.base.Commit(next)
}

func TestLastPersistenceFailureKeepsOldGeneration(t *testing.T) {
	fixture := newTestFixture()
	store := &failingStore{base: NewMemoryStore()}
	host := fixture.host(store)

	first := fixture.manifest(5)
	fixture.signRoots(&first)
	if _, err := host.AcceptManifestAt(first, testNow); err != nil {
		t.Fatal(err)
	}

	store.fail = true
	second := fixture.manifest(6)
	fixture.signRoots(&second)
	if _, err := host.AcceptManifestAt(second, testNow); !errors.Is(err, ErrStore) {
		t.Fatalf("persistence failure was not reported: %v", err)
	}
	accepted, ok, err := host.CurrentManifest()
	if err != nil || !ok || accepted.Generation != 5 {
		t.Fatalf("failed commit exposed new generation: manifest=%+v accepted=%v err=%v", accepted, ok, err)
	}

	store.fail = false
	if _, err := host.AcceptManifestAt(second, testNow); err != nil {
		t.Fatalf("retry after persistence failure failed: %v", err)
	}
	accepted, ok, err = host.CurrentManifest()
	if err != nil || !ok || accepted.Generation != 6 {
		t.Fatalf("retry did not commit generation 6: manifest=%+v accepted=%v err=%v", accepted, ok, err)
	}
}

func TestFileStoreRecoveryIsCoherent(t *testing.T) {
	fixture := newTestFixture()
	directory := t.TempDir()
	path := filepath.Join(directory, "trusted-state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	host := fixture.host(store)

	first := fixture.manifest(5)
	fixture.signRoots(&first)
	if _, err := host.AcceptManifestAt(first, testNow); err != nil {
		t.Fatal(err)
	}

	// A stale temporary file is what a crash before rename can leave behind.
	stale := filepath.Join(directory, ".trusted-state.json.tmp-stale")
	if err := os.WriteFile(stale, []byte("{truncated"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := fixture.manifest(6)
	fixture.signRoots(&second)
	if _, err := host.AcceptManifestAt(second, testNow); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	recoveredHost := fixture.host(reopened)
	recovered, ok, err := recoveredHost.CurrentManifest()
	if err != nil || !ok {
		t.Fatalf("recovery failed: accepted=%v err=%v", ok, err)
	}
	if recovered.Generation != 6 {
		t.Fatalf("recovered generation %d, want 6", recovered.Generation)
	}
	attestation := fixture.attestation(recovered, "2.0.0", "fw-1", "fw-2")
	if _, err := recoveredHost.AdmitFirmwareAt(attestation, testNow); err != nil {
		t.Fatalf("recovered manifest cannot admit matching firmware: %v", err)
	}

	// Simulate a torn/mixed record: a newer generation label with old content.
	state, err := reopened.Load()
	if err != nil {
		t.Fatal(err)
	}
	mixed := fileStoreRecord{
		Version:    fileStoreVersion,
		Generation: state.Generation + 1,
		Digest:     state.Digest,
		Manifest:   state.Manifest,
	}
	encoded, err := json.Marshal(mixed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Load(); !errors.Is(err, ErrStore) {
		t.Fatalf("mixed generation/content record was accepted: %v", err)
	}
}

func TestFileStoreRejectsMixedCommit(t *testing.T) {
	// This exercises the normal atomic path and confirms the resulting file is
	// a single coherent generation/digest/body record.
	fixture := newTestFixture()
	directory := t.TempDir()
	store, err := NewFileStore(filepath.Join(directory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := fixture.manifest(5)
	canonical, err := manifest.CanonicalEncoding()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(TrustedState{Generation: 5, Digest: digest, Manifest: canonical}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Generation != 5 || loaded.Digest != digest || string(loaded.Manifest) != string(canonical) {
		t.Fatalf("file store returned incoherent state: %+v", loaded)
	}

	bad := TrustedState{Generation: 6, Digest: digest, Manifest: canonical}
	if err := store.Commit(bad); !errors.Is(err, ErrStore) {
		t.Fatalf("store committed generation/content mismatch: %v", err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("failed commit damaged old record: %v", err)
	}
}
