package jwtmw

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestUnknownKidStorm_CoalescesIntoSingleRefresh fires many concurrent
// requests with unknown kids while the JWKS fetch is held open. All requests
// must share one in-flight refresh, producing exactly one HTTP fetch.
func TestUnknownKidStorm_CoalescesIntoSingleRefresh(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	release := make(chan struct{})
	h.fetchWait = release
	v := newVerifier(t, clock, h)

	rsaKey := mustRSAKey(t)
	newKey := mustRSAKey(t)

	// Seed an initial document containing only key-1.
	h.fetchWait = nil
	h.setKeys(jwkFromPublic("key-1", &rsaKey.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Publish key-2 in JWKS but make every fetch block until release.
	h.setKeys(
		jwkFromPublic("key-1", &rsaKey.PublicKey),
		jwkFromPublic("key-2", &newKey.PublicKey),
	)
	// Start from a clean rate-limit window so the first unknown kid fetches.
	v.resetRateLimit()
	h.fetchWait = release
	h.started = make(chan struct{})

	const n = 50
	var wg sync.WaitGroup
	statuses := make([]int, n)
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			token := signToken(t, "RS256", "key-2", newKey, baseClaims(clock))
			statuses[i] = doRequest(t, v, token).Code
		}()
	}
	close(start)

	// Wait until the coalesced fetch actually enters the blocked handler.
	select {
	case <-h.started:
	case <-time.After(5 * time.Second):
		t.Fatal("storm never initiated a JWKS fetch")
	}
	close(release)
	wg.Wait()

	// Seed fetch + exactly one coalesced refresh, regardless of concurrency.
	if got := h.fetches.Load(); got != 2 {
		t.Fatalf("expected 2 total JWKS fetches (seed + 1 coalesced), got %d", got)
	}
	for i, code := range statuses {
		if code != http.StatusOK {
			t.Fatalf("request %d: expected 200 after coalesced refresh, got %d", i, code)
		}
	}

	// A follow-up unknown kid within the rate-limit window must not fetch again.
	token := signToken(t, "RS256", "key-ghost", mustRSAKey(t), baseClaims(clock))
	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("rate-limited unknown kid: expected 401, got %d", rec.Code)
	}
	if got := h.fetches.Load(); got != 2 {
		t.Fatalf("rate limiter allowed an extra fetch: total=%d", got)
	}
}

// TestRateLimit_ResetAfterInterval verifies throttling lifts once
// MinRefreshInterval passes while the cache is still fresh.
func TestRateLimit_ResetAfterInterval(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	rsaKey := mustRSAKey(t)
	h.setKeys(jwkFromPublic("key-1", &rsaKey.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	token := signToken(t, "RS256", "ghost", mustRSAKey(t), baseClaims(clock))

	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("first unknown kid triggers refresh then 401, got %d", rec.Code)
	}
	fetchesAfterFirst := h.fetches.Load()

	clock.advance(30 * time.Second) // still inside MinRefreshInterval (1m).
	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("throttled request expected 401, got %d", rec.Code)
	}
	if h.fetches.Load() != fetchesAfterFirst {
		t.Fatalf("refresh not throttled: fetches went %d -> %d", fetchesAfterFirst, h.fetches.Load())
	}
	if _, err := v.Verify(t.Context(), token); !errors.Is(err, ErrRefreshThrottled) {
		t.Fatalf("expected ErrRefreshThrottled, got %v", err)
	}

	clock.advance(31 * time.Second) // past the 1m interval; cache still fresh.
	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-interval unknown kid expected 401, got %d", rec.Code)
	}
	if h.fetches.Load() <= fetchesAfterFirst {
		t.Fatalf("expected a new fetch after interval, got %d", h.fetches.Load())
	}
}

func (v *Verifier) inflightRefs() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inflight == nil {
		return 0
	}
	return 1
}

func (v *Verifier) resetRateLimit() {
	v.mu.Lock()
	v.lastAttempt = time.Time{}
	v.mu.Unlock()
}

// TestKeyRotation_GracePeriod covers the full rotation lifecycle: the old key
// keeps validating inside the grace window and is rejected once it ends.
func TestKeyRotation_GracePeriod(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)

	oldKey := mustRSAKey(t)
	newKey := mustRSAKey(t)
	h.setKeys(jwkFromPublic("old", &oldKey.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	oldToken := signToken(t, "RS256", "old", oldKey, baseClaims(clock))
	if rec := doRequest(t, v, oldToken); rec.Code != http.StatusOK {
		t.Fatalf("old key before rotation: expected 200, got %d", rec.Code)
	}

	// Rotate: new JWKS only contains the new key. A request for the new kid
	// drives the refresh; the old key becomes retired with a grace deadline.
	h.setKeys(jwkFromPublic("new", &newKey.PublicKey))
	v.resetRateLimit()
	newToken := signToken(t, "RS256", "new", newKey, baseClaims(clock))
	if rec := doRequest(t, v, newToken); rec.Code != http.StatusOK {
		t.Fatalf("new key after rotation: expected 200, got %d", rec.Code)
	}

	v.mu.Lock()
	oldEntry, oldPresent := v.keys["old"]
	v.mu.Unlock()
	if !oldPresent || oldEntry.retiredAt.IsZero() {
		t.Fatal("old key should be retained in retired state after rotation")
	}

	// Inside the 30s grace window the old token still validates.
	clock.advance(20 * time.Second)
	if rec := doRequest(t, v, oldToken); rec.Code != http.StatusOK {
		t.Fatalf("old key inside grace period: expected 200, got %d", rec.Code)
	}

	// Exactly at the grace boundary it remains accepted (<=).
	clock.advance(10 * time.Second)
	if rec := doRequest(t, v, oldToken); rec.Code != http.StatusOK {
		t.Fatalf("old key at grace boundary: expected 200, got %d", rec.Code)
	}

	// After the grace window ends it is rejected and evicted.
	clock.advance(time.Second)
	if rec := doRequest(t, v, oldToken); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old key after grace period: expected 401, got %d", rec.Code)
	}
	v.mu.Lock()
	_, stillPresent := v.keys["old"]
	v.mu.Unlock()
	if stillPresent {
		t.Fatal("retired key should be evicted after the grace period")
	}
}

// TestRefreshFailure_UsesUnexpiredCache ensures an upstream outage while the
// cached document is still within TTL does not disrupt verification.
func TestRefreshFailure_UsesUnexpiredCache(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)

	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Upstream JWKS starts failing. The cache is still fresh, so valid
	// tokens keep working and the cached keys are not disturbed.
	h.setFail(true)
	clock.advance(10 * time.Second)
	token := signToken(t, "RS256", "k", key, baseClaims(clock))
	for i := 0; i < 3; i++ {
		if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
			t.Fatalf("request %d during outage with fresh cache: expected 200, got %d", i, rec.Code)
		}
	}

	// Even an unknown kid cannot damage the working cache on failed refresh.
	ghost := signToken(t, "RS256", "ghost", mustRSAKey(t), baseClaims(clock))
	if rec := doRequest(t, v, ghost); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown kid during outage: expected 401, got %d", rec.Code)
	}

	// Recovery restores normal fetching without losing the key.
	h.setFail(false)
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("recovery refresh: %v", err)
	}
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("after recovery: expected 200, got %d", rec.Code)
	}
}

// TestExpiredCache_FailureReturns503_RecoveryReturns200 exercises the TTL
// boundary: once the document expires, a failed refresh yields 503, and a
// successful refresh restores 200.
func TestExpiredCache_FailureReturns503_RecoveryReturns200(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)

	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	token := signToken(t, "RS256", "k", key, baseClaims(clock))

	// At exactly the TTL boundary the cache is still considered fresh.
	clock.advance(time.Minute)
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("at TTL boundary: expected 200, got %d", rec.Code)
	}

	// One second later the cache is stale. With the upstream down, the
	// gateway cannot verify and must answer 503 rather than fail open.
	clock.advance(time.Second)
	h.setFail(true)
	rec := doRequest(t, v, token)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expired cache + failed refresh: expected 503, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("expected WWW-Authenticate header on 503")
	}

	// Bringing the upstream back restores verification on the next request.
	h.setFail(false)
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("after upstream recovery: expected 200, got %d", rec.Code)
	}
}

// TestExpiredCache_RefreshBypassesRateLimit confirms the rate limiter never
// blocks the mandatory refresh of an expired document.
func TestExpiredCache_RefreshBypassesRateLimit(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Burn the rate-limit budget with an unknown-kid request.
	ghost := signToken(t, "RS256", "ghost", mustRSAKey(t), baseClaims(clock))
	_ = doRequest(t, v, ghost)
	fetches := h.fetches.Load()

	// Expire the document without advancing past MinRefreshInterval, then fail
	// the upstream: the refresh must still be attempted (not throttled).
	clock.advance(time.Minute + time.Second)
	h.setFail(true)
	token := signToken(t, "RS256", "k", key, baseClaims(clock))
	rec := doRequest(t, v, token)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if h.fetches.Load() <= fetches {
		t.Fatal("expired-cache refresh was wrongly throttled")
	}
}

func TestJWKSErrorMapping(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)

	// Nothing fetched yet: a request triggers a failing fetch and surfaces 503.
	h.setFail(true)
	token := signToken(t, "RS256", "k", mustRSAKey(t), baseClaims(clock))
	if rec := doRequest(t, v, token); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("cold cache + failed fetch: expected 503, got %d", rec.Code)
	}

	_, err := v.Verify(t.Context(), token)
	if !errors.Is(err, ErrCacheExpired) {
		t.Fatalf("expected ErrCacheExpired, got %v", err)
	}
}

// TestConcurrentRefreshIsSerialisable under -race ensures internal state stays
// consistent when refreshes and lookups interleave.
func TestConcurrentRefreshIsSerialisable(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))

	var wg sync.WaitGroup
	v.resetRateLimit()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				// Known-kid requests hit the cache without any fetch.
				token := signToken(t, "RS256", "k", key, baseClaims(clock))
				_ = doRequest(t, v, token)
			} else {
				// Unknown kids trigger coalesced, rate-limited refreshes.
				token := signToken(t, "RS256", "ghost", mustRSAKey(t), baseClaims(clock))
				_ = doRequest(t, v, token)
			}
		}(i)
	}
	wg.Wait()

	if got := h.fetches.Load(); got != 1 {
		t.Fatalf("expected exactly 1 throttled+coalesced fetch, got %d", got)
	}
}
