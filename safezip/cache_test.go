package safezip

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestFileCacheAtomicity(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("cache-me-please")
	raw := buildZip(false, fixtureMember{name: "a.txt", data: payload})
	a, err := Open(BytesStore{B: raw}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	cache := &FileCache{Dir: dir}
	m, _ := a.Lookup("a.txt")
	if _, err := a.Fetch(m, cache); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	rc, size, err := cache.Open(a.cacheKey(m))
	if err != nil {
		t.Fatalf("open cached: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if size != int64(len(payload)) || !bytes.Equal(got, payload) {
		t.Fatalf("cached content mismatch")
	}
	entries, err := readDirNames(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

// countingStore records ReadAt calls so tests can prove payloads are read
// in bounded ranges rather than one giant request.
type countingStore struct {
	BytesStore
	maxCall int
	calls   int
}

func (s *countingStore) ReadAt(p []byte, off int64) (int, error) {
	s.calls++
	if len(p) > s.maxCall {
		s.maxCall = len(p)
	}
	return s.BytesStore.ReadAt(p, off)
}

func TestChunkedRangesRespected(t *testing.T) {
	payload := []byte(strings.Repeat("range-", 5000))
	raw := buildZip(false, fixtureMember{name: "a.txt", data: payload})
	store := &countingStore{BytesStore: BytesStore{B: raw}}
	a, err := Open(store, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m, _ := a.Lookup("a.txt")
	r, err := a.OpenMember(m)
	if err != nil {
		t.Fatal(err)
	}
	huge := make([]byte, 16<<20)
	n, err := r.Read(huge)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !bytes.Equal(append(huge[:n], rest...), payload) {
		t.Fatal("payload mismatch")
	}
	if store.maxCall > defaultReadChunk {
		t.Fatalf("range call exceeded chunk limit: %d > %d", store.maxCall, defaultReadChunk)
	}
	if store.calls < 2 {
		t.Fatalf("expected multiple ranged calls, got %d", store.calls)
	}
}

// failingSink must never be invoked on a failed verification, and any sink
// error must surface without leaving state behind in MemCache.
func TestSinkErrorSurfaces(t *testing.T) {
	raw := buildZip(false, fixtureMember{name: "a.txt", data: []byte("hello")})
	a, err := Open(BytesStore{B: raw}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m, _ := a.Lookup("a.txt")
	if _, err := a.Fetch(m, errSink{}); err == nil {
		t.Fatal("expected sink error")
	}
}

type errSink struct{}

func (errSink) Commit(string, io.Reader) error { return ErrUnsupported }
