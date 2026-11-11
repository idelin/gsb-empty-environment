package revocation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewFileStore(path)

	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}

	want := PersistedState{
		Version:    1,
		State:      StateSynced,
		Generation: 7,
		NextSeq:    42,
		Revoked:    map[string]bool{"alice": true},
		Pending:    map[int64]Event{9: {Generation: 7, Sequence: 9, CredentialID: "bob", Revoked: true}},
		Applied:    Checkpoint{Generation: 7, Sequence: 41},
		Latest:     Checkpoint{Generation: 7, Sequence: 41},
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := store.Load()
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.Generation != want.Generation || got.NextSeq != want.NextSeq ||
		got.Applied != want.Applied || got.Latest != want.Latest ||
		!got.Revoked["alice"] || got.Pending[9].CredentialID != "bob" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// No temporary files may survive an atomic save.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestFileStoreRejectsCorruptState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := NewFileStore(path).Load(); err == nil {
		t.Fatal("corrupt state must surface an error, not a silent reset")
	}
}

func TestRestartForcesRevalidationAgainstCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	first, err := NewSubscriber(NewFileStore(path))
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	mustApplyEvent(t, first, revoke(1, 1, "alice"))
	mustApplyEvent(t, first, revoke(1, 2, "bob"))
	mustApplyCheckpoint(t, first, Checkpoint{Generation: 1, Sequence: 2})
	if !first.CaughtUp() {
		t.Fatal("precondition: first subscriber caught up")
	}

	// Simulate a process restart over the same state file.
	second, err := NewSubscriber(NewFileStore(path))
	if err != nil {
		t.Fatalf("NewSubscriber after restart: %v", err)
	}
	if second.CaughtUp() || second.Synced() {
		t.Fatal("restarted subscriber must not claim caught-up before revalidation")
	}
	if second.Valid("carol") {
		t.Fatal("restarted subscriber must fail closed")
	}

	// The publisher confirms the same checkpoint: no events were missed, so
	// the persisted view becomes trustworthy again without a snapshot.
	mustApplyCheckpoint(t, second, Checkpoint{Generation: 1, Sequence: 2})
	if !second.CaughtUp() {
		t.Fatal("matching checkpoint must restore caught-up status")
	}
	if !second.IsRevoked("alice") || !second.IsRevoked("bob") {
		t.Fatal("revocation set must survive the restart")
	}
	if second.IsRevoked("carol") {
		t.Fatal("unrelated credential must stay valid")
	}
}

func TestRestartWithAdvancedCheckpointStaysStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	first, err := NewSubscriber(NewFileStore(path))
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	mustApplyEvent(t, first, revoke(1, 1, "alice"))
	mustApplyCheckpoint(t, first, Checkpoint{Generation: 1, Sequence: 1})

	second, err := NewSubscriber(NewFileStore(path))
	if err != nil {
		t.Fatalf("NewSubscriber after restart: %v", err)
	}

	// While the process was down the publisher moved to 1:3. The restarted
	// subscriber must not report caught up and must request a snapshot.
	mustApplyCheckpoint(t, second, Checkpoint{Generation: 1, Sequence: 3})
	if second.CaughtUp() {
		t.Fatal("stale restarted subscriber must not report caught up")
	}
	requireSnapshotRequest(t, second, Checkpoint{Generation: 1, Sequence: 1})
}

func TestRestartDuringGapResumesSnapshotRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	first, err := NewSubscriber(NewFileStore(path))
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	mustApplyEvent(t, first, revoke(1, 1, "alice"))
	mustApplyCheckpoint(t, first, Checkpoint{Generation: 1, Sequence: 1})
	mustApplyEvent(t, first, revoke(1, 3, "carol")) // opens gap
	requireSnapshotRequest(t, first, Checkpoint{Generation: 1, Sequence: 1})

	second, err := NewSubscriber(NewFileStore(path))
	if err != nil {
		t.Fatalf("NewSubscriber after restart: %v", err)
	}
	if second.Synced() {
		t.Fatal("gap state must survive restart as denying")
	}
	// The outstanding repair must be re-signaled so a fresh stream can serve
	// it.
	requireSnapshotRequest(t, second, Checkpoint{Generation: 1, Sequence: 1})

	mustApplySnapshot(t, second, Snapshot{
		Generation:   1,
		LastSequence: 3,
		Revoked:      map[string]bool{"alice": true, "carol": true},
	})
	mustApplyCheckpoint(t, second, Checkpoint{Generation: 1, Sequence: 3})
	if !second.CaughtUp() {
		t.Fatal("snapshot after restart must complete the repair")
	}
}
