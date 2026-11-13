package netsec

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSameRoleDifferentTenantsGetDistinctSecrets(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("tenant-" + identity.TenantID)
		result := testResult(identity, clock, time.Hour, secretID, "AKIA-SAME-ACCESS-KEY", "material-"+identity.TenantID)
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	a := testIdentity("tenant-a", "role-exact-1")
	b := testIdentity("tenant-b", "role-exact-1")
	if a.CacheKey() == b.CacheKey() {
		t.Fatal("different tenants produced the same canonical cache key")
	}

	leaseA := mustBorrow(t, cache, a)
	leaseB := mustBorrow(t, cache, b)
	defer leaseA.Release()
	defer leaseB.Release()

	if leaseA.AccessKeyID() != leaseB.AccessKeyID() {
		t.Fatalf("test setup: access key IDs differ: %q != %q", leaseA.AccessKeyID(), leaseB.AccessKeyID())
	}
	if got := mustReveal(t, leaseA.Secret()); got != "material-tenant-a" {
		t.Fatalf("tenant-a secret = %q", got)
	}
	if got := mustReveal(t, leaseB.Secret()); got != "material-tenant-b" {
		t.Fatalf("tenant-b secret = %q", got)
	}
	if sts.Calls() != 2 {
		t.Fatalf("STS calls = %d, want 2", sts.Calls())
	}
	if cache.Len() != 2 {
		t.Fatalf("cache entries = %d, want 2", cache.Len())
	}
}

func TestAccessKeyIDDoesNotIdentifySecret(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("secret")
		result := testResult(identity, clock, time.Hour, secretID, "AKIA-COLLIDING", "material-"+identity.ExternalIdentityID)
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	a := testIdentity("tenant-a", "role")
	b := testIdentity("tenant-a", "role")
	b.ExternalIdentityID = "another-external-identity"

	leaseA := mustBorrow(t, cache, a)
	leaseB := mustBorrow(t, cache, b)
	defer leaseA.Release()
	defer leaseB.Release()

	if leaseA.AccessKeyID() != leaseB.AccessKeyID() {
		t.Fatalf("test setup: access key IDs differ")
	}
	if mustReveal(t, leaseA.Secret()) == mustReveal(t, leaseB.Secret()) {
		t.Fatal("same accessKeyID with different secret material was reused")
	}
	if sts.Calls() != 2 {
		t.Fatalf("STS calls = %d, want 2", sts.Calls())
	}
}

func TestSessionTagOrderSharesCanonicalIdentity(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("tags")
		result := testResult(identity, clock, time.Hour, secretID, "AKIA", "material")
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	tagsA := []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "security"}, {Key: "region", Value: "cn"}}
	tagsB := []Tag{{Key: "region", Value: "cn"}, {Key: "env", Value: "prod"}, {Key: "team", Value: "security"}}
	if SessionTagsDigest(tagsA) != SessionTagsDigest(tagsB) {
		t.Fatal("tag order changed the canonical tag digest")
	}

	a := testIdentity("tenant-a", "role")
	b := a
	a.SessionTagsDigest = SessionTagsDigest(tagsA)
	b.SessionTagsDigest = SessionTagsDigest(tagsB)
	if a.CacheKey() != b.CacheKey() {
		t.Fatal("canonically equal tag sets produced different cache keys")
	}

	leaseA := mustBorrow(t, cache, a)
	leaseB := mustBorrow(t, cache, b)
	defer leaseA.Release()
	defer leaseB.Release()

	if sts.Calls() != 1 {
		t.Fatalf("STS calls = %d, want 1", sts.Calls())
	}
	if cache.Len() != 1 {
		t.Fatalf("cache entries = %d, want 1", cache.Len())
	}
}

func TestPolicyTighteningInvalidatesWithoutRebindingLease(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("policy")
		result := testResult(identity, clock, time.Hour, secretID, "AKIA", "material-"+identity.PolicyDigest.String()[:8])
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	policyA := DigestBytes([]byte("policy-a"))
	policyB := DigestBytes([]byte("policy-b"))
	identityA := testIdentity("tenant-a", "role")
	identityA.PolicyDigest = policyA
	identityB := identityA
	identityB.PolicyDigest = policyB

	leaseA := mustBorrow(t, cache, identityA)
	oldClone := leaseA.Secret()
	if got := mustReveal(t, oldClone); !strings.HasPrefix(got, "material-") {
		t.Fatalf("old secret = %q", got)
	}

	cache.TightenPolicy("tenant-a", "role", policyB)
	if !sts.Secret(0).State().Destroyed {
		t.Fatal("policy tightening did not destroy the cached master secret")
	}

	if _, err := cache.Borrow(context.Background(), identityA); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("old policy borrow error = %v, want ErrPolicyMismatch", err)
	}
	if sts.Calls() != 1 {
		t.Fatalf("old policy caused an STS call: calls = %d", sts.Calls())
	}

	leaseB := mustBorrow(t, cache, identityB)
	defer leaseB.Release()
	if sts.Calls() != 2 {
		t.Fatalf("new policy STS calls = %d, want 2", sts.Calls())
	}
	if leaseA.Identity() != identityA {
		t.Fatal("existing lease identity was rebound after policy change")
	}
	if got := mustReveal(t, oldClone); !strings.HasPrefix(got, "material-") {
		t.Fatalf("old lease became unusable after policy change: %q", got)
	}

	if err := leaseA.Release(); err != nil {
		t.Fatalf("old lease Release: %v", err)
	}
	state := oldClone.(*MemorySecret).State()
	if !state.Destroyed || !state.Zeroed {
		t.Fatalf("old lease was not zeroized on return: %+v", state)
	}
}

func TestGenerationAndRoleRevocationBlockNewBorrows(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("generation")
		result := testResult(identity, clock, time.Hour, secretID, "AKIA", "material-"+secretID)
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	generation0 := testIdentity("tenant-a", "role")
	lease0 := mustBorrow(t, cache, generation0)
	defer lease0.Release()
	if cache.Generation() != 0 {
		t.Fatalf("initial generation = %d", cache.Generation())
	}

	if got := cache.BumpGeneration(); got != 1 {
		t.Fatalf("BumpGeneration = %d, want 1", got)
	}
	if cache.Len() != 0 {
		t.Fatalf("generation bump left %d entries", cache.Len())
	}
	if !sts.Secret(0).State().Destroyed {
		t.Fatal("generation bump did not destroy the old master secret")
	}
	if _, err := cache.Borrow(context.Background(), generation0); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("old generation error = %v, want ErrStaleGeneration", err)
	}
	if _, err := mustRevealLease(lease0); err != nil {
		t.Fatalf("existing lease did not survive generation bump: %v", err)
	}

	generation1 := generation0
	generation1.ProviderGeneration = 1
	lease1 := mustBorrow(t, cache, generation1)
	defer lease1.Release()

	cache.RevokeRole("tenant-a", "role")
	if cache.Len() != 0 {
		t.Fatalf("role revocation left %d entries", cache.Len())
	}
	if !sts.Secret(1).State().Destroyed {
		t.Fatal("role revocation did not destroy the cached master secret")
	}
	if _, err := cache.Borrow(context.Background(), generation1); !errors.Is(err, ErrRoleRevoked) {
		t.Fatalf("revoked role error = %v, want ErrRoleRevoked", err)
	}
	if _, err := mustRevealLease(lease1); err != nil {
		t.Fatalf("existing lease did not survive role revocation: %v", err)
	}
}

func TestGlobalRoleRevocationBlocksEveryTenant(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("global-revoke")
		result := testResult(identity, clock, time.Hour, secretID, "AKIA", "material")
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	tenantA := testIdentity("tenant-a", "shared-role")
	tenantB := testIdentity("tenant-b", "shared-role")
	leaseA := mustBorrow(t, cache, tenantA)
	defer leaseA.Release()
	leaseB := mustBorrow(t, cache, tenantB)
	defer leaseB.Release()

	cache.RevokeRoleID("shared-role")
	if _, err := cache.Borrow(context.Background(), tenantA); !errors.Is(err, ErrRoleRevoked) {
		t.Fatalf("tenant-a error = %v, want ErrRoleRevoked", err)
	}
	if _, err := cache.Borrow(context.Background(), tenantB); !errors.Is(err, ErrRoleRevoked) {
		t.Fatalf("tenant-b error = %v, want ErrRoleRevoked", err)
	}
	if _, err := mustRevealLease(leaseA); err != nil {
		t.Fatalf("tenant-a lease did not survive revocation: %v", err)
	}
	if _, err := mustRevealLease(leaseB); err != nil {
		t.Fatalf("tenant-b lease did not survive revocation: %v", err)
	}
}

func mustRevealLease(lease *Lease) (string, error) {
	memory, ok := lease.Secret().(*MemorySecret)
	if !ok {
		return "", errors.New("not a memory secret")
	}
	material, err := memory.Reveal()
	return string(material), err
}

func TestConcurrentWaitersShareOneRefreshAndCancelIndependently(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	identity := testIdentity("tenant-a", "role")
	key := identity.CacheKey()

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var providerCanceled bool
	var canceledMu sync.Mutex
	sts := &testSTS{}
	sts.fetch = func(ctx context.Context, identity Identity) (*STSResult, error) {
		startedOnce.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			canceledMu.Lock()
			providerCanceled = true
			canceledMu.Unlock()
			return nil, ctx.Err()
		case <-release:
			secretID := uniqueSecretID("shared")
			result := testResult(identity, clock, time.Hour, secretID, "AKIA-SHARED", "shared-material")
			result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
			return result, nil
		}
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	type borrowResult struct {
		lease *Lease
		err   error
	}
	results := make(chan borrowResult, 3)
	go func() {
		lease, err := cache.Borrow(ctx1, identity)
		results <- borrowResult{lease: lease, err: err}
	}()

	<-started
	waitForWaiters(t, cache, key, 1)

	go func() {
		lease, err := cache.Borrow(context.Background(), identity)
		results <- borrowResult{lease: lease, err: err}
	}()
	go func() {
		lease, err := cache.Borrow(context.Background(), identity)
		results <- borrowResult{lease: lease, err: err}
	}()
	waitForWaiters(t, cache, key, 3)

	cancel1()
	first := <-results
	if !errors.Is(first.err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", first.err)
	}

	canceledMu.Lock()
	canceled := providerCanceled
	canceledMu.Unlock()
	if canceled {
		t.Fatal("canceling one waiter canceled the shared provider call")
	}
	select {
	case <-release:
		t.Fatal("test bug: release channel closed")
	default:
	}

	close(release)
	second := <-results
	third := <-results
	if second.err != nil || third.err != nil {
		t.Fatalf("remaining waiters: %v, %v", second.err, third.err)
	}
	defer second.lease.Release()
	defer third.lease.Release()
	if sts.Calls() != 1 {
		t.Fatalf("STS calls = %d, want 1", sts.Calls())
	}
	if second.lease.Secret() == third.lease.Secret() {
		t.Fatal("waiters received the same mutable secret handle")
	}
	if mustReveal(t, second.lease.Secret()) != mustReveal(t, third.lease.Secret()) {
		t.Fatal("waiters did not share the refreshed credential")
	}
}

func TestLastWaiterCancellationCancelsProvider(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	identity := testIdentity("tenant-a", "role")

	started := make(chan struct{})
	providerCanceled := make(chan struct{})
	var startedOnce sync.Once
	sts := &testSTS{}
	sts.fetch = func(ctx context.Context, _ Identity) (*STSResult, error) {
		startedOnce.Do(func() { close(started) })
		<-ctx.Done()
		close(providerCanceled)
		return nil, ctx.Err()
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := cache.Borrow(ctx, identity)
		result <- err
	}()
	<-started
	cancel()

	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Borrow error = %v, want context.Canceled", err)
	}
	select {
	case <-providerCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("provider context was not canceled after the last waiter left")
	}
	waitFor(t, "canceled flight cleanup", func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return len(cache.flights) == 0
	})
}

func TestLateResultIsRevalidatedAndDestroyed(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	identity := testIdentity("tenant-a", "role")

	for _, tc := range []struct {
		name   string
		tenant string
	}{
		{name: "wrong tenant", tenant: "tenant-b"},
		{name: "valid identity but abandoned", tenant: "tenant-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			done := make(chan struct{})
			var startedOnce sync.Once
			secret := NewMemorySecret(uniqueSecretID("late"), []byte("late-material"))
			sts := &testSTS{}
			sts.fetch = func(ctx context.Context, _ Identity) (*STSResult, error) {
				startedOnce.Do(func() { close(started) })
				<-release
				result := testResult(identity, clock, time.Hour, "unused", "AKIA-LATE", "")
				result.Secret = secret
				result.TenantID = tc.tenant
				close(done)
				return result, nil
			}
			cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				_, err := cache.Borrow(ctx, identity)
				result <- err
			}()
			<-started
			cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("Borrow error = %v, want context.Canceled", err)
			}

			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("provider did not return")
			}
			waitFor(t, "late result destruction", func() bool {
				return secret.State().Destroyed
			})
			if cache.Len() != 0 {
				t.Fatalf("late result was cached: entries = %d", cache.Len())
			}
			if tc.tenant != identity.TenantID {
				waitFor(t, "late mismatch rejection", func() bool {
					cache.mu.Lock()
					defer cache.mu.Unlock()
					return len(cache.flights) == 0
				})
			}
		})
	}
}

func TestFailedRefreshIsNotCached(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	identity := testIdentity("tenant-a", "role")
	refreshErr := errors.New("STS unavailable")
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		if sts.Calls() == 1 {
			return nil, refreshErr
		}
		secretID := uniqueSecretID("retry")
		result := testResult(identity, clock, time.Hour, secretID, "AKIA", "material")
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	if _, err := cache.Borrow(context.Background(), identity); !errors.Is(err, refreshErr) {
		t.Fatalf("first Borrow error = %v, want refresh error", err)
	}
	if cache.Len() != 0 {
		t.Fatalf("failed refresh was cached")
	}

	lease := mustBorrow(t, cache, identity)
	defer lease.Release()
	if sts.Calls() != 2 {
		t.Fatalf("STS calls = %d, want 2", sts.Calls())
	}
}

func TestInvalidRefreshResultDestroysSecretAndIsNotCached(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	identity := testIdentity("tenant-a", "role")
	var badSecret *MemorySecret
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, _ Identity) (*STSResult, error) {
		badSecret = NewMemorySecret(uniqueSecretID("bad"), []byte("bad-material"))
		result := testResult(identity, clock, time.Hour, "unused", "AKIA", "")
		result.Secret = badSecret
		result.RoleID = "role-name-instead-of-id"
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	_, err := cache.Borrow(context.Background(), identity)
	if !errors.Is(err, ErrInvalidSTSResult) {
		t.Fatalf("Borrow error = %v, want ErrInvalidSTSResult", err)
	}
	if !badSecret.State().Destroyed {
		t.Fatal("rejected STS secret was not destroyed")
	}
	if cache.Len() != 0 {
		t.Fatal("rejected STS result was cached")
	}
}

func TestExpiredEntryIsReplacedByFreshRefresh(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	identity := testIdentity("tenant-a", "role")
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("refresh")
		result := testResult(identity, clock, 10*time.Second, secretID, "AKIA", "material-"+secretID)
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(2*time.Second))

	first := mustBorrow(t, cache, identity)
	defer first.Release()
	firstMaterial := mustReveal(t, first.Secret())

	clock.Advance(9 * time.Second)
	second := mustBorrow(t, cache, identity)
	defer second.Release()

	if sts.Calls() != 2 {
		t.Fatalf("STS calls = %d, want 2", sts.Calls())
	}
	if mustReveal(t, second.Secret()) == firstMaterial {
		t.Fatal("stale credential was reused")
	}
	if !sts.Secret(0).State().Destroyed {
		t.Fatal("stale cached master secret was not destroyed")
	}
}

func TestGenerationBumpCancelsInFlightRefresh(t *testing.T) {
	clock := newTestClock(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	identity := testIdentity("tenant-a", "role")
	started := make(chan struct{})
	providerCanceled := make(chan struct{})
	var startedOnce sync.Once
	sts := &testSTS{}
	sts.fetch = func(ctx context.Context, _ Identity) (*STSResult, error) {
		startedOnce.Do(func() { close(started) })
		<-ctx.Done()
		close(providerCanceled)
		return nil, ctx.Err()
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(0))

	result := make(chan error, 1)
	go func() {
		_, err := cache.Borrow(context.Background(), identity)
		result <- err
	}()
	<-started
	cache.BumpGeneration()

	select {
	case <-providerCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("generation bump did not cancel the in-flight provider call")
	}
	if err := <-result; !errors.Is(err, ErrRefreshAbandoned) {
		t.Fatalf("in-flight borrow error = %v, want ErrRefreshAbandoned", err)
	}
	if cache.Len() != 0 {
		t.Fatal("old-generation refresh was cached")
	}
}
