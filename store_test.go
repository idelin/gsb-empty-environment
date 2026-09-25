package chunkstore

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

const testBlockSize = 64

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, testBlockSize)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func putBytes(t *testing.T, s *Store, name string, data []byte) {
	t.Helper()
	if err := s.Put(name, bytes.NewReader(data)); err != nil {
		t.Fatalf("Put(%s): %v", name, err)
	}
}

func countBlockFiles(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	root := filepath.Join(dir, "blocks")
	err := filepath.WalkDir(root, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPutGetRoundTrip(t *testing.T) {
	s, _ := openTestStore(t)
	data := randBytes(t, testBlockSize*2+testBlockSize/2) // 2.5 blocks
	putBytes(t, s, "a", data)

	got, err := s.Get("a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(data))
	}
}

func TestEmptyValue(t *testing.T) {
	s, dir := openTestStore(t)
	putBytes(t, s, "empty", nil)

	got, err := s.Get("empty")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty value, got %d bytes", len(got))
	}
	if n := countBlockFiles(t, dir); n != 0 {
		t.Fatalf("empty value wrote %d block files", n)
	}
}

func TestExactBlockSizeBoundaries(t *testing.T) {
	s, _ := openTestStore(t)
	for _, n := range []int{1, testBlockSize - 1, testBlockSize, testBlockSize + 1, 2 * testBlockSize, 3*testBlockSize - 1} {
		name := fmt.Sprintf("v%d", n)
		data := randBytes(t, n)
		putBytes(t, s, name, data)
		got, err := s.Get(name)
		if err != nil {
			t.Fatalf("Get(%s): %v", name, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("size %d: round trip mismatch", n)
		}
	}
}

func TestSharedBlocksSurviveDelete(t *testing.T) {
	s, dir := openTestStore(t)
	shared := randBytes(t, testBlockSize*3)
	putBytes(t, s, "a", append(append([]byte{}, shared...), randBytes(t, testBlockSize)...))
	putBytes(t, s, "b", append(append([]byte{}, shared...), randBytes(t, testBlockSize)...))

	// 3 shared blocks + 1 unique per value = 5 block files, not 8.
	if n := countBlockFiles(t, dir); n != 5 {
		t.Fatalf("expected 5 deduplicated block files, got %d", n)
	}

	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Shared blocks must survive; only a's unique block is reclaimed.
	if n := countBlockFiles(t, dir); n != 4 {
		t.Fatalf("expected 4 block files after delete, got %d", n)
	}
	got, err := s.Get("b")
	if err != nil {
		t.Fatalf("Get(b) after Delete(a): %v", err)
	}
	want := append(append([]byte{}, shared...), got[len(shared):]...)
	if !bytes.Equal(got[:len(shared)], shared) || !bytes.Equal(got, want) {
		t.Fatal("value b corrupted after deleting a")
	}
	if _, err := s.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(a) after delete: %v", err)
	}
}

func TestOverwriteReclaimsOnlyUnreferenced(t *testing.T) {
	s, dir := openTestStore(t)
	shared := randBytes(t, testBlockSize*2)
	oldUnique := randBytes(t, testBlockSize)
	putBytes(t, s, "v", append(append([]byte{}, shared...), oldUnique...))
	putBytes(t, s, "w", append(append([]byte{}, shared...), randBytes(t, testBlockSize)...))

	// Overwrite v with entirely new content; only v's unique block goes.
	putBytes(t, s, "v", randBytes(t, testBlockSize))

	if n := countBlockFiles(t, dir); n != 4 { // 2 shared + w unique + new v
		t.Fatalf("expected 4 block files after overwrite, got %d", n)
	}
	got, err := s.Get("w")
	if err != nil {
		t.Fatalf("Get(w): %v", err)
	}
	if !bytes.Equal(got[:len(shared)], shared) {
		t.Fatal("shared blocks of w lost after overwriting v")
	}
}

func TestFaultMidWriteCleansOrphans(t *testing.T) {
	s, dir := openTestStore(t)
	old := randBytes(t, testBlockSize)
	putBytes(t, s, "v", old)
	before := countBlockFiles(t, dir)

	fj := NewFaultInjector()
	fj.ArmAfter("block.write", 2) // fail on the 3rd block of the next Put
	s.SetFaultInjector(fj)

	data := randBytes(t, testBlockSize * 5)
	err := s.Put("v", bytes.NewReader(data))
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("expected ErrInjected, got %v", err)
	}
	// Rolled back: old value intact, no new block files left behind.
	got, gerr := s.Get("v")
	if gerr != nil || !bytes.Equal(got, old) {
		t.Fatalf("old value not intact after failed Put: %v", gerr)
	}
	if n := countBlockFiles(t, dir); n != before {
		t.Fatalf("orphan blocks leaked: before=%d after=%d", before, n)
	}
	// Store still usable after the injected failure.
	s.SetFaultInjector(nil)
	putBytes(t, s, "v", data)
}

func TestFaultAtMetaCommitKeepsOldState(t *testing.T) {
	s, dir := openTestStore(t)
	old := randBytes(t, testBlockSize)
	putBytes(t, s, "v", old)
	before := countBlockFiles(t, dir)

	fj := NewFaultInjector()
	fj.ArmAfter("meta.commit", 0)
	s.SetFaultInjector(fj)

	err := s.Put("v", bytes.NewReader(randBytes(t, testBlockSize*3)))
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("expected ErrInjected, got %v", err)
	}
	got, gerr := s.Get("v")
	if gerr != nil || !bytes.Equal(got, old) {
		t.Fatalf("old value not intact: %v", gerr)
	}
	if n := countBlockFiles(t, dir); n != before {
		t.Fatalf("new blocks not cleaned after meta.commit failure: before=%d after=%d", before, n)
	}
}

// TestCrashRecovery simulates a process crash (panic at a fault point,
// store never closed cleanly) and reopens from the same directory.
func TestCrashRecovery(t *testing.T) {
	s, dir := openTestStore(t)
	old := randBytes(t, testBlockSize)
	putBytes(t, s, "v", old)

	// Crash after the 2nd block write of the next Put, before meta commit.
	writes := 0
	s.SetPointHook(func(point string) {
		if point == "block.write" {
			writes++
			if writes == 2 {
				panic("simulated crash")
			}
		}
	})
	func() {
		defer func() { _ = recover() }()
		_ = s.Put("v", bytes.NewReader(randBytes(t, testBlockSize*4)))
		t.Fatal("expected panic")
	}()

	// Reopen: committed state must be the old one.
	s2, err := Open(dir, testBlockSize)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := s2.Get("v")
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("committed value lost after crash: %v", err)
	}
	// Blocks written before the crash leaked as orphans (space, not data).
	orphans := countBlockFiles(t, dir)
	if orphans <= 1 {
		t.Fatalf("expected leaked orphan blocks, got %d", orphans)
	}
	// Explicit GC reclaims them; normal writes never scan.
	if err := s2.CollectGarbage(); err != nil {
		t.Fatalf("CollectGarbage: %v", err)
	}
	if n := countBlockFiles(t, dir); n != 1 {
		t.Fatalf("GC left %d block files, want 1", n)
	}
	// Store fully usable after recovery.
	putBytes(t, s2, "v2", randBytes(t, testBlockSize*2))
}

// TestCrashDuringReclaim: crash after meta commit but before zero-ref
// blocks are unlinked. The leak is harmless and GC-able.
func TestCrashDuringReclaim(t *testing.T) {
	s, dir := openTestStore(t)
	putBytes(t, s, "v", randBytes(t, testBlockSize*2))

	s.SetPointHook(func(point string) {
		if point == "reclaim" {
			panic("simulated crash")
		}
	})
	func() {
		defer func() { _ = recover() }()
		_ = s.Delete("v")
		t.Fatal("expected panic")
	}()

	s2, err := Open(dir, testBlockSize)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := s2.Get("v"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete must survive the crash, got %v", err)
	}
	if n := countBlockFiles(t, dir); n != 2 {
		t.Fatalf("expected 2 leaked zero-ref blocks, got %d", n)
	}
	if err := s2.CollectGarbage(); err != nil {
		t.Fatalf("CollectGarbage: %v", err)
	}
	if n := countBlockFiles(t, dir); n != 0 {
		t.Fatalf("GC left %d block files, want 0", n)
	}
}

func TestReadVerifiesBlockDigest(t *testing.T) {
	s, dir := openTestStore(t)
	putBytes(t, s, "v", randBytes(t, testBlockSize*2))

	// Corrupt one block file on disk (bit rot / torn write by an outsider).
	var victim string
	root := filepath.Join(dir, "blocks")
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && victim == "" {
			victim = p
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = s.Get("v")
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
}

func TestNotFound(t *testing.T) {
	s, _ := openTestStore(t)
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get: %v", err)
	}
	if err := s.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete: %v", err)
	}
}

func TestReopenPersistsValues(t *testing.T) {
	s, dir := openTestStore(t)
	data := randBytes(t, testBlockSize*3)
	putBytes(t, s, "v", data)

	s2, err := Open(dir, testBlockSize)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := s2.Get("v")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("value changed across reopen")
	}
}
