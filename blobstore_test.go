package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func refFor(data []byte) string {
	sum := sha256.Sum256(data)
	return "&" + base64.StdEncoding.EncodeToString(sum[:]) + ".sha256"
}

func newStore(t *testing.T, max int64) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "blobs"), max)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return s
}

// requireCleanStore asserts the blob is absent and no temp file leaked.
func requireCleanStore(t *testing.T, s *Store, ref string) {
	t.Helper()
	if s.Has(ref) {
		t.Fatalf("blob %q must not be stored", ref)
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "tmp"))
	if err != nil {
		t.Fatalf("ReadDir tmp: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp dir not empty after failure: %v", entries)
	}
}

func readBlob(t *testing.T, s *Store, ref string) []byte {
	t.Helper()
	rc, err := s.Open(ref)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return data
}

func TestReceiveSuccess(t *testing.T) {
	s := newStore(t, 1<<20)
	data := []byte("hello scuttlebutt")
	ref := refFor(data)

	if err := s.Receive(ref, int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !s.Has(ref) {
		t.Fatal("blob missing after successful receive")
	}
	if got := readBlob(t, s, ref); !bytes.Equal(got, data) {
		t.Fatalf("stored content mismatch: got %q", got)
	}
}

func TestReceiveEmptyBlob(t *testing.T) {
	s := newStore(t, 1<<20)
	ref := refFor(nil)
	if err := s.Receive(ref, 0, bytes.NewReader(nil)); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !s.Has(ref) {
		t.Fatal("empty blob missing after receive")
	}
}

func TestInvalidRefs(t *testing.T) {
	s := newStore(t, 1<<20)
	good := refFor([]byte("x"))
	body := good[1 : 1+44]

	bad := []string{
		"",                                     // empty
		good[1:],                               // missing '&'
		"&" + body,                             // missing suffix
		good + ".extra",                        // trailing garbage
		"&" + body[:43] + ".sha256",            // 43 base64 chars
		"&" + body + "A.sha256",                // 45 base64 chars
		"&" + strings.Repeat("A", 44) + ".sha256", // decodes to 33 bytes, not 32
		"&" + strings.Repeat("A", 43) + "-" + ".sha256", // base64url-only char
		"&" + body[:44] + ".SHA256",            // wrong suffix case
		"%" + body + ".sha256",                 // wrong sigil
	}
	for _, ref := range bad {
		if _, err := ParseRef(ref); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("ParseRef(%q) = %v, want ErrInvalidRef", ref, err)
		}
		if err := s.Receive(ref, 1, strings.NewReader("x")); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("Receive(%q) = %v, want ErrInvalidRef", ref, err)
		}
	}
	if _, err := ParseRef(good); err != nil {
		t.Fatalf("ParseRef(valid) = %v", err)
	}
}

func TestDeclaredSizeExceedsMax(t *testing.T) {
	s := newStore(t, 100)
	data := []byte("small")
	ref := refFor(data)

	// Peer announces more than the caller-configured cap: rejected
	// before reading a single byte.
	if err := s.Receive(ref, 101, failOnReadReader{t}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Receive = %v, want ErrTooLarge", err)
	}
	requireCleanStore(t, s, ref)
}

type failOnReadReader struct{ t *testing.T }

func (f failOnReadReader) Read([]byte) (int, error) {
	f.t.Fatal("reader must not be touched when declared size is rejected")
	return 0, io.EOF
}

func TestUnderDeclaredOverflow(t *testing.T) {
	s := newStore(t, 1<<20)
	data := []byte("attacker sends way more than announced")
	ref := refFor(data)

	// Peer announces 4 bytes but streams the full blob.
	err := s.Receive(ref, 4, bytes.NewReader(data))
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("Receive = %v, want ErrSizeMismatch", err)
	}
	requireCleanStore(t, s, ref)
}

func TestTruncated(t *testing.T) {
	s := newStore(t, 1<<20)
	data := []byte("truncated payload")
	ref := refFor(data)

	// Peer announces the full size but the stream ends early.
	err := s.Receive(ref, int64(len(data)), bytes.NewReader(data[:5]))
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("Receive = %v, want ErrSizeMismatch", err)
	}
	requireCleanStore(t, s, ref)
}

func TestHashMismatch(t *testing.T) {
	s := newStore(t, 1<<20)
	ref := refFor([]byte("expected content"))
	evil := []byte("ATTACKER CONTENT") // same length, different bytes

	err := s.Receive(ref, int64(len(evil)), bytes.NewReader(evil))
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("Receive = %v, want ErrHashMismatch", err)
	}
	requireCleanStore(t, s, ref)
}

// gateReader blocks until released, letting a second receiver attempt
// the same ref while the first is mid-stream.
type gateReader struct {
	data    []byte
	started chan struct{}
	release chan struct{}
	once    sync.Once
	off     int
}

func (g *gateReader) Read(p []byte) (int, error) {
	g.once.Do(func() { close(g.started) })
	<-g.release
	if g.off >= len(g.data) {
		return 0, io.EOF
	}
	n := copy(p, g.data[g.off:])
	g.off += n
	return n, nil
}

func TestConcurrentSameRef(t *testing.T) {
	s := newStore(t, 1<<20)
	data := []byte("first writer wins")
	ref := refFor(data)

	g := &gateReader{
		data:    data,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- s.Receive(ref, int64(len(data)), g)
	}()
	<-g.started

	// A second peer pushing the same ref mid-flight is aborted.
	if err := s.Receive(ref, int64(len(data)), bytes.NewReader(data)); !errors.Is(err, ErrInFlight) {
		t.Fatalf("concurrent Receive = %v, want ErrInFlight", err)
	}

	close(g.release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first Receive: %v", err)
	}
	if got := readBlob(t, s, ref); !bytes.Equal(got, data) {
		t.Fatalf("stored content mismatch: got %q", got)
	}

	// After the first success, re-receiving the same ref is a no-op
	// and must not clobber verified content.
	other := []byte("other content!!!!")
	if err := s.Receive(ref, int64(len(other)), bytes.NewReader(other)); err != nil {
		t.Fatalf("re-Receive after commit = %v, want nil", err)
	}
	if got := readBlob(t, s, ref); !bytes.Equal(got, data) {
		t.Fatalf("content clobbered: got %q", got)
	}
}

func TestConcurrentDistinctRefs(t *testing.T) {
	s := newStore(t, 1<<20)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		data := bytes.Repeat([]byte{byte('a' + i)}, 1024)
		ref := refFor(data)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Receive(ref, int64(len(data)), bytes.NewReader(data))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent distinct Receive: %v", err)
		}
	}
}

func TestNegativeDeclaredSize(t *testing.T) {
	s := newStore(t, 1<<20)
	ref := refFor([]byte("x"))
	if err := s.Receive(ref, -1, strings.NewReader("x")); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("Receive = %v, want ErrSizeMismatch", err)
	}
	requireCleanStore(t, s, ref)
}
