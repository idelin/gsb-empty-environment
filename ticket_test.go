package netsec

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newFakeStore(t *testing.T) (*TicketStore, *fakeClock, time.Time) {
	t.Helper()
	base := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	return NewTicketStore(WithClock(clock.Now)), clock, base
}

func TestConcurrentDoubleValidationConsumesOnce(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ticket, err := store.IssueServiceTicket("alice", service, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	const goroutines = 128
	start := make(chan struct{})
	var successes atomic.Int32
	var used atomic.Int32
	var unexpected atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			user, err := store.ValidateServiceTicket(ticket, service, false)
			switch {
			case err == nil && user == "alice":
				successes.Add(1)
			case errors.Is(err, ErrTicketUsed):
				used.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful validations = %d, want exactly 1", got)
	}
	if got := used.Load(); got != goroutines-1 {
		t.Fatalf("used-ticket failures = %d, want %d", got, goroutines-1)
	}
	if got := unexpected.Load(); got != 0 {
		t.Fatalf("unexpected validation failures = %d", got)
	}
}

func TestServiceMustMatchAfterNormalization(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const issuedService = "HTTPS://App.Example.COM:443/callback?x=1#fragment"

	ticket, err := store.IssueServiceTicket("alice", issuedService, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := store.ValidateServiceTicket(ticket, "https://evil.example.com/callback?x=1", false); !errors.Is(err, ErrServiceMismatch) {
		t.Fatalf("changed service error = %v, want ErrServiceMismatch", err)
	}
	// An unsuccessful validation must not burn the ticket.
	user, err := store.ValidateServiceTicket(ticket, "https://app.example.com/callback?x=1", false)
	if err != nil || user != "alice" {
		t.Fatalf("correct service after failed attempt: user=%q err=%v", user, err)
	}
	if _, err := store.ValidateServiceTicket(ticket, "https://app.example.com/callback?x=1", false); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("replay error = %v, want ErrTicketUsed", err)
	}
	if _, err := store.ValidateServiceTicket(ticket, "https://app.example.com/callback/", false); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("validation after use = %v, want ErrTicketUsed", err)
	}
}

func TestConcurrentWrongAndRightService(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ticket, err := store.IssueServiceTicket("alice", service, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var success atomic.Int32
	var mismatch atomic.Int32
	var used atomic.Int32
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		if _, err := store.ValidateServiceTicket(ticket, "https://evil.example.com/callback", false); errors.Is(err, ErrServiceMismatch) {
			mismatch.Add(1)
		} else if errors.Is(err, ErrTicketUsed) {
			used.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if _, err := store.ValidateServiceTicket(ticket, service, false); err == nil {
			success.Add(1)
		}
	}()
	close(start)
	wg.Wait()

	if success.Load() != 1 || mismatch.Load()+used.Load() != 1 || used.Load() > 1 {
		t.Fatalf("success=%d mismatch=%d used=%d, want one success and one rejected attempt", success.Load(), mismatch.Load(), used.Load())
	}
}

func TestTenSecondExpiryBoundary(t *testing.T) {
	store, clock, base := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ticket, err := store.IssueServiceTicket("alice", service, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	clock.Set(base.Add(ServiceTicketLifetime - time.Nanosecond))
	if user, err := store.ValidateServiceTicket(ticket, service, false); err != nil || user != "alice" {
		t.Fatalf("validation just before expiry: user=%q err=%v", user, err)
	}

	store2, clock2, base2 := newFakeStore(t)
	ticket2, err := store2.IssueServiceTicket("alice", service, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	clock2.Set(base2.Add(ServiceTicketLifetime))
	if _, err := store2.ValidateServiceTicket(ticket2, service, false); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("validation at exact expiry = %v, want ErrTicketExpired", err)
	}
}

func TestExpiredTicketDoesNotConsume(t *testing.T) {
	store, clock, base := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ticket, err := store.IssueServiceTicketAt(base.Add(-ServiceTicketLifetime), "alice", service, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := store.ValidateServiceTicket(ticket, service, false); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired validation = %v, want ErrTicketExpired", err)
	}
	// The ticket remains expired, not used.
	clock.Set(base)
	if _, err := store.ValidateServiceTicket(ticket, service, false); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("second expired validation = %v, want ErrTicketExpired", err)
	}
}

func TestRenewRequiresCredentialAuthentication(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ssoTicket, err := store.IssueServiceTicket("alice", service, false)
	if err != nil {
		t.Fatalf("issue SSO ticket: %v", err)
	}
	if _, err := store.ValidateServiceTicket(ssoTicket, service, true); !errors.Is(err, ErrRenewRequired) {
		t.Fatalf("renew SSO ticket error = %v, want ErrRenewRequired", err)
	}
	// renew failure does not consume; a normal validation still works.
	if user, err := store.ValidateServiceTicket(ssoTicket, service, false); err != nil || user != "alice" {
		t.Fatalf("normal validation after renew failure: user=%q err=%v", user, err)
	}

	credentialTicket, err := store.IssueServiceTicket("bob", service, true)
	if err != nil {
		t.Fatalf("issue credential ticket: %v", err)
	}
	if user, err := store.ValidateServiceTicket(credentialTicket, service, true); err != nil || user != "bob" {
		t.Fatalf("renew credential ticket: user=%q err=%v", user, err)
	}
}

func TestIssueOptionsRenewAlias(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ticket, err := store.IssueServiceTicketWithOptions(IssueOptions{
		Service:         service,
		Principal:       "alice",
		FromCredentials: true,
	})
	if err != nil {
		t.Fatalf("issue with options: %v", err)
	}
	if user, err := store.ValidateServiceTicket(ticket, service, true); err != nil || user != "alice" {
		t.Fatalf("renew validation: user=%q err=%v", user, err)
	}
}

func TestProxyTicketCannotBeServiceTicket(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const service = "https://app.example.com/callback"

	proxy, err := store.IssueProxyTicket("alice", service, true)
	if err != nil {
		t.Fatalf("issue proxy ticket: %v", err)
	}
	if len(proxy) < len(ProxyTicketPrefix) || proxy[:len(ProxyTicketPrefix)] != ProxyTicketPrefix {
		t.Fatalf("proxy ticket %q does not have prefix %q", proxy, ProxyTicketPrefix)
	}
	if _, err := store.ValidateServiceTicket(proxy, service, false); !errors.Is(err, ErrProxyTicket) {
		t.Fatalf("proxy-as-service error = %v, want ErrProxyTicket", err)
	}
	renamed := ServiceTicketPrefix + strings.TrimPrefix(proxy, ProxyTicketPrefix)
	if _, err := store.ValidateServiceTicket(renamed, service, false); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("renamed proxy ticket error = %v, want ErrTicketNotFound", err)
	}
	// Failed cross-type validation does not consume the proxy ticket.
	if user, err := store.ValidateProxyTicket(proxy, service, true); err != nil || user != "alice" {
		t.Fatalf("proxy validation: user=%q err=%v", user, err)
	}

	serviceTicket, err := store.IssueServiceTicket("bob", service, false)
	if err != nil {
		t.Fatalf("issue service ticket: %v", err)
	}
	if _, err := store.ValidateProxyTicket(serviceTicket, service, false); !errors.Is(err, ErrServiceTicket) {
		t.Fatalf("service-as-proxy error = %v, want ErrServiceTicket", err)
	}
}

func TestUnknownAndMalformedTicket(t *testing.T) {
	store, _, _ := newFakeStore(t)
	if _, err := store.ValidateServiceTicket("ST-not-a-real-ticket", "https://app.example.com/cb", false); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("unknown ticket error = %v, want ErrTicketNotFound", err)
	}
	if _, err := store.ValidateServiceTicket("ST-x", "not a url", false); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("malformed service error = %v, want ErrInvalidService", err)
	}
}
