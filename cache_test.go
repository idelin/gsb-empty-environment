package netsec_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	netsec "example.org/netsec/q0268"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.now = c.now.Add(duration)
}

func request(t *testing.T, method, target, authorization, cookie string, extra http.Header) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	for name, values := range extra {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	return req
}

func allowResponse(maxAge string) *netsec.Result {
	header := make(http.Header)
	if maxAge != "" {
		header.Set("Cache-Control", "max-age="+maxAge)
	}
	header.Set("X-Auth-User", "alice")
	return netsec.NewResult(http.StatusOK, header, []byte(`{"ok":true}`))
}

func TestCacheSeparatesUsersAndCredentials(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	cache := netsec.New(clock.Now)

	alice := request(t, http.MethodGet, "https://gateway.test/files/report?version=1", "Bearer alice", "sid=alice", nil)
	bob := request(t, http.MethodGet, "https://gateway.test/files/report?version=1", "Bearer bob", "sid=bob", nil)

	stored, err := cache.Store(alice, allowResponse("30"))
	if err != nil || !stored {
		t.Fatalf("Store(alice) = %v, %v; want true, nil", stored, err)
	}
	if result, ok := cache.Get(alice); !ok || result.StatusCode != http.StatusOK {
		t.Fatalf("Get(alice) = %#v, %v; want cached 200", result, ok)
	}
	if result, ok := cache.Get(bob); ok {
		t.Fatalf("Get(bob) = %#v, true; want miss", result)
	}

	aliceOtherCookie := request(t, http.MethodGet, "https://gateway.test/files/report?version=1", "Bearer alice", "sid=other", nil)
	if result, ok := cache.Get(aliceOtherCookie); ok {
		t.Fatalf("Get(other cookie) = %#v, true; want miss", result)
	}
}

func TestCacheBindsMethodHostPathAndQuery(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	cache := netsec.New(clock.Now)
	target := "https://gateway.test/files/report?version=1"
	base := request(t, http.MethodGet, target, "Bearer alice", "sid=alice", nil)

	if stored, err := cache.Store(base, allowResponse("30")); err != nil || !stored {
		t.Fatalf("Store(base) = %v, %v", stored, err)
	}

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"method", request(t, http.MethodHead, target, "Bearer alice", "sid=alice", nil)},
		{"host", request(t, http.MethodGet, "https://other.test/files/report?version=1", "Bearer alice", "sid=alice", nil)},
		{"path", request(t, http.MethodGet, "https://gateway.test/files/other?version=1", "Bearer alice", "sid=alice", nil)},
		{"query", request(t, http.MethodGet, "https://gateway.test/files/report?version=2", "Bearer alice", "sid=alice", nil)},
		{"scheme", request(t, http.MethodGet, "http://gateway.test/files/report?version=1", "Bearer alice", "sid=alice", nil)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if result, ok := cache.Get(testCase.req); ok {
				t.Fatalf("Get() = %#v, true; want miss", result)
			}
		})
	}
}

func TestCacheBindsVaryHeaders(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	cache := netsec.New(clock.Now)
	target := "https://gateway.test/files/report"

	tenantA := request(t, http.MethodGet, target, "Bearer alice", "sid=alice", http.Header{
		"X-Tenant": {"alpha"},
		"X-Scope":  {"read"},
	})
	tenantB := request(t, http.MethodGet, target, "Bearer alice", "sid=alice", http.Header{
		"X-Tenant": {"beta"},
		"X-Scope":  {"read"},
	})
	scopeWrite := request(t, http.MethodGet, target, "Bearer alice", "sid=alice", http.Header{
		"X-Tenant": {"alpha"},
		"X-Scope":  {"write"},
	})

	response := allowResponse("30")
	response.Header.Set("Vary", "X-Tenant, X-Scope")
	if stored, err := cache.Store(tenantA, response); err != nil || !stored {
		t.Fatalf("Store(tenant A) = %v, %v", stored, err)
	}
	if _, ok := cache.Get(tenantA); !ok {
		t.Fatal("Get(tenant A) missed")
	}
	if result, ok := cache.Get(tenantB); ok {
		t.Fatalf("Get(tenant B) = %#v, true; want miss", result)
	}
	if result, ok := cache.Get(scopeWrite); ok {
		t.Fatalf("Get(scope write) = %#v, true; want miss", result)
	}
	responseForB := allowResponse("30")
	responseForB.Header.Set("Vary", "X-Tenant, X-Scope")
	if stored, err := cache.Store(tenantB, responseForB); err != nil || !stored {
		t.Fatalf("Store(tenant B) = %v, %v", stored, err)
	}
	if _, ok := cache.Get(tenantA); !ok {
		t.Fatal("Get(tenant A) missed after storing tenant B")
	}
	if _, ok := cache.Get(tenantB); !ok {
		t.Fatal("Get(tenant B) missed after storing tenant B")
	}

	keyA, err := netsec.BuildKey(tenantA, response.Header)
	if err != nil {
		t.Fatalf("BuildKey(tenant A): %v", err)
	}
	keyB, err := netsec.BuildKey(tenantB, response.Header)
	if err != nil {
		t.Fatalf("BuildKey(tenant B): %v", err)
	}
	if keyA == keyB {
		t.Fatal("BuildKey did not bind Vary header values")
	}
}

func TestCacheRejectsVaryStar(t *testing.T) {
	cache := netsec.New()
	req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
	response := allowResponse("30")
	response.Header.Set("Vary", "*")

	stored, err := cache.Store(req, response)
	if err != nil {
		t.Fatalf("Store(): %v", err)
	}
	if stored {
		t.Fatal("Store() cached a Vary * response")
	}
}

func TestNoCredentialRequestsAreNotCached(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	cache := netsec.New(clock.Now)
	target := "https://gateway.test/private"

	anonymous := request(t, http.MethodGet, target, "", "", nil)
	if stored, err := cache.Store(anonymous, allowResponse("30")); err != nil || stored {
		t.Fatalf("Store(anonymous) = %v, %v; want false, nil", stored, err)
	}
	if result, ok := cache.Get(anonymous); ok {
		t.Fatalf("Get(anonymous) = %#v, true; want miss", result)
	}

	emptyAuthorization := request(t, http.MethodGet, target, " ", "", nil)
	if stored, err := cache.Store(emptyAuthorization, allowResponse("30")); err != nil || stored {
		t.Fatalf("Store(empty auth) = %v, %v; want false, nil", stored, err)
	}

	authorizationOnly := request(t, http.MethodGet, target, "Bearer alice", "", nil)
	if stored, err := cache.Store(authorizationOnly, allowResponse("30")); err != nil || !stored {
		t.Fatalf("Store(auth only) = %v, %v; want true, nil", stored, err)
	}
	cookieOnly := request(t, http.MethodGet, target, "", "sid=alice", nil)
	if stored, err := cache.Store(cookieOnly, allowResponse("30")); err != nil || !stored {
		t.Fatalf("Store(cookie only) = %v, %v; want true, nil", stored, err)
	}
}

func TestTTLBoundaries(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)

	t.Run("200 exact max-age boundary", func(t *testing.T) {
		clock := &fakeClock{now: start}
		cache := netsec.New(clock.Now)
		req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
		if stored, err := cache.Store(req, allowResponse("10")); err != nil || !stored {
			t.Fatalf("Store() = %v, %v", stored, err)
		}
		clock.Advance(10*time.Second - time.Nanosecond)
		if _, ok := cache.Get(req); !ok {
			t.Fatal("cache expired before the max-age boundary")
		}
		clock.Advance(time.Nanosecond)
		if result, ok := cache.Get(req); ok {
			t.Fatalf("Get() at boundary = %#v, true; want miss", result)
		}
	})

	t.Run("200 max-age capped at 60 seconds", func(t *testing.T) {
		clock := &fakeClock{now: start}
		cache := netsec.New(clock.Now)
		req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
		if stored, err := cache.Store(req, allowResponse("600")); err != nil || !stored {
			t.Fatalf("Store() = %v, %v", stored, err)
		}
		clock.Advance(60*time.Second - time.Nanosecond)
		if _, ok := cache.Get(req); !ok {
			t.Fatal("cache expired before the 60 second cap")
		}
		clock.Advance(time.Nanosecond)
		if _, ok := cache.Get(req); ok {
			t.Fatal("200 cache outlived the 60 second cap")
		}
	})

	t.Run("403 default lifetime is 5 seconds", func(t *testing.T) {
		clock := &fakeClock{now: start}
		cache := netsec.New(clock.Now)
		req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
		response := netsec.NewResult(http.StatusForbidden, nil, []byte("denied"))
		if stored, err := cache.Store(req, response); err != nil || !stored {
			t.Fatalf("Store() = %v, %v", stored, err)
		}
		clock.Advance(5*time.Second - time.Nanosecond)
		if _, ok := cache.Get(req); !ok {
			t.Fatal("403 cache expired before 5 seconds")
		}
		clock.Advance(time.Nanosecond)
		if _, ok := cache.Get(req); ok {
			t.Fatal("403 cache outlived 5 seconds")
		}
	})

	t.Run("403 declared max-age is also capped at 5 seconds", func(t *testing.T) {
		clock := &fakeClock{now: start}
		cache := netsec.New(clock.Now)
		req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
		response := netsec.NewResult(http.StatusForbidden, http.Header{
			"Cache-Control": {"max-age=120"},
		}, nil)
		if stored, err := cache.Store(req, response); err != nil || !stored {
			t.Fatalf("Store() = %v, %v", stored, err)
		}
		clock.Advance(5 * time.Second)
		if _, ok := cache.Get(req); ok {
			t.Fatal("403 cache outlived the 5 second cap")
		}
	})
}

func TestUncacheableStatusesAndCacheControl(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		header     http.Header
		wantStored bool
	}{
		{"200 without max-age", http.StatusOK, nil, false},
		{"200 max-age zero", http.StatusOK, http.Header{"Cache-Control": {"max-age=0"}}, false},
		{"200 no-store", http.StatusOK, http.Header{"Cache-Control": {"max-age=30, no-store"}}, false},
		{"200 no-cache", http.StatusOK, http.Header{"Cache-Control": {"max-age=30, no-cache"}}, false},
		{"200 quoted max-age", http.StatusOK, http.Header{"Cache-Control": {`max-age="30"`}}, true},
		{"401", http.StatusUnauthorized, http.Header{"Cache-Control": {"max-age=30"}}, false},
		{"500", http.StatusInternalServerError, http.Header{"Cache-Control": {"max-age=30"}}, false},
		{"503", http.StatusServiceUnavailable, http.Header{"Cache-Control": {"max-age=30"}}, false},
		{"403 no-store", http.StatusForbidden, http.Header{"Cache-Control": {"max-age=5, no-store"}}, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cache := netsec.New()
			req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
			response := netsec.NewResult(testCase.statusCode, testCase.header, nil)
			stored, err := cache.Store(req, response)
			if err != nil {
				t.Fatalf("Store(): %v", err)
			}
			if stored != testCase.wantStored {
				t.Fatalf("Store() = %v; want %v", stored, testCase.wantStored)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/a/./b", "/a/b"},
		{"/a/../b", "/b"},
		{"/a/%2e/b", "/a/b"},
		{"/a/%2E%2E/b", "/b"},
		{"/a/%252e%252e/b", "/b"},
		{"/a/b/..", "/a/"},
		{"/a/b/.", "/a/b/"},
		{"/a//b", "/a//b"},
		{"/discount%25", "/discount%"},
		{"/discount%2525", "/discount%"},
		{"", "/"},
	}
	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			got, err := netsec.NormalizePath(testCase.input)
			if err != nil {
				t.Fatalf("NormalizePath(%q): %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Fatalf("NormalizePath(%q) = %q; want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestEncodedSlashRejected(t *testing.T) {
	inputs := []string{
		"/a/%2fb",
		"/a/%2Fb",
		"/a/%252fb",
		"/a/%25252Fb",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			if got, err := netsec.NormalizePath(input); !errors.Is(err, netsec.ErrEncodedSlash) {
				t.Fatalf("NormalizePath(%q) = %q, %v; want ErrEncodedSlash", input, got, err)
			}
			req := httptest.NewRequest(http.MethodGet, "https://gateway.test"+input, nil)
			if key, err := netsec.BuildKey(req); !errors.Is(err, netsec.ErrEncodedSlash) {
				t.Fatalf("BuildKey(%q) = %q, %v; want ErrEncodedSlash", input, key, err)
			}
		})
	}
}

func TestEncodedSlashInQueryIsNotAPathError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://gateway.test/files?next=%2fdashboard", nil)
	if _, err := netsec.BuildKey(req); err != nil {
		t.Fatalf("BuildKey() rejected an encoded slash in the query: %v", err)
	}
}

func TestPathCanonicalizationDoesNotCrossResources(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	cache := netsec.New(clock.Now)
	admin := request(t, http.MethodGet, "https://gateway.test/admin", "Bearer alice", "", nil)
	adminEscape := request(t, http.MethodGet, "https://gateway.test/admin/%2e%2e/public", "Bearer alice", "", nil)
	publicEscape := request(t, http.MethodGet, "https://gateway.test/x/%2e%2e/admin", "Bearer alice", "", nil)

	if stored, err := cache.Store(admin, allowResponse("30")); err != nil || !stored {
		t.Fatalf("Store(admin) = %v, %v", stored, err)
	}
	if result, ok := cache.Get(adminEscape); ok {
		t.Fatalf("Get(admin/../public) = %#v, true; want miss", result)
	}
	if result, ok := cache.Get(publicEscape); !ok {
		t.Fatalf("Get(x/../admin) = %#v, false; want normalized admin hit", result)
	}
}

func TestCachedResultIsCopied(t *testing.T) {
	cache := netsec.New()
	req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
	response := allowResponse("30")
	response.Body = []byte("allowed")

	if stored, err := cache.Store(req, response); err != nil || !stored {
		t.Fatalf("Store() = %v, %v", stored, err)
	}
	response.Header.Set("X-Auth-User", "mallory")
	response.Body[0] = 'X'

	first, ok := cache.Get(req)
	if !ok {
		t.Fatal("Get() missed")
	}
	if got := first.Header.Get("X-Auth-User"); got != "alice" {
		t.Fatalf("cached header = %q; want alice", got)
	}
	if string(first.Body) != "allowed" {
		t.Fatalf("cached body = %q; want allowed", first.Body)
	}

	first.Header.Set("X-Auth-User", "mallory")
	first.Body[0] = 'X'
	second, ok := cache.Get(req)
	if !ok {
		t.Fatal("second Get() missed")
	}
	if got := second.Header.Get("X-Auth-User"); got != "alice" {
		t.Fatalf("second cached header = %q; want alice", got)
	}
	if string(second.Body) != "allowed" {
		t.Fatalf("second cached body = %q; want allowed", second.Body)
	}
}

func TestHTTPResponseCanBeStored(t *testing.T) {
	cache := netsec.New()
	req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", nil)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Cache-Control": {"max-age=30"},
		},
		Body: io.NopCloser(strings.NewReader("allowed")),
	}

	if stored, err := cache.Store(req, response); err != nil || !stored {
		t.Fatalf("Store() = %v, %v", stored, err)
	}
	restoredBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restoredBody) != "allowed" {
		t.Fatalf("restored body = %q; want allowed", string(restoredBody))
	}
	if result, ok := cache.Get(req); !ok || string(result.Body) != "allowed" {
		t.Fatalf("Get() = %#v, %v", result, ok)
	}
}

func TestVaryCanBeSuppliedSeparately(t *testing.T) {
	cache := netsec.New()
	req := request(t, http.MethodGet, "https://gateway.test/", "Bearer alice", "", http.Header{
		"X-Tenant": {"alpha"},
	})
	vary := http.Header{"Vary": {"X-Tenant"}}

	if stored, err := cache.Store(req, allowResponse("30"), vary); err != nil || !stored {
		t.Fatalf("Store() = %v, %v", stored, err)
	}
	if _, ok := cache.Get(req, vary); !ok {
		t.Fatal("Get() with matching Vary missed")
	}
	if result, ok := cache.Get(req, http.Header{"Vary": {"X-Other"}}); ok {
		t.Fatalf("Get() with different Vary = %#v, true; want miss", result)
	}
}

func TestCacheTTL(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		header     http.Header
		wantTTL    time.Duration
		wantOK     bool
	}{
		{"200 max-age", http.StatusOK, http.Header{"Cache-Control": {"max-age=12"}}, 12 * time.Second, true},
		{"200 cap", http.StatusOK, http.Header{"Cache-Control": {"max-age=120"}}, 60 * time.Second, true},
		{"200 huge max-age", http.StatusOK, http.Header{"Cache-Control": {"max-age=9223372036854775807"}}, 60 * time.Second, true},
		{"200 no max-age", http.StatusOK, nil, 0, false},
		{"403 default", http.StatusForbidden, nil, 5 * time.Second, true},
		{"403 cap", http.StatusForbidden, http.Header{"Cache-Control": {"max-age=120"}}, 5 * time.Second, true},
		{"403 huge max-age", http.StatusForbidden, http.Header{"Cache-Control": {"max-age=9223372036854775807"}}, 5 * time.Second, true},
		{"403 shorter max-age", http.StatusForbidden, http.Header{"Cache-Control": {"max-age=2"}}, 2 * time.Second, true},
		{"401", http.StatusUnauthorized, nil, 0, false},
		{"500", http.StatusInternalServerError, nil, 0, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := netsec.CacheTTL(testCase.statusCode, testCase.header)
			if got != testCase.wantTTL || ok != testCase.wantOK {
				t.Fatalf("CacheTTL() = %v, %v; want %v, %v", got, ok, testCase.wantTTL, testCase.wantOK)
			}
		})
	}
}
