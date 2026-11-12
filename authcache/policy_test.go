package authcache

import (
	"net/http"
	"testing"
	"time"
)

func makeResponder(status int, header http.Header) func(r *http.Request) *http.Response {
	return func(r *http.Request) *http.Response {
		return makeResponse(status, header, "body")
	}
}

func TestStatusPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		status     int
		header     http.Header
		wantCached bool
	}{
		{name: "200 cached", status: http.StatusOK, header: http.Header{}, wantCached: true},
		{name: "403 cached", status: http.StatusForbidden, header: http.Header{}, wantCached: true},
		{name: "401 never cached", status: http.StatusUnauthorized, header: http.Header{}, wantCached: false},
		{name: "500 never cached", status: http.StatusInternalServerError, header: http.Header{}, wantCached: false},
		{name: "503 never cached", status: http.StatusServiceUnavailable, header: http.Header{}, wantCached: false},
		{name: "200 no-store", status: http.StatusOK, header: http.Header{"Cache-Control": []string{"no-store"}}, wantCached: false},
		{name: "200 no-cache", status: http.StatusOK, header: http.Header{"Cache-Control": []string{"no-cache"}}, wantCached: false},
		{name: "200 max-age zero", status: http.StatusOK, header: http.Header{"Cache-Control": []string{"max-age=0"}}, wantCached: false},
		{name: "403 max-age zero", status: http.StatusForbidden, header: http.Header{"Cache-Control": []string{"max-age=0"}}, wantCached: false},
		{name: "vary wildcard", status: http.StatusOK, header: http.Header{"Vary": []string{"*"}}, wantCached: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache, fake, _ := newTestCache(t, makeResponder(tc.status, tc.header))
			target := "https://gateway.example/x"

			if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
				t.Fatalf("first Authorize: %v", err)
			}
			if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
				t.Fatalf("second Authorize: %v", err)
			}
			wantCalls := 2
			if tc.wantCached {
				wantCalls = 1
			}
			if fake.calls != wantCalls {
				t.Fatalf("authorizer calls = %d, want %d", fake.calls, wantCalls)
			}
			if got := cache.entryCount() > 0; got != tc.wantCached {
				t.Fatalf("stored entry = %v, want %v", got, tc.wantCached)
			}
		})
	}
}

func TestTTLBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("403 expires at five seconds", func(t *testing.T) {
		t.Parallel()
		cache, fake, clock := newTestCache(t, makeResponder(http.StatusForbidden, http.Header{}))
		target := "https://gateway.example/denied"

		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		clock.advance(5 * time.Second)
		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize at boundary: %v", err)
		}
		if fake.calls != 2 {
			t.Fatalf("at exactly 5s the 403 entry must be expired, calls = %d, want 2", fake.calls)
		}
	})

	t.Run("403 stays cached one millisecond before boundary", func(t *testing.T) {
		t.Parallel()
		cache, fake, clock := newTestCache(t, makeResponder(http.StatusForbidden, http.Header{}))
		target := "https://gateway.example/denied2"

		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		clock.advance(4999 * time.Millisecond)
		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize before boundary: %v", err)
		}
		if fake.calls != 1 {
			t.Fatalf("403 must still be cached 1ms before expiry, calls = %d", fake.calls)
		}
	})

	t.Run("200 honors max-age under cap", func(t *testing.T) {
		t.Parallel()
		header := http.Header{"Cache-Control": []string{"max-age=10"}}
		cache, fake, clock := newTestCache(t, makeResponder(http.StatusOK, header))
		target := "https://gateway.example/ok10"

		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		clock.advance(10 * time.Second)
		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize at boundary: %v", err)
		}
		if fake.calls != 2 {
			t.Fatalf("max-age=10 must expire at 10s, calls = %d, want 2", fake.calls)
		}
	})

	t.Run("200 caps max-age at sixty seconds", func(t *testing.T) {
		t.Parallel()
		header := http.Header{"Cache-Control": []string{"max-age=600"}}
		cache, fake, clock := newTestCache(t, makeResponder(http.StatusOK, header))
		target := "https://gateway.example/okcap"

		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		clock.advance(60 * time.Second)
		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize at cap: %v", err)
		}
		if fake.calls != 2 {
			t.Fatalf("max-age must be capped at 60s, calls = %d, want 2", fake.calls)
		}
	})

	t.Run("200 defaults to sixty seconds without max-age", func(t *testing.T) {
		t.Parallel()
		cache, fake, clock := newTestCache(t, makeResponder(http.StatusOK, http.Header{}))
		target := "https://gateway.example/okdefault"

		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		clock.advance(59 * time.Second)
		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize before default expiry: %v", err)
		}
		clock.advance(time.Second)
		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize at default expiry: %v", err)
		}
		if fake.calls != 2 {
			t.Fatalf("default 200 TTL must be 60s, calls = %d, want 2", fake.calls)
		}
	})

	t.Run("403 caps service max-age at five seconds", func(t *testing.T) {
		t.Parallel()
		header := http.Header{"Cache-Control": []string{"max-age=30"}}
		cache, fake, clock := newTestCache(t, makeResponder(http.StatusForbidden, header))
		target := "https://gateway.example/deniedcap"

		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		clock.advance(5 * time.Second)
		if _, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer token")); err != nil {
			t.Fatalf("Authorize at cap: %v", err)
		}
		if fake.calls != 2 {
			t.Fatalf("403 TTL must be capped at 5s, calls = %d, want 2", fake.calls)
		}
	})
}

func TestCleanupEvictsExpired(t *testing.T) {
	t.Parallel()

	cache, _, clock := newTestCache(t, makeResponder(http.StatusForbidden, http.Header{}))
	if _, err := cache.Authorize(newRequest(http.MethodGet, "https://gateway.example/c", "Bearer token")); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if cache.entryCount() != 1 {
		t.Fatalf("entry count = %d, want 1", cache.entryCount())
	}
	clock.advance(5 * time.Second)
	if evicted := cache.Cleanup(); evicted != 1 {
		t.Fatalf("Cleanup evicted = %d, want 1", evicted)
	}
	if cache.entryCount() != 0 {
		t.Fatalf("entry count after cleanup = %d, want 0", cache.entryCount())
	}
}

func TestStartCleanupStops(t *testing.T) {
	t.Parallel()

	cache, _, _ := newTestCache(t, makeResponder(http.StatusForbidden, http.Header{}))
	if _, err := cache.Authorize(newRequest(http.MethodGet, "https://gateway.example/bg", "Bearer token")); err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	stop := cache.StartCleanup(10 * time.Millisecond)
	close(stop)
	// Closing stop must terminate the goroutine; the cache remains usable.
	if _, err := cache.Authorize(newRequest(http.MethodGet, "https://gateway.example/bg2", "Bearer token")); err != nil {
		t.Fatalf("cache unusable after stopping cleanup: %v", err)
	}
}
