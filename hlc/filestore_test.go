package hlc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreRoundTripAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "hlc.state")
	store := NewFileStore(path)

	// A never-written store loads as the zero state.
	state, err := store.Load()
	if err != nil {
		t.Fatalf("load missing store: %v", err)
	}
	if state != (State{}) {
		t.Fatalf("missing store = %+v, want zero", state)
	}

	values := []State{
		{Physical: 1731300000123, Logical: 7},
		{Physical: -42, Logical: 65535},
		{Physical: 1<<63 - 1, Logical: 0},
	}
	for _, want := range values {
		if err := store.Save(want); err != nil {
			t.Fatalf("save %+v: %v", want, err)
		}
		got, err := store.Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got != want {
			t.Fatalf("load = %+v, want %+v", got, want)
		}
	}

	// No stray temp files remain after atomic writes.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == "" && e.Name() != "hlc.state" {
			t.Fatalf("unexpected file in state directory: %s", e.Name())
		}
	}
}

func TestFileStoreRejectsTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hlc.state")
	if err := os.WriteFile(path, []byte("HLC1\x00\x01"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path).Load(); err == nil {
		t.Fatal("truncated state file loaded without error")
	}

	bad := filepath.Join(t.TempDir(), "hlc-bad.state")
	if err := os.WriteFile(bad, make([]byte, stateFileSize), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(bad).Load(); err == nil {
		t.Fatal("state file with bad magic loaded without error")
	}
}

// TestFileStoreSurvivesRestart verifies a clock rebuilt on a FileStore keeps
// its persisted lower bound across process-like restarts.
func TestFileStoreSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hlc.state")
	fc := NewFakeClock(1234)

	clk, err := New(Config{NodeID: 55, Physical: fc, Store: NewFileStore(path)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := clk.Now(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	reborn, err := New(Config{NodeID: 55, Physical: NewFakeClock(10), Store: NewFileStore(path)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := reborn.Now(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Before(second) {
		t.Fatalf("restart on FileStore repeated value: %s %s", first, second)
	}
}
