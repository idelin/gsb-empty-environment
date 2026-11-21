package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var errInjectedDiskFailure = errors.New("injected final-write failure")

func TestLastPersistenceFailureKeepsOldState(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2")

	gen1 := baseManifest(entries(keys, 1, 10), 1, 1)
	if _, err := env.host.Admit(env.signManifest(gen1, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit gen1: %v", err)
	}

	// Fail the final persistence step for the gen-2 commit. The old state must
	// survive and the failed generation must not be partially visible.
	gen2 := baseManifest(entries(keys, 1, 10), 2, 1, "k2")
	env.store.SetSaveFault(func(*State) error { return errInjectedDiskFailure })
	if _, err := env.host.Admit(env.signManifest(gen2, []string{"r1", "r2"})); !errors.Is(err, errInjectedDiskFailure) {
		t.Fatalf("expected injected failure, got %v", err)
	}

	state := mustLoad(t, env.store)
	if state.Generation != 1 {
		t.Fatalf("generation advanced despite failed save: %d", state.Generation)
	}
	if state.Manifest.Generation != 1 || len(state.Manifest.RevokedKeyIDs) != 0 {
		t.Fatalf("old content not preserved after failed save")
	}

	// Once the fault clears, the same gen-2 manifest commits normally.
	env.store.SetSaveFault(nil)
	if _, err := env.host.Admit(env.signManifest(gen2, []string{"r1", "r2"})); err != nil {
		t.Fatalf("retry after fault: %v", err)
	}
	state = mustLoad(t, env.store)
	if state.Generation != 2 || len(state.Manifest.RevokedKeyIDs) != 1 {
		t.Fatalf("retry did not commit gen2 correctly")
	}
}

func TestFileStoreAtomicCommitAndCrashBeforeRename(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNoState) {
		t.Fatalf("expected ErrNoState, got %v", err)
	}

	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2")
	gen1 := baseManifest(entries(keys, 1, 10), 1, 1)
	state1 := acceptState(t, env, gen1)
	if err := store.Save(state1); err != nil {
		t.Fatalf("save gen1: %v", err)
	}

	// Simulate a crash after the temp file is durable but before the final
	// rename: the live file must still expose exactly the gen-1 content.
	gen2 := baseManifest(entries(keys, 1, 10), 2, 1, "k2")
	state2 := acceptState(t, env, gen2)
	store.BeforeRename = func(tmpPath string) error {
		if _, err := os.Stat(tmpPath); err != nil {
			t.Fatalf("temp state missing before rename: %v", err)
		}
		return errInjectedDiskFailure
	}
	if err := store.Save(state2); !errors.Is(err, errInjectedDiskFailure) {
		t.Fatalf("expected injected rename failure, got %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load after crash: %v", err)
	}
	if loaded.Generation != 1 || loaded.Manifest.Generation != 1 {
		t.Fatalf("crash exposed new generation with old content: %d", loaded.Generation)
	}

	// A stale temp file must not poison the next successful commit.
	store.BeforeRename = nil
	if err := store.Save(state2); err != nil {
		t.Fatalf("save gen2 retry: %v", err)
	}
	loaded = mustLoad(t, store)
	if loaded.Generation != 2 || len(loaded.Manifest.RevokedKeyIDs) != 1 {
		t.Fatalf("gen2 not committed correctly after retry")
	}
	if !equalDigests(loaded.Digest, state2.Digest) {
		t.Fatalf("persisted digest disagrees with committed state")
	}
}

func TestFileStoreDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1")
	if err := store.Save(acceptState(t, env, baseManifest(entries(keys, 1, 10), 1, 1))); err != nil {
		t.Fatalf("save: %v", err)
	}

	path := filepath.Join(dir, stateFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	// Flip a byte inside the body (after magic+length header).
	raw[20] ^= 0x01
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("expected ErrCorruptState after tampering, got %v", err)
	}
}

func TestEncodeStateRejectsGenerationContentMismatch(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1")
	state := acceptState(t, env, baseManifest(entries(keys, 1, 10), 1, 1))

	// Forge a state whose header generation disagrees with the manifest body.
	state.Generation = 42
	if _, err := encodeState(state); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("expected ErrCorruptState for generation mismatch, got %v", err)
	}
}

func acceptState(t *testing.T, env *testEnv, m Manifest) *State {
	t.Helper()
	encoded := mustEncode(t, m)
	state, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"}))
	if err != nil {
		t.Fatalf("admit helper: %v", err)
	}
	if !equalDigests(state.Digest, sha256Sum(encoded)) {
		t.Fatalf("helper state digest mismatch")
	}
	return state
}

func equalDigests(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
