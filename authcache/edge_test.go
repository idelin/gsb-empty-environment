package authcache

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }
func (errReader) Close() error             { return nil }

func TestNormalizePathInvalidEscape(t *testing.T) {
	t.Parallel()

	if _, err := NormalizePath("/a/%zz"); err == nil {
		t.Fatalf("invalid percent escape must return an error")
	}
}

func TestNewHTTPAuthorizerRejectsBadURL(t *testing.T) {
	t.Parallel()

	if _, err := NewHTTPAuthorizer("://bad-url", nil); err == nil {
		t.Fatalf("bad endpoint URL must return an error")
	}
}

func TestAuthorizerFuncAdapter(t *testing.T) {
	t.Parallel()

	calls := 0
	fn := AuthorizerFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return makeResponse(http.StatusOK, http.Header{}, "ok"), nil
	})
	cache := New(Config{Authorizer: fn})

	if _, err := cache.Authorize(newRequest(http.MethodGet, "https://gateway.example/f", "Bearer t")); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", calls)
	}
}

func TestStoreToleratesUnreadableBody(t *testing.T) {
	t.Parallel()

	cache, _, _ := newTestCache(t, func(r *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       errReader{},
		}
	})

	resp, err := cache.Authorize(newRequest(http.MethodGet, "https://gateway.example/broken", "Bearer t"))
	if err != nil {
		t.Fatalf("Authorize must surface the response even when caching fails: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if cache.entryCount() != 0 {
		t.Fatalf("unreadable body must not be cached, entries = %d", cache.entryCount())
	}
}

func TestBaseKeyRejectsEncodedSlash(t *testing.T) {
	t.Parallel()

	cache, _, _ := newTestCache(t, allowResponder(http.Header{}))
	if _, err := cache.BaseKey(newRequest(http.MethodGet, "https://gateway.example/a%2Fb", "Bearer t")); !errors.Is(err, ErrEncodedSlash) {
		t.Fatalf("BaseKey error = %v, want ErrEncodedSlash", err)
	}
}

var _ io.ReadCloser = errReader{}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestNewAppliesDefaultsAndValidates(t *testing.T) {
	t.Parallel()

	cache := New(Config{Authorizer: AuthorizerFunc(func(*http.Request) (*http.Response, error) {
		return makeResponse(http.StatusOK, http.Header{}, ""), nil
	})})
	if cache.sessionCookie != "session" {
		t.Fatalf("default session cookie = %q, want session", cache.sessionCookie)
	}
	before := time.Now()
	if got := cache.now(); got.Before(before) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("default clock does not report wall time")
	}

	defer func() {
		if recover() == nil {
			t.Fatalf("New without an Authorizer must panic")
		}
	}()
	_ = New(Config{})
}

func TestStartCleanupRejectsNonPositiveInterval(t *testing.T) {
	t.Parallel()

	cache, _, _ := newTestCache(t, allowResponder(http.Header{}))
	defer func() {
		if recover() == nil {
			t.Fatalf("non-positive interval must panic")
		}
	}()
	_ = cache.StartCleanup(0)
}

func TestHTTPAuthorizerPropagatesTransportError(t *testing.T) {
	t.Parallel()

	authorizer, err := NewHTTPAuthorizer("https://authz.internal/check", &http.Client{Transport: failingTransport{}})
	if err != nil {
		t.Fatalf("NewHTTPAuthorizer: %v", err)
	}
	cache := New(Config{Authorizer: authorizer})
	if _, err := cache.Authorize(newRequest(http.MethodGet, "https://gateway.example/e", "Bearer t")); err == nil {
		t.Fatalf("transport error must be propagated")
	}
}

func TestRemoveDotSegmentsRelativeAndRepeated(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a/../../b":  "/b",
		"./a/b/":     "a/b/",
		"/a//../b":   "/a/b",
		"/a/b/c/./.": "/a/b/c/",
	}
	for in, want := range cases {
		if got := removeDotSegments(in); got != want {
			t.Fatalf("removeDotSegments(%q) = %q, want %q", in, got, want)
		}
	}
}
