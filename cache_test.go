package netsec_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0268"
)

func TestCacheSeparatesUsersCookiesAndMethods(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	alice := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	if !cache.Set(alice, http.StatusOK, cacheHeader("max-age=60")) {
		t.Fatal("Set() = false, want true")
	}
	if result, ok := cache.Get(alice); !ok || result.StatusCode != http.StatusOK {
		t.Fatalf("Get(alice) = (%v, %v), want cached 200", result, ok)
	}

	bob := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer bob"},
		"Cookie":        {"session=bob"},
	})
	if result, ok := cache.Get(bob); ok {
		t.Fatalf("Get(bob) = (%v, true), want cache miss", result)
	}

	bobWithAliceCookie := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer bob"},
		"Cookie":        {"session=alice"},
	})
	if _, ok := cache.Get(bobWithAliceCookie); ok {
		t.Fatal("different Authorization reused another user's cache entry")
	}

	alicePost := newRequest(t, http.MethodPost, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	if _, ok := cache.Get(alicePost); ok {
		t.Fatal("different method reused cached authorization")
	}
}

func TestCacheDoesNotStoreRequestsWithoutCredentials(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	req := newRequest(t, http.MethodGet, "https://example.com/public", nil)
	if cache.Set(req, http.StatusOK, cacheHeader("max-age=60")) {
		t.Fatal("Set() = true for request without credentials")
	}
	if _, ok := cache.Get(req); ok {
		t.Fatal("Get() = true for uncached credentialless request")
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}
}

func TestCacheNormalizesEncodedDotPaths(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	direct := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer alice"},
	})
	bypass := newRequest(t, http.MethodGet, "https://example.com/public/%2e%2e/admin", http.Header{
		"Authorization": {"Bearer alice"},
	})

	if !cache.Set(direct, http.StatusOK, cacheHeader("max-age=60")) {
		t.Fatal("Set() = false, want true")
	}
	if result, ok := cache.Get(bypass); !ok || result.StatusCode != http.StatusOK {
		t.Fatalf("Get(encoded dot path) = (%v, %v), want normalized 200", result, ok)
	}
}

func TestCacheRejectsEncodedSlash(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	req := newRequest(t, http.MethodGet, "https://example.com/admin%2fsecret", http.Header{
		"Authorization": {"Bearer alice"},
	})
	if cache.Set(req, http.StatusOK, cacheHeader("max-age=60")) {
		t.Fatal("Set() = true for encoded-slash path")
	}
	if _, ok := cache.Get(req); ok {
		t.Fatal("Get() = true for encoded-slash path")
	}
}

func TestCacheHonorsVary(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	responseHeader := cacheHeader("max-age=60")
	responseHeader.Set("Vary", "Accept-Language")

	english := newRequest(t, http.MethodGet, "https://example.com/docs", http.Header{
		"Authorization":   {"Bearer alice"},
		"Accept-Language": {"en"},
	})
	if !cache.Set(english, http.StatusOK, responseHeader) {
		t.Fatal("Set(english) = false, want true")
	}

	french := newRequest(t, http.MethodGet, "https://example.com/docs", http.Header{
		"Authorization":   {"Bearer alice"},
		"Accept-Language": {"fr"},
	})
	if _, ok := cache.Get(french); ok {
		t.Fatal("different Vary value reused cached authorization")
	}

	if result, ok := cache.Get(english); !ok || result.StatusCode != http.StatusOK {
		t.Fatalf("Get(english) = (%v, %v), want cached 200", result, ok)
	}

	if !cache.Set(french, http.StatusForbidden, responseHeader) {
		t.Fatal("Set(french) = false, want true")
	}
	if result, ok := cache.Get(french); !ok || result.StatusCode != http.StatusForbidden {
		t.Fatalf("Get(french) = (%v, %v), want cached 403", result, ok)
	}
	if result, ok := cache.Get(english); !ok || result.StatusCode != http.StatusOK {
		t.Fatalf("Get(english after french) = (%v, %v), want cached 200", result, ok)
	}
}

func TestVaryStarIsNotCacheableAndInvalidatesVariants(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	req := newRequest(t, http.MethodGet, "https://example.com/docs", http.Header{
		"Authorization":   {"Bearer alice"},
		"Accept-Language": {"en"},
	})
	varyLanguage := cacheHeader("max-age=60")
	varyLanguage.Set("Vary", "Accept-Language")
	if !cache.Set(req, http.StatusOK, varyLanguage) {
		t.Fatal("Set() = false, want true")
	}

	varyAll := cacheHeader("max-age=60")
	varyAll.Set("Vary", "*")
	if cache.Set(req, http.StatusOK, varyAll) {
		t.Fatal("Set() = true for Vary: *")
	}
	if _, ok := cache.Get(req); ok {
		t.Fatal("Vary: * response did not invalidate old variants")
	}
}

func TestChangingVaryFieldsRemovesOldVariants(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	req := newRequest(t, http.MethodGet, "https://example.com/docs", http.Header{
		"Authorization":   {"Bearer alice"},
		"Accept-Language": {"en"},
	})

	varyLanguage := cacheHeader("max-age=60")
	varyLanguage.Set("Vary", "Accept-Language")
	if !cache.Set(req, http.StatusOK, varyLanguage) {
		t.Fatal("Set(Vary: Accept-Language) = false")
	}

	varyNone := cacheHeader("max-age=60")
	if !cache.Set(req, http.StatusForbidden, varyNone) {
		t.Fatal("Set(no Vary) = false")
	}
	for i := 0; i < 20; i++ {
		result, ok := cache.Get(req)
		if !ok {
			t.Fatal("Get() = false after storing replacement")
		}
		if result.StatusCode != http.StatusForbidden {
			t.Fatalf("Get() status = %d, want replacement 403", result.StatusCode)
		}
	}
	if got := cache.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 after Vary change", got)
	}
}

func TestNonCacheableStatusesInvalidateOldAllow(t *testing.T) {
	t.Parallel()

	for _, statusCode := range []int{
		http.StatusUnauthorized,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		statusCode := statusCode
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			cache := netsec.New()
			req := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
				"Authorization": {"Bearer alice"},
			})
			if !cache.Set(req, http.StatusOK, cacheHeader("max-age=60")) {
				t.Fatal("seed Set() = false")
			}
			if cache.Set(req, statusCode, cacheHeader("max-age=60")) {
				t.Fatalf("Set(%d) = true, want false", statusCode)
			}
			if _, ok := cache.Get(req); ok {
				t.Fatalf("Get() reused old allow after status %d", statusCode)
			}
		})
	}
}

func TestCacheTTLPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		header    http.Header
		wantTTL   time.Duration
		cacheable bool
	}{
		{name: "200 max-age", status: 200, header: cacheHeader("max-age=30"), wantTTL: 30 * time.Second, cacheable: true},
		{name: "200 quoted max-age", status: 200, header: cacheHeader(`public, max-age="15"`), wantTTL: 15 * time.Second, cacheable: true},
		{name: "200 capped", status: 200, header: cacheHeader("max-age=300"), wantTTL: 60 * time.Second, cacheable: true},
		{name: "200 no max-age", status: 200, header: cacheHeader("public"), cacheable: false},
		{name: "200 zero max-age", status: 200, header: cacheHeader("max-age=0"), cacheable: false},
		{name: "200 no-store", status: 200, header: cacheHeader("max-age=60, no-store"), cacheable: false},
		{name: "200 no-cache", status: 200, header: cacheHeader("max-age=60, no-cache"), cacheable: false},
		{name: "200 private", status: 200, header: cacheHeader("max-age=60, private"), cacheable: false},
		{name: "200 invalid max-age", status: 200, header: cacheHeader("max-age=abc"), cacheable: false},
		{name: "200 age zero", status: 200, header: withHeader(cacheHeader("max-age=30"), "Age", "0"), wantTTL: 30 * time.Second, cacheable: true},
		{name: "200 age reduces", status: 200, header: withHeader(cacheHeader("max-age=30"), "Age", "10"), wantTTL: 20 * time.Second, cacheable: true},
		{name: "200 age exhausted", status: 200, header: withHeader(cacheHeader("max-age=30"), "Age", "30"), cacheable: false},
		{name: "403 default", status: 403, header: make(http.Header), wantTTL: 5 * time.Second, cacheable: true},
		{name: "403 capped", status: 403, header: cacheHeader("max-age=300"), wantTTL: 5 * time.Second, cacheable: true},
		{name: "403 shorter", status: 403, header: cacheHeader("max-age=2"), wantTTL: 2 * time.Second, cacheable: true},
		{name: "403 zero", status: 403, header: cacheHeader("max-age=0"), cacheable: false},
		{name: "403 no-store", status: 403, header: cacheHeader("no-store"), cacheable: false},
		{name: "401", status: 401, header: cacheHeader("max-age=60"), cacheable: false},
		{name: "500", status: 500, header: cacheHeader("max-age=60"), cacheable: false},
		{name: "503", status: 503, header: cacheHeader("max-age=60"), cacheable: false},
		{name: "404", status: 404, header: cacheHeader("max-age=60"), cacheable: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotTTL, gotCacheable := netsec.CacheTTL(test.status, test.header)
			if gotCacheable != test.cacheable {
				t.Fatalf("CacheTTL() cacheable = %v, want %v", gotCacheable, test.cacheable)
			}
			if gotTTL != test.wantTTL {
				t.Fatalf("CacheTTL() ttl = %v, want %v", gotTTL, test.wantTTL)
			}
		})
	}
}

func TestExpirationBoundaries(t *testing.T) {
	now := time.Date(2026, time.November, 17, 12, 0, 0, 0, time.UTC)
	cache := netsec.New(netsec.WithClock(func() time.Time { return now }))
	req := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer alice"},
	})

	if !cache.Set(req, http.StatusOK, cacheHeader("max-age=60")) {
		t.Fatal("Set() = false, want true")
	}
	now = now.Add(60*time.Second - time.Nanosecond)
	if _, ok := cache.Get(req); !ok {
		t.Fatal("Get() before expiry = false, want true")
	}
	now = now.Add(time.Nanosecond)
	if _, ok := cache.Get(req); ok {
		t.Fatal("Get() at exact expiry = true, want false")
	}

	now = now.Add(time.Second)
	forbidden := newRequest(t, http.MethodGet, "https://example.com/denied", http.Header{
		"Authorization": {"Bearer alice"},
	})
	if !cache.Set(forbidden, http.StatusForbidden, nil) {
		t.Fatal("Set(403) = false, want true")
	}
	now = now.Add(5*time.Second - time.Nanosecond)
	if _, ok := cache.Get(forbidden); !ok {
		t.Fatal("Get(403) before expiry = false, want true")
	}
	now = now.Add(time.Nanosecond)
	if _, ok := cache.Get(forbidden); ok {
		t.Fatal("Get(403) at exact expiry = true, want false")
	}
}

func TestCachedHeaderIsCloned(t *testing.T) {
	t.Parallel()

	cache := netsec.New()
	req := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer alice"},
	})
	header := cacheHeader("max-age=60")
	header.Set("X-Auth-Subject", "alice")
	if !cache.Set(req, http.StatusOK, header) {
		t.Fatal("Set() = false, want true")
	}
	header.Set("X-Auth-Subject", "mutated")

	result, ok := cache.Get(req)
	if !ok {
		t.Fatal("Get() = false, want true")
	}
	if got := result.Header.Get("X-Auth-Subject"); got != "alice" {
		t.Fatalf("cached header = %q, want alice", got)
	}
	result.Header.Set("X-Auth-Subject", "changed by caller")

	second, ok := cache.Get(req)
	if !ok || second.Header.Get("X-Auth-Subject") != "alice" {
		t.Fatal("returned result exposed the cache's internal header")
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	t.Parallel()

	cache := netsec.New(netsec.WithCapacity(16))
	req := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer alice"},
	})
	header := cacheHeader("max-age=60")

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				cache.Set(req, http.StatusOK, header)
				cache.Get(req)
				cache.Len()
			}
		}()
	}
	wg.Wait()
}

func cacheHeader(cacheControl string) http.Header {
	header := make(http.Header)
	if cacheControl != "" {
		header.Set("Cache-Control", cacheControl)
	}
	return header
}

func withHeader(header http.Header, name, value string) http.Header {
	header.Set(name, value)
	return header
}
