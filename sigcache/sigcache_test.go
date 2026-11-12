package sigcache

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testKeyID    = "key-2026-11"
	testAudience = "download.example.org"
	testDigest   = "sha256:9f5f26e0f1f4f4f0c6d3b1a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9"
)

var testSecret = []byte("super-secret-HMAC-key-for-tests-0123456789")

// fakeClock is a controllable TrustedClock returning an uncertainty interval.
type fakeClock struct {
	mu          sync.Mutex
	base        time.Time
	uncertainty time.Duration
	err         error
}

func newFakeClock(base time.Time, uncertainty time.Duration) *fakeClock {
	return &fakeClock{base: base, uncertainty: uncertainty}
}

func (c *fakeClock) NowInterval() (time.Time, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return time.Time{}, time.Time{}, c.err
	}
	return c.base.Add(-c.uncertainty), c.base.Add(c.uncertainty), nil
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.base = t
	c.mu.Unlock()
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.base = c.base.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) rewind(d time.Duration) {
	c.mu.Lock()
	c.base = c.base.Add(-d)
	c.mu.Unlock()
}

func (c *fakeClock) setUncertainty(d time.Duration) {
	c.mu.Lock()
	c.uncertainty = d
	c.mu.Unlock()
}

func (c *fakeClock) fail(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

func newTestVerifier(t *testing.T, clock TrustedClock) (*Verifier, *Keyring) {
	t.Helper()
	keyring := NewKeyring()
	keyring.Add(testKeyID, testSecret)
	verifier, err := New(Config{Keyring: keyring, Clock: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return verifier, keyring
}

func validRequest(token, age, date string) Request {
	return Request{
		Token:        token,
		Audience:     testAudience,
		ObjectDigest: testDigest,
		AgeHeader:    age,
		DateHeader:   date,
	}
}

func denialReason(t *testing.T, err error) error {
	t.Helper()
	var denial *Denial
	if !errors.As(err, &denial) {
		t.Fatalf("expected *Denial, got %T: %v", err, err)
	}
	return denial.Reason
}

func reasonOf(err error) error {
	var denial *Denial
	if errors.As(err, &denial) {
		return denial.Reason
	}
	return err
}

// TestSignUsesRealHMACSHA256 independently recomputes the MAC and checks the
// token really is HMAC-SHA256 over the canonical binding payload.
func TestSignUsesRealHMACSHA256(t *testing.T) {
	expiry := time.Unix(1_900_000_000, 0).UTC()
	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("token should have two parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	wantMAC := hmac.New(sha256.New, testSecret)
	wantMAC.Write(payload)
	if !hmac.Equal(wantMAC.Sum(nil), signature) {
		t.Fatal("token signature is not the real HMAC-SHA256 of the payload")
	}
	if got := len(signature); got != sha256.Size {
		t.Fatalf("signature length = %d, want %d", got, sha256.Size)
	}
	wantPayload := "HMAC-SHA256\n" + testKeyID + "\n1900000000\n" + testAudience + "\n" + testDigest
	if string(payload) != wantPayload {
		t.Fatalf("canonical payload mismatch:\n got %q\nwant %q", payload, wantPayload)
	}
}

// TestAuthorizeCachesSignatureOnly proves the cache stores verification
// results while expiry is re-checked on every delivery.
func TestAuthorizeCachesSignatureOnly(t *testing.T) {
	base := time.Unix(1_800_000_000, 0).UTC()
	expiry := base.Add(time.Hour)
	clock := newFakeClock(base, time.Second)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	receipt, err := verifier.Authorize(validRequest(token, "0", ""))
	if err != nil {
		t.Fatalf("first Authorize: %v", err)
	}
	if receipt.CacheHit {
		t.Fatal("first call must verify, not hit cache")
	}
	if verifier.CacheLen() != 1 {
		t.Fatalf("cache length = %d, want 1", verifier.CacheLen())
	}

	receipt, err = verifier.Authorize(validRequest(token, "3600", ""))
	if err != nil {
		t.Fatalf("second Authorize: %v", err)
	}
	if !receipt.CacheHit {
		t.Fatal("second call should reuse the cached signature result")
	}

	// Move the trusted time past the deadline. Even though the signature is
	// cached and a forged Age header still claims freshness, delivery is refused.
	clock.set(expiry.Add(time.Second))
	_, err = verifier.Authorize(validRequest(token, "0", ""))
	if !errors.Is(denialReason(t, err), ErrExpired) {
		t.Fatalf("after deadline reason = %v, want ErrExpired", err)
	}
	if verifier.CacheLen() != 1 {
		t.Fatalf("expiry decision must not evict cache, len = %d", verifier.CacheLen())
	}

	// Rewind: the same cached signature authorizes again once the trusted
	// interval is back before the deadline.
	clock.rewind(3 * time.Second)
	if _, err = verifier.Authorize(validRequest(token, "999999", "")); err != nil {
		t.Fatalf("rewind Authorize: %v", err)
	}
}

// TestForgedNegativeAgeCannotExtendAuthorization feeds an intermediate
// cache's negative Age header both before and after expiry; the header never
// influences the decision.
func TestForgedNegativeAgeCannotExtendAuthorization(t *testing.T) {
	base := time.Unix(1_800_001_000, 0).UTC()
	expiry := base.Add(30 * time.Second)
	clock := newFakeClock(base, 0)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Warm the cache while valid, with a negative Age header already present.
	req := validRequest(token, "-4294967296", "Wed, 21 Oct 2015 07:28:00 GMT")
	receipt, err := verifier.Authorize(req)
	if err != nil {
		t.Fatalf("warm Authorize: %v", err)
	}
	if receipt.CacheHit {
		t.Fatal("warm-up must not be a cache hit")
	}

	clock.set(expiry)
	_, err = verifier.Authorize(req)
	denial := &Denial{}
	if !errors.As(err, &denial) || !errors.Is(denial.Reason, ErrExpired) {
		t.Fatalf("at deadline reason = %v, want ErrExpired", err)
	}
	if denial.Diagnostics.AgeParseError == nil {
		t.Fatal("negative Age must be recorded as an untrusted diagnostic parse error")
	}
	if denial.Diagnostics.DateParseError != nil || denial.Diagnostics.DateParsed.IsZero() {
		t.Fatalf("Date diagnostic parse failed: %v", denial.Diagnostics.DateParseError)
	}
	if !denial.Diagnostics.CacheHit {
		t.Fatal("expired delivery should still hit the signature cache")
	}

	clock.advance(time.Second)
	_, err = verifier.Authorize(validRequest(token, "-1", ""))
	if !errors.Is(denialReason(t, err), ErrExpired) {
		t.Fatalf("post-deadline with negative Age reason = %v, want ErrExpired", err)
	}

	// A garbage Age header is likewise ignored.
	_, err = verifier.Authorize(validRequest(token, "not-a-number", ""))
	if !errors.Is(denialReason(t, err), ErrExpired) {
		t.Fatalf("garbage Age reason = %v, want ErrExpired", err)
	}
}

// TestRewoundClockRechecksExpiryOnCacheHit jumps time forward past expiry and
// then rewinds the clock, verifying every delivery consults the trusted
// interval rather than a cached allow decision.
func TestRewoundClockRechecksExpiryOnCacheHit(t *testing.T) {
	base := time.Unix(1_800_002_000, 0).UTC()
	expiry := base.Add(10 * time.Second)
	clock := newFakeClock(base, 0)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err = verifier.Authorize(validRequest(token, "0", "")); err != nil {
		t.Fatalf("initial Authorize: %v", err)
	}

	clock.set(expiry.Add(time.Minute))
	if _, err = verifier.Authorize(validRequest(token, "0", "")); !errors.Is(denialReason(t, err), ErrExpired) {
		t.Fatalf("future Authorize: %v", err)
	}

	// Attacker rewinds the trusted clock; no cached allow exists, but the
	// cached signature must still be re-gated by the fresh interval.
	clock.rewind(time.Minute + time.Second)
	receipt, err := verifier.Authorize(validRequest(token, "9999", ""))
	if err != nil {
		t.Fatalf("rewound Authorize: %v", err)
	}
	if !receipt.CacheHit || !receipt.Expiry.Equal(expiry) {
		t.Fatalf("rewound receipt = %+v", receipt)
	}
}

// TestExpiryBoundaryConcurrentRequests hammers the verifier concurrently while
// the clock flips across the deadline: any request whose trusted interval
// touches the deadline is refused, cache hits included.
func TestExpiryBoundaryConcurrentRequests(t *testing.T) {
	base := time.Unix(1_800_003_000, 0).UTC()
	expiry := base.Add(50 * time.Millisecond)
	clock := newFakeClock(base, 5*time.Millisecond)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	const goroutines = 32
	const iterations = 200
	var allowed, expired, other int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				_, err := verifier.Authorize(validRequest(token, "0", ""))
				switch {
				case err == nil:
					atomic.AddInt64(&allowed, 1)
				case errors.Is(reasonOf(err), ErrExpired):
					atomic.AddInt64(&expired, 1)
				default:
					atomic.AddInt64(&other, 1)
				}
			}
		}()
	}

	close(start)
	clock.set(expiry)
	wg.Wait()

	if other != 0 {
		t.Fatalf("unexpected non-expiry denials: %d", other)
	}
	if allowed != 0 {
		t.Fatalf("no request may be allowed once latest == expiry, allowed = %d", allowed)
	}
	if expired != goroutines*iterations {
		t.Fatalf("expired = %d, want %d", expired, goroutines*iterations)
	}

	// Well before the boundary with uncertainty, every request passes.
	clock.set(expiry.Add(-time.Second))
	for i := 0; i < 64; i++ {
		if _, err := verifier.Authorize(validRequest(token, "0", "")); err != nil {
			t.Fatalf("pre-boundary request %d: %v", i, err)
		}
	}
}

// TestIndefiniteClockIsRefused covers clock errors, inverted intervals and
// intervals whose uncertainty straddles the deadline.
func TestIndefiniteClockIsRefused(t *testing.T) {
	base := time.Unix(1_800_004_000, 0).UTC()
	expiry := base.Add(time.Second)
	clock := newFakeClock(base, 0)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err = verifier.Authorize(validRequest(token, "0", "")); err != nil {
		t.Fatalf("baseline Authorize: %v", err)
	}

	clock.fail(errors.New("NTP source unreachable"))
	_, err = verifier.Authorize(validRequest(token, "0", ""))
	if !errors.Is(denialReason(t, err), ErrIndefiniteClock) {
		t.Fatalf("clock error reason = %v, want ErrIndefiniteClock", err)
	}

	clock.fail(nil)
	// Uncertainty interval straddles the deadline: latest >= expiry.
	clock.setUncertainty(2 * time.Second)
	_, err = verifier.Authorize(validRequest(token, "0", ""))
	if !errors.Is(denialReason(t, err), ErrExpired) {
		t.Fatalf("straddling interval reason = %v, want ErrExpired", err)
	}

	// A definite interval fully before the deadline still passes.
	clock.setUncertainty(500 * time.Millisecond)
	if _, err = verifier.Authorize(validRequest(token, "0", "")); err != nil {
		t.Fatalf("definite interval Authorize: %v", err)
	}

	// Negative uncertainty is rejected by SystemClock.
	bad := SystemClock{Uncertainty: -time.Second}
	if _, _, err = bad.NowInterval(); !errors.Is(err, ErrIndefiniteClock) {
		t.Fatalf("negative uncertainty reason = %v, want ErrIndefiniteClock", err)
	}
}

// TestRevocationInvalidatesCache warms the cache, revokes the key, and
// asserts the cached result can no longer authorize the URL.
func TestRevocationInvalidatesCache(t *testing.T) {
	base := time.Unix(1_800_005_000, 0).UTC()
	expiry := base.Add(time.Hour)
	clock := newFakeClock(base, 0)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err = verifier.Authorize(validRequest(token, "0", "")); err != nil {
		t.Fatalf("warm Authorize: %v", err)
	}
	if verifier.CacheLen() != 1 {
		t.Fatalf("cache len = %d, want 1", verifier.CacheLen())
	}

	verifier.RevokeKey(testKeyID)
	if verifier.CacheLen() != 0 {
		t.Fatalf("cache len after revoke = %d, want 0", verifier.CacheLen())
	}
	_, err = verifier.Authorize(validRequest(token, "0", ""))
	if !errors.Is(denialReason(t, err), ErrUnknownKey) {
		t.Fatalf("post-revoke reason = %v, want ErrUnknownKey", err)
	}

	// Re-adding a fresh key generation must not resurrect the old signature.
	verifier.keyring.Add(testKeyID, []byte("rotated-secret-with-different-bytes-99"))
	_, err = verifier.Authorize(validRequest(token, "0", ""))
	if !errors.Is(denialReason(t, err), ErrBadSignature) {
		t.Fatalf("stale token after key rotation reason = %v, want ErrBadSignature", err)
	}
}

// TestSignatureBindingsCannotBeSubstituted verifies digest and audience are
// cryptographically bound: changing either request-side value is rejected
// even when the signature itself is cached as valid.
func TestSignatureBindingsCannotBeSubstituted(t *testing.T) {
	base := time.Unix(1_800_006_000, 0).UTC()
	expiry := base.Add(time.Hour)
	clock := newFakeClock(base, 0)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Warm the cache.
	if _, err = verifier.Authorize(validRequest(token, "0", "")); err != nil {
		t.Fatalf("warm Authorize: %v", err)
	}

	swappedAudience := validRequest(token, "0", "")
	swappedAudience.Audience = "evil.example.net"
	if _, err = verifier.Authorize(swappedAudience); !errors.Is(denialReason(t, err), ErrAudienceMismatch) {
		t.Fatalf("audience swap reason = %v, want ErrAudienceMismatch", err)
	}

	swappedDigest := validRequest(token, "0", "")
	swappedDigest.ObjectDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if _, err = verifier.Authorize(swappedDigest); !errors.Is(denialReason(t, err), ErrDigestMismatch) {
		t.Fatalf("digest swap reason = %v, want ErrDigestMismatch", err)
	}

	// A token with a flipped byte fails HMAC and is never cached as valid.
	tampered := tamperSignature(t, token)
	if _, err = verifier.Authorize(validRequest(tampered, "0", "")); !errors.Is(denialReason(t, err), ErrBadSignature) {
		t.Fatalf("tampered token reason = %v, want ErrBadSignature", err)
	}
	if verifier.CacheLen() != 1 {
		t.Fatalf("invalid signatures must not populate cache, len = %d", verifier.CacheLen())
	}

	// Unknown key id and malformed tokens get classified reasons.
	otherToken, err := Sign("other-key", testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign other: %v", err)
	}
	if _, err = verifier.Authorize(validRequest(otherToken, "0", "")); !errors.Is(denialReason(t, err), ErrUnknownKey) {
		t.Fatalf("unknown key reason = %v, want ErrUnknownKey", err)
	}
	if _, err = verifier.Authorize(validRequest("garbage", "0", "")); !errors.Is(denialReason(t, err), ErrMalformedToken) {
		t.Fatalf("malformed reason = %v, want ErrMalformedToken", err)
	}
}

func tamperSignature(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[0] ^= 0xFF
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join(parts, ".")
}

// TestFromHTTPRequestCopiesDiagnostics confirms the integration helper wires
// Age/Date through as diagnostics without authorizing on their basis.
func TestFromHTTPRequestCopiesDiagnostics(t *testing.T) {
	base := time.Unix(1_800_007_000, 0).UTC()
	expiry := base.Add(time.Minute)
	clock := newFakeClock(base, 0)
	verifier, _ := newTestVerifier(t, clock)

	token, err := Sign(testKeyID, testSecret, testAudience, testDigest, expiry)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	header := http.Header{}
	header.Set("Age", "120")
	header.Set("Date", "Wed, 21 Oct 2015 07:28:00 GMT")
	req := FromHTTPRequest(token, testAudience, testDigest, header)
	if req.AgeHeader != "120" || req.DateHeader == "" {
		t.Fatalf("headers not copied: %+v", req)
	}
	if _, err = verifier.Authorize(req); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
}

// TestCacheEvictionBoundsMemory verifies the cap evicts recomputable entries.
func TestCacheEvictionBoundsMemory(t *testing.T) {
	base := time.Unix(1_800_008_000, 0).UTC()
	clock := newFakeClock(base, 0)
	keyring := NewKeyring()
	keyring.Add(testKeyID, testSecret)
	verifier, err := New(Config{Keyring: keyring, Clock: clock, MaxEntries: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 5; i++ {
		token, err := Sign(testKeyID, testSecret, testAudience,
			fmt.Sprintf("sha256:%064d", i), base.Add(time.Hour))
		if err != nil {
			t.Fatalf("Sign %d: %v", i, err)
		}
		req := Request{
			Token:        token,
			Audience:     testAudience,
			ObjectDigest: fmt.Sprintf("sha256:%064d", i),
		}
		if _, err = verifier.Authorize(req); err != nil {
			t.Fatalf("Authorize %d: %v", i, err)
		}
	}
	if verifier.CacheLen() > 2 {
		t.Fatalf("cache len = %d exceeds cap", verifier.CacheLen())
	}
}
