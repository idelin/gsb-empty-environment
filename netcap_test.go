package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// scriptedBody is a controllable response body. It can fail mid-stream
// (half-body), block until the request context is canceled, and caps each
// Read so the byte budget advances in small, deterministic steps.
type scriptedBody struct {
	data    []byte
	pos     int
	maxRead int
	failAt  int // return ErrUnexpectedEOF once pos reaches failAt; -1 disables
	blockAt int // block once pos reaches blockAt until ctx fires; -1 disables
	ctx     context.Context
	gate    chan struct{}
	mu      sync.Mutex
	closed  bool
}

func newScriptedBody(ctx context.Context, data []byte) *scriptedBody {
	return &scriptedBody{
		data:    data,
		maxRead: 256,
		failAt:  -1,
		blockAt: -1,
		ctx:     ctx,
		gate:    make(chan struct{}),
	}
}

func (b *scriptedBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, http.ErrBodyReadAfterClose
	}
	if b.pos >= len(b.data) {
		b.mu.Unlock()
		return 0, io.EOF
	}
	if b.blockAt >= 0 && b.pos >= b.blockAt {
		b.mu.Unlock()
		select {
		case <-b.gate:
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
		b.mu.Lock()
	}

	limit := len(p)
	if b.maxRead > 0 && limit > b.maxRead {
		limit = b.maxRead
	}
	remaining := len(b.data) - b.pos
	if limit > remaining {
		limit = remaining
	}
	if b.failAt >= 0 {
		if untilFail := b.failAt - b.pos; untilFail <= 0 {
			b.mu.Unlock()
			return 0, io.ErrUnexpectedEOF
		} else if limit > untilFail {
			limit = untilFail
		}
	}
	n := copy(p[:limit], b.data[b.pos:b.pos+limit])
	b.pos += n
	willFail := b.failAt >= 0 && b.pos >= b.failAt
	b.mu.Unlock()
	if willFail {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func (b *scriptedBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

// fakeTransport records calls and serves scripted responses without any real
// network access.
type fakeTransport struct {
	mu      sync.Mutex
	calls   int
	handler func(*http.Request) (*http.Response, error)
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls++
	handler := f.handler
	f.mu.Unlock()
	return handler(req)
}

func (f *fakeTransport) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func scriptedResponse(status int, body io.ReadCloser) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/octet-stream")
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     h,
		Body:       body,
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newTestCapability(t *testing.T, rt http.RoundTripper, maxBytes int64, attempts int) *Capability {
	t.Helper()
	sources, err := NewSourceSet(
		"https://mirror.example/deps/",
		"https://cdn.example/redirected/",
	)
	if err != nil {
		t.Fatalf("NewSourceSet: %v", err)
	}
	opts := []Option{WithTransport(rt)}
	if attempts > 0 {
		opts = append(opts, WithMaxAttempts(attempts))
	}
	capability, err := New(sources, maxBytes, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return capability
}

// TestHalfBodyRetry verifies that a transfer failing halfway through the body
// is retried, that both attempts are charged to one shared budget, and that
// the verified file is published.
func TestHalfBodyRetry(t *testing.T) {
	payload := bytes.Repeat([]byte("dependency-content-"), 256) // 4864 bytes
	half := len(payload) / 2

	var transport *fakeTransport
	transport = &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			body := newScriptedBody(req.Context(), payload)
			if transport.callCount() == 1 {
				body.failAt = half // first attempt dies after half the bytes
			}
			return scriptedResponse(http.StatusOK, body), nil
		},
	}

	// Budget covers half + full body, nothing more: proves retries share it.
	maxBytes := int64(half + len(payload))
	capability := newTestCapability(t, transport, maxBytes, 3)

	dir := t.TempDir()
	targets := []Target{{
		URL:    "https://mirror.example/deps/lib.tar.gz",
		Name:   "lib.tar.gz",
		SHA256: sha256Hex(payload),
	}}
	if err := capability.Fetch(context.Background(), dir, targets); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := transport.callCount(); got != 2 {
		t.Fatalf("expected 2 attempts (1 failure + 1 retry), got %d", got)
	}
	published, err := os.ReadFile(filepath.Join(dir, "lib.tar.gz"))
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	if !bytes.Equal(published, payload) {
		t.Fatal("published content does not match payload")
	}
	if used := capability.BudgetUsed(); used != maxBytes {
		t.Fatalf("expected shared budget used %d (half+full), got %d", maxBytes, used)
	}
	if remaining := capability.BudgetRemaining(); remaining != 0 {
		t.Fatalf("expected zero remaining budget, got %d", remaining)
	}
}

// TestRetryBudgetExhausted verifies that repeated half-body failures keep
// spending the same budget and stop with ErrBudgetExceeded once it runs out,
// leaving no published file.
func TestRetryBudgetExhausted(t *testing.T) {
	payload := bytes.Repeat([]byte("X"), 4096)
	half := len(payload) / 2

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			body := newScriptedBody(req.Context(), payload)
			body.failAt = half
			return scriptedResponse(http.StatusOK, body), nil
		},
	}
	// One full body would fit, but half+half+... never completes before the
	// shared budget is consumed.
	maxBytes := int64(len(payload) + 128)
	capability := newTestCapability(t, transport, maxBytes, 5)

	dir := t.TempDir()
	targets := []Target{{
		URL:    "https://mirror.example/deps/lib.tar.gz",
		Name:   "lib.tar.gz",
		SHA256: sha256Hex(payload),
	}}
	err := capability.Fetch(context.Background(), dir, targets)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "lib.tar.gz")); !os.IsNotExist(statErr) {
		t.Fatalf("no file may be published on failure, stat err = %v", statErr)
	}
	if remaining := capability.BudgetRemaining(); remaining != 0 {
		t.Fatalf("budget must be fully consumed, remaining = %d", remaining)
	}
	if used := capability.BudgetUsed(); used != maxBytes {
		t.Fatalf("expected saturated used %d, got %d", maxBytes, used)
	}
	// Exactly two half-bodies fit before overflow; the third read trips it.
	if got := transport.callCount(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

// TestWrongDigestIsTerminal verifies that a digest mismatch is not retried,
// consumes exactly one body worth of budget, and publishes nothing.
func TestWrongDigestIsTerminal(t *testing.T) {
	payload := bytes.Repeat([]byte("A"), 1024)

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return scriptedResponse(http.StatusOK,
				newScriptedBody(req.Context(), payload)), nil
		},
	}
	capability := newTestCapability(t, transport, 10_000, 4)

	dir := t.TempDir()
	wrongDigest := sha256Hex([]byte("something-else"))
	targets := []Target{{
		URL:    "https://mirror.example/deps/lib.tar.gz",
		Name:   "lib.tar.gz",
		SHA256: wrongDigest,
	}}
	err := capability.Fetch(context.Background(), dir, targets)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("digest mismatch must be terminal, expected 1 call, got %d", got)
	}
	if used := capability.BudgetUsed(); used != int64(len(payload)) {
		t.Fatalf("expected %d charged bytes, got %d", len(payload), used)
	}
	if remaining := capability.BudgetRemaining(); remaining != 10_000-int64(len(payload)) {
		t.Fatalf("unexpected remaining budget %d", remaining)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "lib.tar.gz")); !os.IsNotExist(statErr) {
		t.Fatal("no file may be published after digest mismatch")
	}
}

// TestCancelPreservesRemainingBudget verifies that cancellation mid-body stops
// the fetch immediately, charges only the bytes actually received, and keeps
// the rest of the shared budget available for a later fetch.
func TestCancelPreservesRemainingBudget(t *testing.T) {
	payload := bytes.Repeat([]byte("C"), 8192)
	const maxBytes int64 = 20_000

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			body := newScriptedBody(req.Context(), payload)
			body.maxRead = 200  // 2000 bytes then block, deterministic accounting
			body.blockAt = 2000 // stall once 2000 bytes have been delivered
			return scriptedResponse(http.StatusOK, body), nil
		},
	}
	capability := newTestCapability(t, transport, maxBytes, 3)

	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	targets := []Target{{
		URL:    "https://mirror.example/deps/lib.tar.gz",
		Name:   "lib.tar.gz",
		SHA256: sha256Hex(payload),
	}}

	done := make(chan error, 1)
	go func() { done <- capability.Fetch(ctx, dir, targets) }()

	// Wait until the body has been charged, then cancel while it is blocked.
	deadline := time.Now().Add(2 * time.Second)
	for capability.BudgetUsed() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Fetch did not stop after cancellation")
	}

	if used := capability.BudgetUsed(); used != 2000 {
		t.Fatalf("expected exactly 2000 charged bytes, got %d", used)
	}
	if remaining := capability.BudgetRemaining(); remaining != maxBytes-2000 {
		t.Fatalf("expected %d remaining, got %d", maxBytes-2000, remaining)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("cancellation must not be retried, got %d calls", got)
	}

	// The remaining budget must still be usable by a later fetch.
	second := []byte("small")
	transport.handler = func(req *http.Request) (*http.Response, error) {
		return scriptedResponse(http.StatusOK,
			newScriptedBody(req.Context(), second)), nil
	}
	if err := capability.Fetch(context.Background(), dir, []Target{{
		URL:    "https://mirror.example/deps/small.txt",
		Name:   "small.txt",
		SHA256: sha256Hex(second),
	}}); err != nil {
		t.Fatalf("follow-up fetch with remaining budget failed: %v", err)
	}
	if used := capability.BudgetUsed(); used != 2000+int64(len(second)) {
		t.Fatalf("expected cumulative used %d, got %d", 2000+len(second), used)
	}
}

type noopBody struct{}

func (noopBody) Read([]byte) (int, error) { return 0, io.EOF }
func (noopBody) Close() error             { return nil }

// TestRedirectStaysInsideSourceSet verifies both that an in-set redirect
// chain is followed and that a redirect leaving the set is rejected before
// the unauthorized host is contacted.
func TestRedirectStaysInsideSourceSet(t *testing.T) {
	payload := []byte("redirected-payload")

	t.Run("allowed redirect", func(t *testing.T) {
		transport := &fakeTransport{
			handler: func(req *http.Request) (*http.Response, error) {
				switch req.URL.String() {
				case "https://mirror.example/deps/a.tgz":
					return &http.Response{
						StatusCode: http.StatusFound,
						Status:     http.StatusText(http.StatusFound),
						Header:     http.Header{"Location": {"https://cdn.example/redirected/a.tgz"}},
						Body:       noopBody{},
					}, nil
				case "https://cdn.example/redirected/a.tgz":
					return scriptedResponse(http.StatusOK,
						newScriptedBody(req.Context(), payload)), nil
				default:
					t.Errorf("unexpected request to %s", req.URL)
					return nil, errors.New("unexpected url")
				}
			},
		}
		capability := newTestCapability(t, transport, 10_000, 2)
		dir := t.TempDir()
		err := capability.Fetch(context.Background(), dir, []Target{{
			URL:    "https://mirror.example/deps/a.tgz",
			Name:   "a.tgz",
			SHA256: sha256Hex(payload),
		}})
		if err != nil {
			t.Fatalf("in-set redirect should succeed: %v", err)
		}
	})

	t.Run("redirect leaves the set", func(t *testing.T) {
		transport := &fakeTransport{
			handler: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusFound,
					Status:     http.StatusText(http.StatusFound),
					Header:     http.Header{"Location": {"https://evil.example/steal"}},
					Body:       noopBody{},
				}, nil
			},
		}
		capability := newTestCapability(t, transport, 10_000, 1)
		dir := t.TempDir()
		err := capability.Fetch(context.Background(), dir, []Target{{
			URL:    "https://mirror.example/deps/a.tgz",
			Name:   "a.tgz",
			SHA256: sha256Hex(payload),
		}})
		if !errors.Is(err, ErrOriginNotAllowed) {
			t.Fatalf("expected ErrOriginNotAllowed, got %v", err)
		}
		if got := transport.callCount(); got != 1 {
			t.Fatalf("unauthorized redirect must not trigger another request, got %d", got)
		}
	})
}

// TestInitialURLNotAllowed verifies that an out-of-set target is rejected
// before any transport call happens.
func TestInitialURLNotAllowed(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			t.Error("transport must never be called for a disallowed URL")
			return nil, errors.New("unreachable")
		},
	}
	capability := newTestCapability(t, transport, 10_000, 1)
	dir := t.TempDir()
	err := capability.Fetch(context.Background(), dir, []Target{{
		URL:    "https://evil.example/deps/lib.tgz",
		Name:   "lib.tgz",
		SHA256: sha256Hex([]byte("x")),
	}})
	if !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("expected ErrOriginNotAllowed, got %v", err)
	}
	if got := transport.callCount(); got != 0 {
		t.Fatalf("expected zero transport calls, got %d", got)
	}
}

// TestAllOrNothingPublish verifies that when one target of a batch fails
// verification, neither it nor its verified sibling is published, and the
// staging directory is removed.
func TestAllOrNothingPublish(t *testing.T) {
	good := []byte("good-content")
	bad := []byte("tampered-content")

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/deps/good.tgz":
				return scriptedResponse(http.StatusOK,
					newScriptedBody(req.Context(), good)), nil
			default:
				return scriptedResponse(http.StatusOK,
					newScriptedBody(req.Context(), bad)), nil
			}
		},
	}
	capability := newTestCapability(t, transport, 10_000, 1)
	dir := t.TempDir()
	err := capability.Fetch(context.Background(), dir, []Target{
		{
			URL:    "https://mirror.example/deps/good.tgz",
			Name:   "good.tgz",
			SHA256: sha256Hex(good),
		},
		{
			URL:    "https://mirror.example/deps/bad.tgz",
			Name:   "bad.tgz",
			SHA256: sha256Hex([]byte("declared-but-different")),
		},
	})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("output dir must be empty after failure, found %v", entries)
	}
}

// TestServerErrorRetry verifies that a retryable 5xx response is retried and
// a later success publishes the file, with the budget still shared.
func TestServerErrorRetry(t *testing.T) {
	payload := []byte("eventually-ok")
	var transport *fakeTransport
	transport = &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if transport.callCount() == 1 {
				return scriptedResponse(http.StatusBadGateway, noopBody{}), nil
			}
			return scriptedResponse(http.StatusOK,
				newScriptedBody(req.Context(), payload)), nil
		},
	}
	capability := newTestCapability(t, transport, 10_000, 3)
	dir := t.TempDir()
	if err := capability.Fetch(context.Background(), dir, []Target{{
		URL:    "https://mirror.example/deps/e.tgz",
		Name:   "e.tgz",
		SHA256: "sha256:" + sha256Hex(payload),
	}}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := transport.callCount(); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
	if used := capability.BudgetUsed(); used != int64(len(payload)) {
		t.Fatalf("5xx body carried no bytes; expected used %d, got %d", len(payload), used)
	}
}

// TestSourceSetMatching covers exact vs directory-prefix matching and the
// rejection of userinfo, query and non-http(s) sources.
func TestSourceSetMatching(t *testing.T) {
	set, err := NewSourceSet(
		"https://mirror.example/deps/",
		"https://mirror.example/exact.txt",
	)
	if err != nil {
		t.Fatalf("NewSourceSet: %v", err)
	}

	mustAllow := func(raw string) {
		t.Helper()
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			t.Fatalf("parse %q: %v", raw, parseErr)
		}
		if err := set.allow(u); err != nil {
			t.Fatalf("expected %q allowed, got %v", raw, err)
		}
	}
	mustDeny := func(raw string) {
		t.Helper()
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			t.Fatalf("parse %q: %v", raw, parseErr)
		}
		if err := set.allow(u); !errors.Is(err, ErrOriginNotAllowed) {
			t.Fatalf("expected %q denied, got %v", raw, err)
		}
	}

	mustAllow("https://mirror.example/deps/lib/a.tgz?token=1")
	mustAllow("https://mirror.example/exact.txt")
	mustDeny("https://mirror.example/other.txt")
	mustDeny("https://evil.example/deps/a.tgz")
	mustDeny("http://mirror.example/deps/a.tgz")
	mustDeny("https://user@mirror.example/deps/a.tgz")

	for _, bad := range []string{
		"ftp://mirror.example/deps/",
		"https://mirror.example/deps/?q=1",
		"https://user:pass@mirror.example/deps/",
		"https://mirror.example/deps/#frag",
		"/relative/path",
	} {
		if err := set.Add(bad); !errors.Is(err, ErrOriginNotAllowed) {
			t.Fatalf("expected Add(%q) rejected, got %v", bad, err)
		}
	}
}

// TestInputValidation verifies malformed targets fail before any network use.
func TestInputValidation(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			t.Error("transport must not be called for invalid input")
			return nil, errors.New("unreachable")
		},
	}
	capability := newTestCapability(t, transport, 10_000, 1)
	dir := t.TempDir()

	if err := capability.Fetch(context.Background(), dir, []Target{{
		URL:    "https://mirror.example/deps/a.tgz",
		Name:   "a.tgz",
		SHA256: "not-a-digest",
	}}); !errors.Is(err, ErrBadDigest) {
		t.Fatalf("expected ErrBadDigest, got %v", err)
	}

	for _, badName := range []string{"../escape", "dir/file", "/abs", ".hidden", ""} {
		err := capability.Fetch(context.Background(), dir, []Target{{
			URL:    "https://mirror.example/deps/a.tgz",
			Name:   badName,
			SHA256: sha256Hex([]byte("x")),
		}})
		if !errors.Is(err, ErrBadTarget) {
			t.Fatalf("name %q: expected ErrBadTarget, got %v", badName, err)
		}
	}

	if got := transport.callCount(); got != 0 {
		t.Fatalf("expected no transport calls, got %d", got)
	}
}

// TestZeroBudgetRejectsAllBytes verifies a zero-byte capability cannot
// receive anything, even on the very first read.
func TestZeroBudgetRejectsAllBytes(t *testing.T) {
	payload := []byte("anything")
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return scriptedResponse(http.StatusOK,
				newScriptedBody(req.Context(), payload)), nil
		},
	}
	capability := newTestCapability(t, transport, 0, 3)
	dir := t.TempDir()
	err := capability.Fetch(context.Background(), dir, []Target{{
		URL:    "https://mirror.example/deps/a.tgz",
		Name:   "a.tgz",
		SHA256: sha256Hex(payload),
	}})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("budget exhaustion is terminal, expected 1 call, got %d", got)
	}
}
