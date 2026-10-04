package snapcatchup

import (
	"errors"
	"testing"
)

// crashStore fails exactly once, on the first Put of failKey, simulating a
// crash at a chosen point of the install boundary.
type crashStore struct {
	Store
	failKey string
	failed  bool
}

func (c *crashStore) Put(key string, data []byte) error {
	if !c.failed && key == c.failKey {
		c.failed = true
		return errors.New("injected crash")
	}
	return c.Store.Put(key, data)
}

func newSnapshottedLeader(t *testing.T, cmds int) (*Leader, SnapshotMeta) {
	t.Helper()
	l := NewLeader(16)
	for i := 0; i < cmds; i++ {
		l.Append("set k" + string(rune('0'+i)) + " v")
	}
	meta := l.TakeSnapshot()
	if meta.NumChunks < 2 {
		t.Fatalf("test needs multiple chunks, got %d", meta.NumChunks)
	}
	return l, meta
}

// Crash after the begin-boundary marker but before the commit: Recover rolls
// the install forward from the still-complete staged chunks.
func TestRecoverRollsForwardInterruptedInstall(t *testing.T) {
	l, meta := newSnapshottedLeader(t, 4)

	store := &crashStore{Store: NewMemStore(), failKey: keyInstalled}
	f := NewFollower(store)
	for seq := 0; seq < meta.NumChunks-1; seq++ {
		sendChunk(t, l, f, meta.Generation, seq)
	}
	// The last chunk triggers install, which "crashes" at the commit write.
	last, err := l.Chunk(meta.Generation, meta.NumChunks-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReceiveChunk(last); err == nil {
		t.Fatal("expected injected crash during install")
	}
	if marker, _ := store.Get(keyInstalling); marker == nil {
		t.Fatal("begin-boundary marker missing after crash")
	}

	// Restart on the same durable store.
	f2 := NewFollower(store)
	if err := f2.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if f2.InstalledGeneration() != 1 || f2.LastIndex() != 4 {
		t.Fatalf("gen=%d lastIndex=%d, want 1/4", f2.InstalledGeneration(), f2.LastIndex())
	}
	if got := f2.StateMachine().Data["k3"]; got != "v" {
		t.Fatalf("k3 = %q, want v (install not rolled forward)", got)
	}
	if marker, _ := store.Get(keyInstalling); marker != nil {
		t.Fatal("install marker not cleaned after recovery")
	}
	if keys, _ := store.List(stagingPrefix); len(keys) != 0 {
		t.Fatalf("staging not cleaned after recovery: %v", keys)
	}
}

// Crash mid-transfer with only partial chunks staged: Recover discards the
// incomplete transfer and a fresh one succeeds.
func TestRecoverDiscardsPartialTransfer(t *testing.T) {
	l, meta := newSnapshottedLeader(t, 4)

	store := NewMemStore()
	f := NewFollower(store)
	sendChunk(t, l, f, meta.Generation, 0) // partial transfer, then "crash"

	f2 := NewFollower(store)
	if err := f2.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if f2.InstalledGeneration() != 0 || f2.LastIndex() != 0 {
		t.Fatalf("gen=%d lastIndex=%d, want 0/0", f2.InstalledGeneration(), f2.LastIndex())
	}
	if keys, _ := store.List(stagingPrefix); len(keys) != 0 {
		t.Fatalf("partial staging not discarded: %v", keys)
	}

	// A fresh transfer on the recovered follower completes.
	transferAll(t, l, f2, meta)
	if got := f2.StateMachine().Data["k3"]; got != "v" {
		t.Fatalf("k3 = %q, want v", got)
	}
}

// Crash after the commit boundary: Recover loads the installed snapshot and
// replay continues from the snapshot index.
func TestRecoverCommittedInstall(t *testing.T) {
	l, meta := newSnapshottedLeader(t, 3)

	store := NewMemStore()
	f := NewFollower(store)
	transferAll(t, l, f, meta)

	// Leader moves on while the follower is "down".
	var newEntries []Entry
	for i := 3; i < 6; i++ {
		newEntries = append(newEntries, l.Append("set n"+string(rune('0'+i))+" v"))
	}

	f2 := NewFollower(store)
	if err := f2.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if f2.InstalledGeneration() != 1 || f2.LastIndex() != 3 {
		t.Fatalf("gen=%d lastIndex=%d, want 1/3", f2.InstalledGeneration(), f2.LastIndex())
	}
	if got := f2.StateMachine().Data["k2"]; got != "v" {
		t.Fatalf("k2 = %q, want v (installed snapshot not restored)", got)
	}

	f2.Replay(l.EntriesAfter(f2.LastIndex()))
	_ = newEntries
	if got := f2.StateMachine().Applied; got != 6 {
		t.Fatalf("applied = %d, want 6", got)
	}
	if got := f2.StateMachine().Data["n5"]; got != "v" {
		t.Fatalf("n5 = %q, want v", got)
	}
}

// End-to-end check of the file-backed store, including restart recovery.
func TestFileStoreDurability(t *testing.T) {
	l, meta := newSnapshottedLeader(t, 3)

	dir := t.TempDir()
	f := NewFollower(NewFileStore(dir))
	transferAll(t, l, f, meta)

	f2 := NewFollower(NewFileStore(dir))
	if err := f2.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if f2.InstalledGeneration() != 1 || f2.LastIndex() != 3 {
		t.Fatalf("gen=%d lastIndex=%d, want 1/3", f2.InstalledGeneration(), f2.LastIndex())
	}
	if got := f2.StateMachine().Data["k0"]; got != "v" {
		t.Fatalf("k0 = %q, want v", got)
	}
}
