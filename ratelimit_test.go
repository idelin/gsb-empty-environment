package netsec

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAddressLimitBlocksForgedXForwardedFor(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 5, AccountLimit: 5, DeviceLimit: 5},
	})

	now := time.Unix(1_700_000_000, 0)
	allowed := 0
	for i := 0; i < 10; i++ {
		req := newRequest("198.51.100.23:5555")
		req.Header.Set(HeaderXForwardedFor, fmt.Sprintf("203.0.113.%d", i))
		identity, err := resolver.Resolve(req)
		if err != nil {
			t.Fatal(err)
		}
		if limiter.AllowAt("login", identity, now).Allowed {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("allowed = %d, want 5", allowed)
	}
}

func TestLegitimateProxyStillBlocksRotatingClientHeaders(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 5, AccountLimit: 10, DeviceLimit: 10},
	})

	now := time.Unix(1_700_000_000, 0)
	allowed := 0
	for i := 0; i < 12; i++ {
		req := newRequest("10.0.0.2:443")
		req.Header.Set(HeaderXForwardedFor, fmt.Sprintf("203.0.113.%d, 198.51.100.23, 10.0.0.7", i))
		identity, err := resolver.Resolve(req)
		if err != nil {
			t.Fatal(err)
		}
		if identity.Address != "198.51.100.23" {
			t.Fatalf("request %d resolved to %q", i, identity.Address)
		}
		if limiter.AllowAt("login", identity, now).Allowed {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("allowed = %d, want 5", allowed)
	}
}

func TestAccountDimensionBlocksDistributedAttack(t *testing.T) {
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 100, AccountLimit: 3, DeviceLimit: 100},
	})

	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 4; i++ {
		identity := Identity{
			Address: fmt.Sprintf("198.51.100.%d", i),
			Account: "victim",
			Device:  fmt.Sprintf("device-%d", i),
		}
		decision := limiter.AllowAt("login", identity, now)
		if i < 3 && !decision.Allowed {
			t.Fatalf("request %d unexpectedly blocked: %+v", i, decision)
		}
		if i == 3 {
			if decision.Allowed || !decision.DeniedBy(DimensionAccount) {
				t.Fatalf("expected account dimension to block: %+v", decision)
			}
		}
	}
}

func TestDeviceDimensionBlocksRotatingAddressAndAccount(t *testing.T) {
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 100, AccountLimit: 100, DeviceLimit: 2},
	})

	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 3; i++ {
		identity := Identity{
			Address: fmt.Sprintf("198.51.100.%d", i),
			Account: fmt.Sprintf("account-%d", i),
			Device:  "shared-device",
		}
		decision := limiter.AllowAt("login", identity, now)
		if i < 2 && !decision.Allowed {
			t.Fatalf("request %d unexpectedly blocked: %+v", i, decision)
		}
		if i == 2 {
			if decision.Allowed || !decision.DeniedBy(DimensionDevice) {
				t.Fatalf("expected device dimension to block: %+v", decision)
			}
		}
	}
}

func TestActionSpecificThresholdsAndWindows(t *testing.T) {
	limiter := MustNewLimiter(map[string]Policy{
		"login":          {Window: time.Minute, AddressLimit: 2, AccountLimit: 2, DeviceLimit: 2},
		"password-reset": {Window: 10 * time.Minute, AddressLimit: 5, AccountLimit: 5, DeviceLimit: 5},
	})

	now := time.Unix(1_700_000_000, 0)
	identity := Identity{Address: "198.51.100.1", Account: "alice", Device: "phone"}
	for i := 0; i < 3; i++ {
		decision := limiter.AllowAt("login", identity, now)
		if i < 2 && !decision.Allowed {
			t.Fatalf("login %d blocked early: %+v", i, decision)
		}
		if i == 2 && decision.Allowed {
			t.Fatal("third login must be blocked")
		}
	}
	for i := 0; i < 5; i++ {
		decision := limiter.AllowAt("password-reset", identity, now)
		if !decision.Allowed {
			t.Fatalf("password-reset %d should have a separate budget: %+v", i, decision)
		}
	}
	if decision := limiter.AllowAt("password-reset", identity, now); decision.Allowed || !decision.DeniedBy(DimensionAddress) {
		t.Fatalf("sixth password-reset must be blocked: %+v", decision)
	}
}

func TestSlidingWindowExpiryAndRetryAfter(t *testing.T) {
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 2, AccountLimit: 2, DeviceLimit: 2},
	})
	identity := Identity{Address: "198.51.100.1", Account: "alice", Device: "phone"}
	start := time.Unix(1_700_000_000, 0)

	if d := limiter.AllowAt("login", identity, start); !d.Allowed {
		t.Fatal(d)
	}
	if d := limiter.AllowAt("login", identity, start.Add(30*time.Second)); !d.Allowed {
		t.Fatal(d)
	}
	d := limiter.AllowAt("login", identity, start.Add(59*time.Second))
	if d.Allowed || d.RetryAfter != time.Second {
		t.Fatalf("decision = %+v, want retry after 1s", d)
	}
	if d := limiter.AllowAt("login", identity, start.Add(61*time.Second)); !d.Allowed {
		t.Fatalf("oldest event should have expired: %+v", d)
	}
}

func TestUnknownActionFailsClosed(t *testing.T) {
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 1, AccountLimit: 1, DeviceLimit: 1},
	})
	d := limiter.AllowAt("unconfigured", Identity{Address: "198.51.100.1"}, time.Now())
	if d.Allowed || d.Reason != ReasonUnknownAction {
		t.Fatalf("decision = %+v", d)
	}
}

func TestPolicyHotReloadReevaluatesExistingCounters(t *testing.T) {
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 5, AccountLimit: 5, DeviceLimit: 5},
	})
	identity := Identity{Address: "198.51.100.1", Account: "alice", Device: "phone"}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 3; i++ {
		if d := limiter.AllowAt("login", identity, now); !d.Allowed {
			t.Fatal(d)
		}
	}
	if err := limiter.SetPolicy("login", Policy{Window: time.Minute, AddressLimit: 3, AccountLimit: 3, DeviceLimit: 3}); err != nil {
		t.Fatal(err)
	}
	if d := limiter.AllowAt("login", identity, now); d.Allowed {
		t.Fatalf("reloaded policy must re-interpret existing counters: %+v", d)
	}
	if err := limiter.SetPolicy("login", Policy{Window: time.Minute, AddressLimit: 4, AccountLimit: 4, DeviceLimit: 4}); err != nil {
		t.Fatal(err)
	}
	if d := limiter.AllowAt("login", identity, now); !d.Allowed {
		t.Fatalf("fourth event should be allowed after raising the limit: %+v", d)
	}
}

func TestPerActionProtectionAcrossHTTPProtocols(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 2, AccountLimit: 2, DeviceLimit: 2},
	})
	now := time.Unix(1_700_000_000, 0)

	allowed := 0
	for i, proto := range []string{"HTTP/1.1", "HTTP/2.0", "WebSocket"} {
		req := newRequest("10.0.0.2:443")
		req.Proto = proto
		req.Header.Set(HeaderXForwardedFor, fmt.Sprintf("203.0.113.%d, 198.51.100.9, 10.0.0.7", i+1))
		req.Header.Set("X-Account-ID", "alice")
		req.Header.Set("X-Device-ID", "phone")
		identity, err := resolver.Resolve(req)
		if err != nil {
			t.Fatal(err)
		}
		if identity.Address != "198.51.100.9" {
			t.Fatalf("protocol %s resolved to %q", proto, identity.Address)
		}
		if limiter.AllowAt("login", identity, now).Allowed {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("allowed = %d, want 2 across protocols", allowed)
	}
}

func TestLimiterConcurrentCalls(t *testing.T) {
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 10, AccountLimit: 10, DeviceLimit: 10},
	})
	identity := Identity{Address: "198.51.100.1", Account: "alice", Device: "phone"}
	now := time.Unix(1_700_000_000, 0)

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.AllowAt("login", identity, now).Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed = %d, want 10", got)
	}
}

func TestGuardUsesResolvedIdentity(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 1, AccountLimit: 1, DeviceLimit: 1},
	})
	guard, err := NewGuard(resolver, limiter)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)

	req := newRequest("10.0.0.2:443")
	req.Header.Set(HeaderXForwardedFor, "198.51.100.5, 10.0.0.7")
	req.Header.Set("X-Account-ID", "alice")
	identity, decision, err := guard.CheckAt(req, "login", now)
	if err != nil || !decision.Allowed {
		t.Fatalf("identity=%+v decision=%+v err=%v", identity, decision, err)
	}
	if identity.Address != "198.51.100.5" {
		t.Fatalf("identity = %+v", identity)
	}

	req.Header.Set(HeaderXForwardedFor, "203.0.113.9, 10.0.0.7")
	_, decision, err = guard.CheckAt(req, "login", now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || !decision.DeniedBy(DimensionAccount) {
		t.Fatalf("account must still block rotating addresses: %+v", decision)
	}
}

func TestGuardPrefersMiddlewareIdentityAfterHeaderStripping(t *testing.T) {
	trusted := MustTrustedProxies("10.0.0.0/8")
	resolver := NewResolver(trusted)
	limiter := MustNewLimiter(map[string]Policy{
		"login": {Window: time.Minute, AddressLimit: 1, AccountLimit: 1, DeviceLimit: 1},
	})
	guard, err := NewGuard(resolver, limiter)
	if err != nil {
		t.Fatal(err)
	}

	var decisions []Decision
	handler := resolver.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, decision, err := guard.CheckAt(req, "login", time.Unix(1_700_000_000, 0))
		if err != nil {
			t.Fatal(err)
		}
		decisions = append(decisions, decision)
	}))

	for i := 0; i < 2; i++ {
		req := newRequest("10.0.0.2:443")
		req.Header.Set(HeaderXForwardedFor, fmt.Sprintf("203.0.113.%d, 198.51.100.44, 10.0.0.7", i))
		req.Header.Set("X-Account-ID", "alice")
		req.Header.Set("X-Device-ID", "phone")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
	}
	if len(decisions) != 2 {
		t.Fatalf("decisions = %d", len(decisions))
	}
	if !decisions[0].Allowed {
		t.Fatalf("first request blocked: %+v", decisions[0])
	}
	if decisions[0].Identity.Address != "198.51.100.44" {
		t.Fatalf("first identity = %+v", decisions[0].Identity)
	}
	if decisions[1].Allowed || !decisions[1].DeniedBy(DimensionAccount) {
		t.Fatalf("second request must be blocked by account: %+v", decisions[1])
	}
}
