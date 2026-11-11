package robotstxt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDoer is an in-memory HTTP transport so tests never bind a socket.
type fakeDoer struct {
	calls             atomic.Int64
	status            int
	body              string
	roundErr          error
	redirectsBeforeOK int
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	if f.roundErr != nil {
		return nil, f.roundErr
	}
	n := int(f.calls.Load()) - 1
	if n < f.redirectsBeforeOK {
		return &http.Response{
			StatusCode: http.StatusMovedPermanently,
			Header:     http.Header{"Location": []string{req.URL.String()}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func newClient(f *fakeDoer, ttl time.Duration) *Client {
	return &Client{UserAgent: "testbot/1.0", TTL: ttl, doer: f}
}

const exampleOrigin = "http://example.com"

func TestAllowed2xxDecisions(t *testing.T) {
	f := &fakeDoer{
		status: http.StatusOK,
		body:   "User-agent: *\nDisallow: /private\nAllow: /private/ok\n",
	}
	c := newClient(f, time.Minute)

	tests := []struct {
		path string
		want bool
	}{
		{"/private/data", false},
		{"/private/ok", true},
		{"/public", true},
	}
	for _, tt := range tests {
		got, err := c.Allowed(context.Background(), exampleOrigin+tt.path)
		if err != nil {
			t.Fatalf("Allowed(%q): %v", tt.path, err)
		}
		if got != tt.want {
			t.Fatalf("Allowed(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
	if f.calls.Load() != 1 {
		t.Fatalf("expected a single robots.txt fetch, got %d", f.calls.Load())
	}
}

func Test4xxAllowsAll(t *testing.T) {
	f := &fakeDoer{status: http.StatusNotFound}
	c := newClient(f, time.Minute)

	allowed, err := c.Allowed(context.Background(), exampleOrigin+"/anything")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("4xx must allow all paths per RFC 9309")
	}

	// The allow-all decision is cached like any other result.
	if _, err := c.Allowed(context.Background(), exampleOrigin+"/other"); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("4xx result must be cached, fetches = %d", f.calls.Load())
	}
}

func Test5xxDeniesAllAndCaches(t *testing.T) {
	f := &fakeDoer{status: http.StatusInternalServerError}
	c := newClient(f, time.Minute)

	for i := 0; i < 2; i++ {
		allowed, err := c.Allowed(context.Background(), exampleOrigin+"/anything")
		if err != nil {
			t.Fatal(err)
		}
		if allowed {
			t.Fatal("5xx must deny all paths per RFC 9309")
		}
	}
	if f.calls.Load() != 1 {
		t.Fatalf("5xx result must be cached; fetches = %d, want 1", f.calls.Load())
	}
}

func TestNetworkErrorDeniesAll(t *testing.T) {
	f := &fakeDoer{roundErr: errors.New("connection refused")}
	c := newClient(f, time.Minute)

	allowed, err := c.Allowed(context.Background(), exampleOrigin+"/anything")
	if err == nil {
		t.Fatal("transport error must be returned")
	}
	if allowed {
		t.Fatal("network errors must deny all paths")
	}
}

func TestUnusableStatusDeniesAll(t *testing.T) {
	f := &fakeDoer{status: http.StatusMovedPermanently} // 3xx not followed here
	c := newClient(f, time.Minute)

	allowed, err := c.Allowed(context.Background(), exampleOrigin+"/anything")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("status codes other than 2xx/4xx must deny all paths")
	}
}

func TestBodyTruncatedAt500KiB(t *testing.T) {
	var body bytes.Buffer
	body.WriteString("User-agent: *\n")
	body.WriteString("Disallow: /first\n")
	// Pad beyond 500 KiB so the final rule is cut off while reading.
	body.WriteString(strings.Repeat("x", MaxBodySize))
	body.WriteString("\nDisallow: /after-cut\n")

	f := &fakeDoer{status: http.StatusOK, body: body.String()}
	c := newClient(f, time.Minute)

	if allowed, _ := c.Allowed(context.Background(), exampleOrigin+"/after-cut"); !allowed {
		t.Fatal("rule beyond the 500 KiB cap must not take effect after truncation")
	}
	if allowed, _ := c.Allowed(context.Background(), exampleOrigin+"/first"); allowed {
		t.Fatal("rules inside the first 500 KiB must still apply after truncation")
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	f := &fakeDoer{status: http.StatusOK, body: "User-agent: *\nDisallow: /private\n"}
	now := time.Now()
	c := &Client{
		UserAgent: "testbot",
		TTL:       time.Minute,
		doer:      f,
		now:       func() time.Time { return now },
	}

	if allowed, _ := c.Allowed(context.Background(), exampleOrigin+"/private"); allowed {
		t.Fatal("expected /private to be disallowed")
	}
	if allowed, _ := c.Allowed(context.Background(), exampleOrigin+"/private"); allowed {
		t.Fatal("expected /private to remain disallowed while cached")
	}
	if f.calls.Load() != 1 {
		t.Fatalf("cached entry must avoid refetch, got %d", f.calls.Load())
	}

	now = now.Add(time.Minute + time.Second) // expire the entry
	if allowed, _ := c.Allowed(context.Background(), exampleOrigin+"/private"); allowed {
		t.Fatal("fresh fetch after expiry still disallows /private")
	}
	if f.calls.Load() != 2 {
		t.Fatalf("expired entry must trigger refetch, got %d", f.calls.Load())
	}
}

func TestCacheKeyedByOrigin(t *testing.T) {
	f := &fakeDoer{status: http.StatusOK, body: "User-agent: *\nDisallow: /private\n"}
	c := newClient(f, time.Minute)

	for _, p := range []string{"/a", "/b", "/c?x=1"} {
		if _, err := c.Allowed(context.Background(), exampleOrigin+p); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls.Load() != 1 {
		t.Fatalf("same origin must share one cache entry, got %d", f.calls.Load())
	}
}

func TestNonHTTPURLAllowed(t *testing.T) {
	c := newClient(&fakeDoer{}, time.Minute)
	allowed, err := c.Allowed(context.Background(), "file:///etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("non-HTTP(S) URLs must be allowed")
	}
}

func TestRedirectsFollowedUpToFive(t *testing.T) {
	f := &fakeDoer{
		status:            http.StatusOK,
		body:              "User-agent: *\nDisallow: /private\n",
		redirectsBeforeOK: 3,
	}
	c := &Client{
		UserAgent:  "testbot",
		TTL:        time.Minute,
		HTTPClient: &http.Client{CheckRedirect: defaultRedirectPolicy},
		doer:       nil,
	}
	c.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return f.Do(req)
	})

	allowed, err := c.Allowed(context.Background(), exampleOrigin+"/private")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("robots.txt reached after redirects still disallows /private")
	}
}

func TestMoreThanFiveRedirectsDenies(t *testing.T) {
	f := &fakeDoer{redirectsBeforeOK: 6}
	c := &Client{
		UserAgent:  "testbot",
		TTL:        time.Minute,
		HTTPClient: &http.Client{CheckRedirect: defaultRedirectPolicy},
	}
	c.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return f.Do(req)
	})

	allowed, err := c.Allowed(context.Background(), exampleOrigin+"/anything")
	if err == nil {
		t.Fatal("redirect loop must surface an error")
	}
	if allowed {
		t.Fatal("more than 5 redirects must deny all paths")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
