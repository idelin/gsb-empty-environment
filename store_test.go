package chunkstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const testChunkSize = 64

func openTest(t *testing.T, opts Options) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	if opts.ChunkSize == 0 {
		opts.ChunkSize = testChunkSize
	}
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

// makeData returns n bytes of deterministic pseudo-random data (xorshift32),
// so different seeds produce chunks that never accidentally deduplicate.
func makeData(seed uint32, n int) []byte {
	if seed == 0 {
		seed = 1
	}
	b := make([]byte, n)
	x := seed
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

func countChunkFiles(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	err := filepath.Walk(filepath.Join(dir, "chunks"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) != ".tmp" {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk chunks: %v", err)
	}
	return count
}

func chunkHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func chunkExists(dir, hash string) bool {
	_, err := os.Stat(filepath.Join(dir, "chunks", hash[:2], hash))
	return err == nil
}

func TestPutGetRoundtrip(t *testing.T) {
	s, _ := openTest(t, Options{})
	data := makeData(1, 5*testChunkSize+13) // spans 6 chunks
	if err := s.PutBytes("big", data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get("big")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("roundtrip mismatch: got %d bytes, want %d", len(got), len(data))
	}
}

func TestEmptyValue(t *testing.T) {
	s, dir := openTest(t, Options{})
	if err := s.PutBytes("empty", nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get("empty")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty value: got %d bytes", len(got))
	}
	if n := countChunkFiles(t, dir); n != 0 {
		t.Fatalf("empty value created %d chunk files", n)
	}
}

func TestExactChunkSizeBoundary(t *testing.T) {
	s, dir := openTest(t, Options{})
	for i, size := range []int{testChunkSize, 2 * testChunkSize, 3 * testChunkSize} {
		key := fmt.Sprintf("exact-%d", size)
		data := makeData(uint32(i+1), size)
		if err := s.PutBytes(key, data); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
		got, err := s.Get(key)
		if err != nil {
			t.Fatalf("Get %s: %v", key, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("%s: roundtrip mismatch", key)
		}
	}
	// No spurious trailing empty chunk: exactly 1+2+3 chunk files.
	if n := countChunkFiles(t, dir); n != 6 {
		t.Fatalf("chunk files = %d, want 6", n)
	}
}

func TestSharedChunksSurviveDelete(t *testing.T) {
	s, dir := openTest(t, Options{})
	shared := makeData(1, testChunkSize)
	tail := makeData(2, testChunkSize+10) // distinct second chunk for "b"

	if err := s.PutBytes("a", shared); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	b := append(append([]byte{}, shared...), tail...)
	if err := s.PutBytes("b", b); err != nil {
		t.Fatalf("Put b: %v", err)
	}

	sharedHash := chunkHash(shared)
	if n := countChunkFiles(t, dir); n != 3 {
		t.Fatalf("chunk files = %d, want 3 (dedup of shared chunk)", n)
	}

	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete a: %v", err)
	}
	if !chunkExists(dir, sharedHash) {
		t.Fatal("shared chunk was deleted while still referenced by b")
	}
	got, err := s.Get("b")
	if err != nil {
		t.Fatalf("Get b after Delete a: %v", err)
	}
	if !bytes.Equal(got, b) {
		t.Fatal("b corrupted after deleting a")
	}

	if err := s.Delete("b"); err != nil {
		t.Fatalf("Delete b: %v", err)
	}
	if n := countChunkFiles(t, dir); n != 0 {
		t.Fatalf("chunk files = %d after deleting all keys, want 0", n)
	}
}

func TestOverwriteReclaimsOnlyUnreferenced(t *testing.T) {
	s, dir := openTest(t, Options{})
	old := makeData(1, 2*testChunkSize) // chunks X, Y
	shared := old[:testChunkSize]       // chunk X reused by another key

	if err := s.PutBytes("v", old); err != nil {
		t.Fatalf("Put v: %v", err)
	}
	if err := s.PutBytes("keep", shared); err != nil {
		t.Fatalf("Put keep: %v", err)
	}

	newData := makeData(2, testChunkSize)
	if err := s.PutBytes("v", newData); err != nil {
		t.Fatalf("overwrite v: %v", err)
	}

	xHash := chunkHash(old[:testChunkSize])
	yHash := chunkHash(old[testChunkSize:])
	if !chunkExists(dir, xHash) {
		t.Fatal("overwrite deleted chunk still referenced by another key")
	}
	if chunkExists(dir, yHash) {
		t.Fatal("overwrite did not reclaim unreferenced chunk")
	}
	got, err := s.Get("v")
	if err != nil || !bytes.Equal(got, newData) {
		t.Fatalf("Get overwritten v: err=%v match=%v", err, bytes.Equal(got, newData))
	}
	if got, err := s.Get("keep"); err != nil || !bytes.Equal(got, shared) {
		t.Fatalf("Get keep after overwrite: err=%v", err)
	}
}

func TestInjectedFailureMidPutRollsBackOrphans(t *testing.T) {
	boom := errors.New("injected chunk write failure")
	s, dir := openTest(t, Options{
		Hooks: Hooks{
			AfterChunkWrite: func(n int, hash string) error {
				if n == 2 {
					return boom
				}
				return nil
			},
		},
	})

	// Pre-existing key sharing the first chunk of the failing value.
	first := makeData(1, testChunkSize)
	if err := s.PutBytes("stable", first); err != nil {
		t.Fatalf("Put stable: %v", err)
	}
	keysBefore, chunksBefore := s.Stats()

	// 4 chunks: chunk 1 is shared with "stable", chunks 2-4 are new.
	data := append(append([]byte{}, first...), makeData(2, 3*testChunkSize)...)
	err := s.PutBytes("victim", data)
	if !errors.Is(err, boom) {
		t.Fatalf("Put error = %v, want %v", err, boom)
	}

	// New orphan chunks rolled back; shared chunk untouched.
	if n := countChunkFiles(t, dir); n != 1 {
		t.Fatalf("chunk files after rollback = %d, want 1 (only stable's)", n)
	}
	if !chunkExists(dir, chunkHash(first)) {
		t.Fatal("rollback removed pre-existing shared chunk")
	}
	keys, chunks := s.Stats()
	if keys != keysBefore || chunks != chunksBefore {
		t.Fatalf("metadata changed after failed Put: (%d,%d) -> (%d,%d)",
			keysBefore, chunksBefore, keys, chunks)
	}
	if _, err := s.Get("victim"); err == nil {
		t.Fatal("Get succeeded for key whose Put failed")
	}
	if got, err := s.Get("stable"); err != nil || !bytes.Equal(got, first) {
		t.Fatalf("stable unreadable after rollback: err=%v", err)
	}
}

func TestInjectedFailureBeforeCommitRollsBack(t *testing.T) {
	boom := errors.New("injected commit failure")
	s, dir := openTest(t, Options{
		Hooks: Hooks{BeforeMetaCommit: func() error { return boom }},
	})
	err := s.PutBytes("victim", makeData(1, 3*testChunkSize))
	if !errors.Is(err, boom) {
		t.Fatalf("Put error = %v, want %v", err, boom)
	}
	if n := countChunkFiles(t, dir); n != 0 {
		t.Fatalf("orphan chunks left after commit failure: %d", n)
	}
	if keys, _ := s.Stats(); keys != 0 {
		t.Fatalf("metadata has %d keys after failed Put", keys)
	}
}

func TestCrashRecoveryDiscardsStaleTempMeta(t *testing.T) {
	s, dir := openTest(t, Options{})
	data := makeData(1, 2*testChunkSize)
	if err := s.PutBytes("k", data); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Simulate a crash between temp-file write and rename.
	if err := os.WriteFile(filepath.Join(dir, "meta.json.tmp"), []byte(`{"partial":`), 0o644); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir, Options{ChunkSize: testChunkSize})
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	got, err := s2.Get("k")
	if err != nil {
		t.Fatalf("Get after crash recovery: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("data mismatch after crash recovery")
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("stale temp meta not cleaned up")
	}
}

func TestCorruptChunkDetectedOnRead(t *testing.T) {
	s, dir := openTest(t, Options{})
	data := makeData(1, testChunkSize)
	if err := s.PutBytes("k", data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	h := chunkHash(data)
	path := filepath.Join(dir, "chunks", h[:2], h)
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("k"); err == nil {
		t.Fatal("Get succeeded on corrupted chunk")
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	s, dir := openTest(t, Options{})
	data := makeData(1, 3*testChunkSize+1)
	if err := s.PutBytes("k", data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	s2, err := Open(dir, Options{ChunkSize: testChunkSize})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := s2.Get("k")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("data mismatch after reopen")
	}
}

func TestDeleteMissingKey(t *testing.T) {
	s, _ := openTest(t, Options{})
	if err := s.Delete("nope"); err == nil {
		t.Fatal("Delete of missing key succeeded")
	}
	if _, err := s.Get("nope"); err == nil {
		t.Fatal("Get of missing key succeeded")
	}
}
