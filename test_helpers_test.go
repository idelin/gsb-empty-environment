package netsec

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock {
	return &testClock{now: now}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type testSTS struct {
	mu      sync.Mutex
	calls   int
	fetch   func(context.Context, Identity) (*STSResult, error)
	secrets []*MemorySecret
}

func (s *testSTS) Fetch(ctx context.Context, identity Identity) (*STSResult, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.fetch(ctx, identity)
}

func (s *testSTS) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *testSTS) recordSecret(secret *MemorySecret) *MemorySecret {
	s.mu.Lock()
	s.secrets = append(s.secrets, secret)
	s.mu.Unlock()
	return secret
}

func (s *testSTS) Secret(i int) *MemorySecret {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secrets[i]
}

func (s *testSTS) SecretCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.secrets)
}

var testSequence atomic.Uint64

func testIdentity(tenantID, roleID string) Identity {
	return Identity{
		TenantID:           tenantID,
		RoleID:             roleID,
		PolicyDigest:       DigestBytes([]byte("policy")),
		SessionTagsDigest:  SessionTagsDigest([]Tag{{Key: "env", Value: "prod"}}),
		ExternalIdentityID: "external-identity",
		Audience:           "sts.example",
		ProviderGeneration: 0,
	}
}

func testResult(identity Identity, clock *testClock, ttl time.Duration, secretID, accessKey, material string) *STSResult {
	now := clock.Now()
	return &STSResult{
		AccessKeyID:       accessKey,
		Secret:            NewMemorySecret(secretID, []byte(material)),
		SessionToken:      "session-token-" + secretID,
		RoleID:            identity.RoleID,
		TenantID:          identity.TenantID,
		Audience:          identity.Audience,
		SessionTagsDigest: identity.SessionTagsDigest,
		IssuedAt:          now,
		ExpiresAt:         now.Add(ttl),
	}
}

func newTestCache(t *testing.T, provider Provider, options ...Option) *Cache {
	t.Helper()
	cache, err := NewCache(provider, options...)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache
}

func mustBorrow(t *testing.T, cache *Cache, identity Identity) *Lease {
	t.Helper()
	lease, err := cache.Borrow(context.Background(), identity)
	if err != nil {
		t.Fatalf("Borrow(%s/%s): %v", identity.TenantID, identity.RoleID, err)
	}
	return lease
}

func mustReveal(t *testing.T, handle SecretHandle) string {
	t.Helper()
	memory, ok := handle.(*MemorySecret)
	if !ok {
		t.Fatalf("secret handle is %T, want *MemorySecret", handle)
	}
	material, err := memory.Reveal()
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	return string(material)
}

func waitForWaiters(t *testing.T, cache *Cache, key Digest, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		flight := cache.flights[key]
		got := 0
		if flight != nil {
			got = flight.waiters
		}
		cache.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d waiters on %s", want, key)
}

func waitFor(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func uniqueSecretID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, testSequence.Add(1))
}
