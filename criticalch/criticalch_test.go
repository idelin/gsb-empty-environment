package criticalch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const (
	hintUA       = "Sec-Ch-Ua"
	hintMobile   = "Sec-Ch-Ua-Mobile"
	hintPlatform = "Sec-Ch-Ua-Platform"
)

func newRequest(t *testing.T, headers map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://example.test/index", nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return req
}

func newResponse(headers map[string]string) *http.Response {
	header := make(http.Header, len(headers))
	for name, value := range headers {
		header.Set(name, value)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(nil),
	}
}

func clientHints() ClientHints {
	return NewClientHints(map[string]string{
		hintUA:       `"TestBrowser"`,
		hintMobile:   "?0",
		hintPlatform: `"macOS"`,
	})
}

// A Critical-CH field that Accept-CH does not advertise must be ignored: it
// neither triggers a retry nor forbids shared caching.
func TestCriticalCHNotInAcceptCHIsIgnored(t *testing.T) {
	resp := newResponse(map[string]string{
		"Accept-CH":   hintMobile,
		"Critical-CH": hintPlatform,
	})

	if effective := EffectiveCriticalHints(resp); len(effective) != 0 {
		t.Fatalf("effective critical hints = %v, want empty", effective)
	}

	req := newRequest(t, nil)
	decision := Evaluate(req, resp, clientHints())
	if decision.Retry {
		t.Fatal("retry = true for a Critical-CH hint missing from Accept-CH")
	}
	if decision.Uncacheable {
		t.Fatal("uncacheable = true for an ignored Critical-CH hint")
	}
	if !SharedCacheable(resp) {
		t.Fatal("response with only ignored Critical-CH hints must stay shared-cacheable")
	}

	// Same rule when the client could send the hint: still ignored.
	resp.Header.Set("Critical-CH", hintUA)
	decision = Evaluate(req, resp, clientHints())
	if decision.Retry || decision.Uncacheable {
		t.Fatalf("unexpected decision for unadvertised hint: %+v", decision)
	}
}

// A retry happens once per navigation: the first Critical-CH demand retries
// with the hint attached, and a second demand on the retried request must not
// cause another round trip.
func TestAtMostOneRetryPerNavigation(t *testing.T) {
	var requests []*http.Request
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		switch len(requests) {
		case 1:
			return newResponse(map[string]string{
				"Accept-CH":   hintMobile,
				"Critical-CH": hintMobile,
				"Vary":        hintMobile,
			}), nil
		default:
			// The retried response demands another hint.
			return newResponse(map[string]string{
				"Accept-CH":   hintMobile + ", " + hintPlatform,
				"Critical-CH": hintPlatform,
				"Vary":        hintMobile + ", " + hintPlatform,
			}), nil
		}
	})

	req := newRequest(t, nil)
	resp, err := RoundTrip(transport, req, clientHints())
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("round trips = %d, want exactly 2", len(requests))
	}

	retryReq := requests[1]
	if got := retryReq.Header.Get(hintMobile); got != "?0" {
		t.Fatalf("retried request hint = %q, want ?0", got)
	}
	if !AlreadyRetried(retryReq.Context()) {
		t.Fatal("retried request must carry the per-navigation retry marker")
	}

	// Evaluating the second Critical-CH response against the marked request
	// must not request another retry.
	decision := Evaluate(retryReq, resp, clientHints())
	if decision.Retry {
		t.Fatal("second Critical-CH demand must not trigger another retry")
	}

	// Building another retry request is a no-op in terms of retry eligibility.
	if BuildRetryRequest(retryReq, decision).Header.Get(hintPlatform) != "" {
		t.Fatal("no hint may be attached when the retry budget is spent")
	}
}

// A retry is also suppressed when the original request already carried the
// hint or when the client cannot send it.
func TestNoRetryWhenHintPresentOrUnsupported(t *testing.T) {
	mobileOnly := newResponse(map[string]string{
		"Accept-CH":   hintMobile,
		"Critical-CH": hintMobile,
		"Vary":        hintMobile,
	})

	// The request already carries the demanded hint: no retry.
	present := newRequest(t, map[string]string{hintMobile: "?1"})
	if decision := Evaluate(present, mobileOnly, clientHints()); decision.Retry {
		t.Fatal("retry = true although the request already carried Sec-Ch-Ua-Mobile")
	}

	both := newResponse(map[string]string{
		"Accept-CH":   hintMobile + ", " + hintPlatform,
		"Critical-CH": hintMobile + ", " + hintPlatform,
		"Vary":        hintMobile + ", " + hintPlatform,
	})

	// Client can send only one of the two demanded hints.
	unsupported := NewClientHints(map[string]string{hintPlatform: `"macOS"`})
	decision := Evaluate(newRequest(t, nil), both, unsupported)
	if !decision.Retry {
		t.Fatal("retry = false although the client can send Sec-Ch-Ua-Platform")
	}
	want := map[string]string{hintPlatform: `"macOS"`}
	if !reflect.DeepEqual(decision.Hints, want) {
		t.Fatalf("retry hints = %v, want %v", decision.Hints, want)
	}

	// Explicit retry marker blocks retries even on a fresh request.
	marked := newRequest(t, nil)
	marked = marked.Clone(WithRetryMarker(marked.Context()))
	if decision := Evaluate(marked, mobileOnly, clientHints()); decision.Retry {
		t.Fatal("retry = true on an already-retried navigation")
	}
}

// When a response depends on a Critical-CH hint but Vary omits it, the
// response is forbidden from the shared cache.
func TestCriticalCHWithoutVaryIsUncacheable(t *testing.T) {
	resp := newResponse(map[string]string{
		"Accept-CH":   hintMobile + ", " + hintPlatform,
		"Critical-CH": hintMobile + ", " + hintPlatform,
		"Vary":        hintMobile,
	})

	decision := Evaluate(newRequest(t, nil), resp, clientHints())
	if !decision.Uncacheable {
		t.Fatal("response should be uncacheable when Critical-CH hint is missing from Vary")
	}
	if SharedCacheable(resp) {
		t.Fatal("SharedCacheable = true with a Critical-CH hint absent from Vary")
	}

	cache := NewCache()
	err := cache.Store("base", resp, false)
	if !errors.Is(err, ErrNotCacheable) {
		t.Fatalf("store error = %v, want ErrNotCacheable", err)
	}
	if cache.Len() != 0 {
		t.Fatalf("cache length = %d, want 0 after rejected store", cache.Len())
	}

	// Listing every critical hint in Vary makes the response cacheable.
	resp.Header.Set("Vary", hintMobile+", "+hintPlatform)
	if !SharedCacheable(resp) {
		t.Fatal("response should be cacheable once Vary covers all Critical-CH hints")
	}
	if err := cache.Store("base", resp, false); err != nil {
		t.Fatalf("store after Vary fix: %v", err)
	}
}

// Different values of a Vary-listed hint must produce distinct cache keys and
// distinct cache entries, so responses never get mixed across clients.
func TestCacheSeparationByHintValue(t *testing.T) {
	resp := newResponse(map[string]string{
		"Accept-CH":   hintMobile,
		"Critical-CH": hintMobile,
		"Vary":        hintMobile,
	})

	desktop := newRequest(t, map[string]string{hintMobile: "?0"})
	mobile := newRequest(t, map[string]string{hintMobile: "?1"})

	keyDesktop := CacheKey("GET https://example.test/index", desktop, resp)
	keyMobile := CacheKey("GET https://example.test/index", mobile, resp)
	if keyDesktop == keyMobile {
		t.Fatalf("identical cache keys for distinct hint values: %q", keyDesktop)
	}

	cache := NewCache()
	if err := cache.Store(keyDesktop, resp, false); err != nil {
		t.Fatalf("store desktop: %v", err)
	}
	if err := cache.Store(keyMobile, resp, false); err != nil {
		t.Fatalf("store mobile: %v", err)
	}
	if cache.Len() != 2 {
		t.Fatalf("cache length = %d, want 2 separated entries", cache.Len())
	}
	if _, ok := cache.Get(keyDesktop); !ok {
		t.Fatal("desktop entry missing")
	}
	if _, ok := cache.Get(keyMobile); !ok {
		t.Fatal("mobile entry missing")
	}

	// Identical hint values share an entry.
	desktopOther := newRequest(t, map[string]string{hintMobile: "?0"})
	keyOther := CacheKey("GET https://example.test/index", desktopOther, resp)
	if keyOther != keyDesktop {
		t.Fatal("identical hint values must produce identical cache keys")
	}
}

// A retried response must never overwrite the entry created from the first
// response, even when both land on the same cache key.
func TestRetryResponseDoesNotOverwriteFirstEntry(t *testing.T) {
	first := newResponse(map[string]string{
		"Accept-CH":   hintMobile,
		"Critical-CH": hintMobile,
		"Vary":        hintMobile,
	})
	first.StatusCode = http.StatusOK

	retried := newResponse(map[string]string{
		"Accept-CH":   hintMobile,
		"Critical-CH": hintMobile,
		"Vary":        hintMobile,
	})
	retried.StatusCode = http.StatusCreated

	cache := NewCache()
	const key = "same-key"
	if err := cache.Store(key, first, false); err != nil {
		t.Fatalf("store first response: %v", err)
	}

	err := cache.Store(key, retried, true)
	if !errors.Is(err, ErrEntryExists) {
		t.Fatalf("retry store error = %v, want ErrEntryExists", err)
	}

	entry, ok := cache.Get(key)
	if !ok {
		t.Fatal("first response entry disappeared")
	}
	if entry.StatusCode != http.StatusOK {
		t.Fatalf("cached status = %d, first response was overwritten", entry.StatusCode)
	}

	// A retry response may still populate a previously empty key.
	if err := cache.Store("other-key", retried, true); err != nil {
		t.Fatalf("retry store into empty key: %v", err)
	}
}

// End-to-end behavior: the first response is cached against the request that
// lacked the hint, and the retried response is cached separately under the
// hint-bearing key without replacing the first entry.
func TestRoundTripCachesFirstAndRetrySeparately(t *testing.T) {
	var requests []*http.Request
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		resp := newResponse(map[string]string{
			"Accept-CH":   hintMobile,
			"Critical-CH": hintMobile,
			"Vary":        hintMobile,
		})
		if len(requests) > 1 {
			resp.StatusCode = http.StatusNonAuthoritativeInfo
			resp.Header.Set("X-Try", "retry")
		}
		return resp, nil
	})

	cache := NewCache()
	req := newRequest(t, nil)

	first, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("first round trip: %v", err)
	}
	firstKey := CacheKey("nav", req, first)
	if err := cache.Store(firstKey, first, false); err != nil {
		t.Fatalf("cache first response: %v", err)
	}

	// The proxy evaluates the first response and performs the single retry.
	decision := Evaluate(req, first, clientHints())
	if !decision.Retry {
		t.Fatal("expected a retry after the first Critical-CH response")
	}
	retryReq := BuildRetryRequest(req, decision)
	retryResp, err := transport.RoundTrip(retryReq)
	if err != nil {
		t.Fatalf("retry round trip: %v", err)
	}
	if got := retryReq.Header.Get(hintMobile); got != "?0" {
		t.Fatalf("retry request carried %q, want ?0", got)
	}
	retryKey := CacheKey("nav", retryReq, retryResp)
	if retryKey == firstKey {
		t.Fatal("retry response must not share the first response's cache key")
	}
	if err := cache.Store(retryKey, retryResp, true); err != nil {
		t.Fatalf("cache retry response: %v", err)
	}
	if cache.Len() != 2 {
		t.Fatalf("cache length = %d, want 2 entries", cache.Len())
	}

	entry, ok := cache.Get(firstKey)
	if !ok || entry.StatusCode != http.StatusOK {
		t.Fatal("first-response cache entry was not preserved")
	}
}

func TestVaryKeyEncodingAndWildcard(t *testing.T) {
	resp := newResponse(map[string]string{"Vary": hintMobile})
	reqA := newRequest(t, map[string]string{hintMobile: "?0"})
	reqB := newRequest(t, map[string]string{hintMobile: "?00"})
	if VaryKey(reqA, resp) == VaryKey(reqB, resp) {
		t.Fatal("length-prefixed encoding must distinguish ?0 from ?00")
	}

	wildcard := newResponse(map[string]string{"Vary": "*"})
	if got := VaryKey(reqA, wildcard); got == "" {
		t.Fatal("wildcard Vary must contribute a non-empty key fragment")
	}

	noVary := newResponse(nil)
	if got := VaryKey(reqA, noVary); got != "" {
		t.Fatalf("VaryKey without Vary = %q, want empty fragment", got)
	}
}

func TestAlreadyRetriedMarker(t *testing.T) {
	if AlreadyRetried(context.Background()) {
		t.Fatal("fresh context must not be marked as retried")
	}
	marked := WithRetryMarker(context.Background())
	if !AlreadyRetried(marked) {
		t.Fatal("WithRetryMarker did not mark the context")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParsingAndWrappers(t *testing.T) {
	// Parameters after ';', whitespace, blanks and duplicates are handled, and
	// names are canonicalized.
	resp := newResponse(map[string]string{
		"Accept-CH":   " sec-ch-ua-mobile, sec-ch-ua-platform;q=0.5, , SEC-CH-UA-MOBILE ",
		"Critical-CH": "Sec-Ch-Ua-Mobile",
		"Vary":        "sec-ch-ua-mobile, *",
	})

	got := EffectiveCriticalHints(resp)
	want := []string{hintMobile}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective hints = %v, want %v", got, want)
	}
	if got := VarySet(resp); !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatalf("VarySet = %v, want [*]", got)
	}

	hints := clientHints()
	if !hints.CanSend(hintMobile) {
		t.Fatal("CanSend = false for an advertised client hint")
	}
	if hints.CanSend("Sec-Ch-Ua-Arch") {
		t.Fatal("CanSend = true for a hint the client cannot send")
	}

	// A nil request never carries a hint and does not panic.
	if requestCarries(nil, hintMobile) {
		t.Fatal("nil request must report no carried hints")
	}
}

func TestRoundTripBranches(t *testing.T) {
	// No Critical-CH header: exactly one round trip and no retry.
	var trips int
	plain := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		trips++
		return newResponse(nil), nil
	})
	if _, err := RoundTrip(plain, newRequest(t, nil), clientHints()); err != nil {
		t.Fatalf("RoundTrip without Critical-CH: %v", err)
	}
	if trips != 1 {
		t.Fatalf("trips = %d, want 1 when no retry is needed", trips)
	}

	// Transport error on the first leg must surface unchanged.
	boom := errors.New("connection refused")
	failing := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, boom
	})
	if _, err := RoundTrip(failing, newRequest(t, nil), clientHints()); !errors.Is(err, boom) {
		t.Fatalf("RoundTrip error = %v, want %v", err, boom)
	}

	// WithRetryMarker tolerates a nil context and AlreadyRetried reads it back.
	if !AlreadyRetried(WithRetryMarker(nil)) {
		t.Fatal("WithRetryMarker(nil) should produce a marked context")
	}
}
