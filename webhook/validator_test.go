package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testOrigin = "event-platform.example.com"

const testURL = "https://subscriber.example.com/hook"

// recordedRequest captures what the validator sent.
type recordedRequest struct {
	method string
	header http.Header
	url    *url.URL
}

// memoryTransport answers requests in-process without touching the network,
// which keeps the tests independent of socket permissions and DNS.
type memoryTransport struct {
	t         *testing.T
	handler   func(host string, w *memoryResponseWriter, r *http.Request)
	redirects int
	requests  []recordedRequest
}

type memoryResponseWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (m *memoryResponseWriter) Header() http.Header { return m.header }
func (m *memoryResponseWriter) Write(b []byte) (int, error) {
	if m.status == 0 {
		m.status = http.StatusOK
	}
	return m.body.Write(b)
}
func (m *memoryResponseWriter) WriteHeader(status int) { m.status = status }

func (tr *memoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.requests = append(tr.requests, recordedRequest{
		method: req.Method,
		header: req.Header.Clone(),
		url:    req.URL,
	})
	w := &memoryResponseWriter{header: make(http.Header)}
	tr.handler(req.URL.Hostname(), w, req)
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	resp := &http.Response{
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode: status,
		Header:     w.header,
		Body:       io.NopCloser(strings.NewReader(w.body.String())),
		Request:    req,
	}
	if status >= 300 && status < 400 {
		loc := w.header.Get("Location")
		if loc != "" {
			resp.Header.Set("Location", loc)
		}
	}
	return resp, nil
}

// newTestValidator wires a validator to the in-memory transport and a DNS
// seam that resolves every name to a public documentation IP. Production code
// would use the real resolver; this seam only exists for tests.
func newTestValidator(t *testing.T, tr *memoryTransport, ttl time.Duration, extra func(*Options)) *Validator {
	t.Helper()
	opts := Options{
		Origin:      testOrigin,
		RequestRate: 100,
		TTL:         ttl,
		HTTPClient:  &http.Client{Transport: tr},
		LookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.0.2.10")}, nil
		},
	}
	if extra != nil {
		extra(&opts)
	}
	v, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func allowHandler(origin, rate string) func(string, *memoryResponseWriter, *http.Request) {
	return func(host string, w *memoryResponseWriter, r *http.Request) {
		if origin != "" {
			w.Header().Set(HeaderAllowedOrigin, origin)
		}
		if rate != "" {
			w.Header().Set(HeaderAllowedRate, rate)
		}
		w.WriteHeader(http.StatusOK)
	}
}

func TestRegisterSuccessAndHeaders(t *testing.T) {
	tr := &memoryTransport{handler: allowHandler(testOrigin, "50")}
	v := newTestValidator(t, tr, 0, nil)

	sub, err := v.Register(context.Background(), testURL)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(tr.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(tr.requests))
	}
	req := tr.requests[0]
	if req.method != http.MethodOptions {
		t.Errorf("method = %q, want OPTIONS", req.method)
	}
	if got := req.header.Get(HeaderRequestOrigin); got != testOrigin {
		t.Errorf("WebHook-Request-Origin = %q, want %q", got, testOrigin)
	}
	if got := req.header.Get(HeaderRequestRate); got != "100" {
		t.Errorf("WebHook-Request-Rate = %q, want 100", got)
	}
	if sub.AllowedRate != 50 {
		t.Errorf("AllowedRate = %d, want 50", sub.AllowedRate)
	}
	if !sub.ExpiresAt.Equal(sub.VerifiedAt.Add(DefaultTTL)) {
		t.Errorf("expiry = %v, want verified + 24h", sub.ExpiresAt)
	}
	if _, err := v.AllowDelivery(testURL); err != nil {
		t.Errorf("AllowDelivery after register: %v", err)
	}
}

func TestMissingAllowedOriginHeader(t *testing.T) {
	tr := &memoryTransport{handler: allowHandler("", "50")}
	v := newTestValidator(t, tr, 0, nil)

	_, err := v.Register(context.Background(), testURL)
	if !errors.Is(err, ErrMissingAllowedOrigin) {
		t.Fatalf("err = %v, want ErrMissingAllowedOrigin", err)
	}
}

func TestMissingAllowedRateHeader(t *testing.T) {
	tr := &memoryTransport{handler: allowHandler(testOrigin, "")}
	v := newTestValidator(t, tr, 0, nil)

	_, err := v.Register(context.Background(), testURL)
	if !errors.Is(err, ErrMissingAllowedRate) {
		t.Fatalf("err = %v, want ErrMissingAllowedRate", err)
	}
}

func TestAllowedOriginRejectsForeignOrigin(t *testing.T) {
	tr := &memoryTransport{handler: allowHandler("evil.example.net", "50")}
	v := newTestValidator(t, tr, 0, nil)

	_, err := v.Register(context.Background(), testURL)
	if !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("err = %v, want ErrOriginNotAllowed", err)
	}
}

func TestAllowedOriginWildcardAccepted(t *testing.T) {
	tr := &memoryTransport{handler: allowHandler(Wildcard, Wildcard)}
	v := newTestValidator(t, tr, 0, nil)

	sub, err := v.Register(context.Background(), testURL)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if sub.AllowedRate != 0 {
		t.Errorf("AllowedRate = %d, want 0 for wildcard", sub.AllowedRate)
	}
}

func TestRateTakesSmallerValue(t *testing.T) {
	cases := []struct {
		name        string
		requestRate int
		allowed     string
		want        int
		wantErr     error
	}{
		{"subscriber lower", 100, "40", 40, nil},
		{"producer lower", 30, "90", 30, nil},
		{"equal", 25, "25", 25, nil},
		{"wildcard uses producer", 30, "*", 0, nil},
		{"zero rejected", 100, "0", 0, ErrInvalidRate},
		{"negative rejected", 100, "-5", 0, ErrInvalidRate},
		{"garbage rejected", 100, "soon", 0, ErrInvalidRate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &memoryTransport{handler: allowHandler(testOrigin, tc.allowed)}
			v := newTestValidator(t, tr, 0, func(o *Options) {
				o.RequestRate = tc.requestRate
			})

			sub, err := v.Register(context.Background(), testURL)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Register: %v", err)
			}
			if sub.AllowedRate != tc.want {
				t.Errorf("AllowedRate = %d, want %d", sub.AllowedRate, tc.want)
			}
		})
	}
}

func TestCallbackMustBeHTTPS(t *testing.T) {
	v, err := New(Options{Origin: testOrigin, RequestRate: 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, raw := range []string{
		"http://subscriber.example.com/hook",
		"ftp://subscriber.example.com/hook",
		"subscriber.example.com/hook",
	} {
		if _, err := v.Register(context.Background(), raw); !errors.Is(err, ErrInvalidCallback) {
			t.Errorf("Register(%q) err = %v, want ErrInvalidCallback", raw, err)
		}
	}
}

func TestBlockedAddresses(t *testing.T) {
	v, err := New(Options{
		Origin:      testOrigin,
		RequestRate: 10,
		LookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			return nil, fmt.Errorf("lookup should not be needed for literal IPs")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	blocked := []string{
		"https://127.0.0.1/hook",
		"https://127.1.2.3/hook",
		"https://[::1]/hook",
		"https://10.0.0.5/hook",
		"https://172.16.5.4/hook",
		"https://192.168.1.10/hook",
		"https://169.254.1.1/hook",
		"https://100.64.0.1/hook",
		"https://0.0.0.0/hook",
		"https://224.0.0.1/hook",
		"https://[fe80::1]/hook",
		"https://[fc00::1]/hook",
	}
	for _, raw := range blocked {
		if _, err := v.Register(context.Background(), raw); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("Register(%q) err = %v, want ErrBlockedAddress", raw, err)
		}
	}
}

func TestBlockedHostnameViaResolver(t *testing.T) {
	v, err := New(Options{
		Origin:      testOrigin,
		RequestRate: 10,
		LookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			// A mixed record containing one private address must fail closed.
			return []net.IP{net.ParseIP("198.51.100.9"), net.ParseIP("10.1.2.3")}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = v.Register(context.Background(), "https://attacker.example.com/hook")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress when any resolved IP is private", err)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	tr := &memoryTransport{
		handler: func(host string, w *memoryResponseWriter, r *http.Request) {
			if host == "subscriber.example.com" {
				w.Header().Set("Location", "https://final-target.example.net/hook")
				w.WriteHeader(http.StatusFound)
				return
			}
			// Reachable only if redirects were followed, which they must not be.
			w.Header().Set(HeaderAllowedOrigin, testOrigin)
			w.Header().Set(HeaderAllowedRate, "10")
		},
	}
	v := newTestValidator(t, tr, 0, nil)

	_, err := v.Register(context.Background(), testURL)
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus for 302", err)
	}
	if len(tr.requests) != 1 {
		t.Fatalf("request count = %d, want 1: redirects must not be followed", len(tr.requests))
	}
	if tr.requests[0].url.Hostname() != "subscriber.example.com" {
		t.Errorf("request reached %q, want only the registered host", tr.requests[0].url.Hostname())
	}
}

func TestNonSuccessStatusRejected(t *testing.T) {
	tr := &memoryTransport{
		handler: func(host string, w *memoryResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
	}
	v := newTestValidator(t, tr, 0, nil)

	_, err := v.Register(context.Background(), testURL)
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus for 403", err)
	}
}

func TestValidationExpiration(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	clock := now
	tr := &memoryTransport{handler: allowHandler(testOrigin, "10")}
	ttl := time.Hour
	v := newTestValidator(t, tr, ttl, func(o *Options) {
		o.Now = func() time.Time { return clock }
	})

	sub, err := v.Register(context.Background(), testURL)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !sub.ExpiresAt.Equal(now.Add(ttl)) {
		t.Fatalf("ExpiresAt = %v, want %v", sub.ExpiresAt, now.Add(ttl))
	}
	if _, err := v.AllowDelivery(testURL); err != nil {
		t.Fatalf("fresh subscription: %v", err)
	}

	// One second past the 24-hour-style TTL: delivery pauses.
	clock = now.Add(ttl + time.Second)
	if _, err := v.AllowDelivery(testURL); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired after TTL", err)
	}

	// Revalidation refreshes the expiry and restores delivery.
	refreshed, err := v.Revalidate(context.Background(), testURL)
	if err != nil {
		t.Fatalf("Revalidate: %v", err)
	}
	if !refreshed.ExpiresAt.Equal(clock.Add(ttl)) {
		t.Errorf("refreshed ExpiresAt = %v, want %v", refreshed.ExpiresAt, clock.Add(ttl))
	}
	if _, err := v.AllowDelivery(testURL); err != nil {
		t.Errorf("delivery still paused after revalidation: %v", err)
	}
}

func TestDefaultTTLIs24Hours(t *testing.T) {
	tr := &memoryTransport{handler: allowHandler(testOrigin, "10")}
	v := newTestValidator(t, tr, 0, nil)
	if v.ttl != DefaultTTL || DefaultTTL != 24*time.Hour {
		t.Fatalf("ttl = %v, want 24h", v.ttl)
	}
}

func TestRevalidateUnknownSubscription(t *testing.T) {
	v, err := New(Options{Origin: testOrigin, RequestRate: 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := v.Revalidate(context.Background(), "https://unknown.example.com/hook"); !errors.Is(err, ErrUnknownSubscription) {
		t.Fatalf("err = %v, want ErrUnknownSubscription", err)
	}
}

func TestVerifyResponseHostRejectsMismatch(t *testing.T) {
	registered, _ := url.Parse("https://subscriber.example.com/hook")
	actual, _ := url.Parse("https://other-host.example.net/hook")
	if err := verifyResponseHost(registered, actual, "subscriber.example.com"); !errors.Is(err, ErrHostMismatch) {
		t.Fatalf("err = %v, want ErrHostMismatch", err)
	}

	matching, _ := url.Parse("https://SUBSCRIBER.example.com/hook")
	if err := verifyResponseHost(registered, matching, "subscriber.example.com"); err != nil {
		t.Fatalf("same host (case-insensitive) should pass: %v", err)
	}

	if err := verifyResponseHost(registered, nil, "subscriber.example.com"); !errors.Is(err, ErrHostMismatch) {
		t.Fatalf("nil response URL: err = %v, want ErrHostMismatch", err)
	}
}

func TestIsBlockedIP(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "198.51.100.7", "2606:4700:4700::1111"}
	for _, raw := range public {
		if isBlockedIP(net.ParseIP(raw)) {
			t.Errorf("isBlockedIP(%s) = true, want false", raw)
		}
	}
	blocked := []string{"127.0.0.1", "10.2.3.4", "172.31.255.255", "192.168.0.1", "169.254.0.1",
		"100.64.255.255", "0.0.0.0", "239.1.2.3", "::1", "fe80::2", "fd00::1"}
	for _, raw := range blocked {
		if !isBlockedIP(net.ParseIP(raw)) {
			t.Errorf("isBlockedIP(%s) = false, want true", raw)
		}
	}
}

func TestNewRejectsBadOptions(t *testing.T) {
	if _, err := New(Options{Origin: "", RequestRate: 10}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty origin: err = %v, want ErrInvalidConfig", err)
	}
	if _, err := New(Options{Origin: testOrigin, RequestRate: 0}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero rate: err = %v, want ErrInvalidConfig", err)
	}
}
