package symbolcache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testGUID = [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

func testKey(guid [16]byte, age uint32) Key {
	return Key{FileName: "app.pdb", GUID: guid, Age: age}
}

func newTestCache(t *testing.T, mirrors ...Mirror) (*Cache, string) {
	t.Helper()
	dir := t.TempDir()
	cache, err := New(dir, mirrors...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cache, dir
}

func countObjects(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // temp file removed concurrently during the walk
			}
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if !info.IsDir() && rel != "tmp" && !strings.HasPrefix(rel, "tmp"+string(filepath.Separator)) {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return count
}

func TestGetHappyPathAndCaching(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)
	mirror := &fakeMirror{name: "only", payload: pdb}

	cache, dir := newTestCache(t, mirror)

	h, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer h.Close()
	if h.Size() != int64(len(pdb)) {
		t.Fatalf("size = %d, want %d", h.Size(), len(pdb))
	}

	// ReaderAt must return the exact cached bytes, including at an offset.
	got := make([]byte, len(pdb))
	if _, err := h.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(got) != string(pdb) {
		t.Fatal("cached content differs from mirror payload")
	}
	head := make([]byte, 4)
	if _, err := h.ReadAt(head, 8); err != nil {
		t.Fatalf("ReadAt offset: %v", err)
	}
	if string(head) != string(pdb[8:12]) {
		t.Fatal("offset ReadAt returned wrong bytes")
	}

	// Second Get must be a disk hit: no extra mirror call, file on disk.
	h2, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	_ = h2.Close()
	if mirror.callCount() != 1 {
		t.Fatalf("mirror calls = %d, want 1", mirror.callCount())
	}
	idx, _ := key.index()
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(idx))); err != nil {
		t.Fatalf("published object missing: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(entries) != 0 {
		t.Fatalf("temp dir not clean: %v", entries)
	}
}

func TestGUIDAndAgeMismatchRejected(t *testing.T) {
	key := testKey(testGUID, 7)

	cases := []struct {
		name string
		guid [16]byte
		age  uint32
	}{
		{"wrong age", testGUID, 8},
		{"wrong guid", [16]byte{0xff}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pdb := buildPDB(tc.guid, tc.age, 512, nil)
			mirror := &fakeMirror{payload: pdb}
			cache, dir := newTestCache(t, mirror)

			_, err := cache.Get(context.Background(), key)
			if !errors.Is(err, ErrMismatch) {
				t.Fatalf("err = %v, want ErrMismatch", err)
			}
			if n := countObjects(t, dir); n != 0 {
				t.Fatalf("forged identity polluted cache with %d objects", n)
			}
		})
	}
}

func TestForgedHeaderDoesNotPolluteCache(t *testing.T) {
	key := testKey(testGUID, 7)

	t.Run("bad magic", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) { b[0] = 'X' })
		cache, dir := newTestCache(t, &fakeMirror{payload: pdb})
		_, err := cache.Get(context.Background(), key)
		if !errors.Is(err, ErrNotPDB) {
			t.Fatalf("err = %v, want ErrNotPDB", err)
		}
		if n := countObjects(t, dir); n != 0 {
			t.Fatal("non-PDB bytes were cached")
		}
	})

	t.Run("corrupt stream directory", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) {
			// Directory starts at block 2; smash the stream count.
			b[2*512] = 0xff
		})
		cache, dir := newTestCache(t, &fakeMirror{payload: pdb})
		_, err := cache.Get(context.Background(), key)
		if !errors.Is(err, ErrCorruptPDB) {
			t.Fatalf("err = %v, want ErrCorruptPDB", err)
		}
		if n := countObjects(t, dir); n != 0 {
			t.Fatal("corrupt PDB was cached")
		}
	})

	t.Run("truncated image", func(t *testing.T) {
		full := buildPDB(testGUID, 7, 512, nil)
		cache, dir := newTestCache(t, &fakeMirror{payload: full[:len(full)-64]})
		_, err := cache.Get(context.Background(), key)
		if err == nil {
			t.Fatal("expected error for truncated PDB")
		}
		if n := countObjects(t, dir); n != 0 {
			t.Fatal("truncated PDB was cached")
		}
	})
}

func TestLossyMirrorFallsBack(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)

	bad := &fakeMirror{name: "lossy", payload: pdb, failAfter: 120}
	good := &fakeMirror{name: "good", payload: pdb}
	cache, dir := newTestCache(t, bad, good)

	h, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = h.Close()
	if bad.callCount() != 1 || good.callCount() != 1 {
		t.Fatalf("calls bad=%d good=%d, want 1/1", bad.callCount(), good.callCount())
	}
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("objects = %d, want 1", n)
	}

	// Recovery: a later Get is served from disk without touching either mirror.
	h2, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("recovered Get: %v", err)
	}
	_ = h2.Close()
	if bad.callCount() != 1 || good.callCount() != 1 {
		t.Fatal("cache hit unexpectedly re-contacted mirrors")
	}
}

func TestInterruptedWriteRecovery(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)

	dir := t.TempDir()

	// Simulate a crashed writer: a partial temp file is left on disk.
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "tmp", "fetch-stale.part")
	if err := os.WriteFile(stale, pdb[:80], 0o600); err != nil {
		t.Fatal(err)
	}

	mirror := &fakeMirror{payload: pdb}
	cache, err := New(dir, mirror)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp not swept: %v", err)
	}

	h, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get after recovery: %v", err)
	}
	_ = h.Close()
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("objects = %d, want 1", n)
	}
}

func TestReopenServesPublishedObjects(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)

	dir := t.TempDir()
	first, err := New(dir, &fakeMirror{payload: pdb})
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	h, err := first.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	_ = h.Close()

	// A fresh cache over the same directory must hit disk without a mirror.
	second, err := New(dir)
	if err == nil {
		t.Fatal("New without mirrors should fail even with a populated dir")
	}
	second, err = New(dir, &fakeMirror{payload: nil})
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	h2, err := second.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("reopen Get: %v", err)
	}
	buf := make([]byte, len(pdb))
	if _, err := h2.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf) != string(pdb) {
		t.Fatal("reopened cache returned different bytes")
	}
	_ = h2.Close()
}
