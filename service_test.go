package delegation

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testKey = bytes32("0123456789abcdef0123456789abcdef")

func bytes32(s string) []byte {
	key := make([]byte, 32)
	copy(key, s)
	return key
}

type fakeClock struct {
	mu  sync.Mutex
	cur time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{cur: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = c.cur.Add(d)
}

func futureScope(clock *fakeClock, tenant string, objects, actions []string) Scope {
	return Scope{
		TenantID:  tenant,
		Objects:   objects,
		Actions:   actions,
		ExpiresAt: clock.now().Add(time.Hour).Unix(),
	}
}

func newTestService(t *testing.T) (*Service, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	service, err := NewService(testKey, WithClock(clock.now))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, clock
}

func TestNewServiceRejectsWeakKey(t *testing.T) {
	if _, err := NewService(make([]byte, 31)); !errors.Is(err, ErrWeakKey) {
		t.Fatalf("expected ErrWeakKey, got %v", err)
	}
}

func TestIssueAndClaim(t *testing.T) {
	service, clock := newTestService(t)
	token, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := service.Validate(token); err != nil {
		t.Fatalf("Validate before claim: %v", err)
	}
	ticket, err := service.Claim(token, "tenant-a", "obj-1", "read", "job-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if ticket.ID == "" {
		t.Fatal("claimed ticket has no ID")
	}
	if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job-2"); !errors.Is(err, ErrTicketConsumed) {
		t.Fatalf("second claim: expected ErrTicketConsumed, got %v", err)
	}
}

func TestClaimFailuresDoNotConsume(t *testing.T) {
	service, clock := newTestService(t)
	token, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1", "obj-2"}, []string{"read", "write"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := service.Claim(token, "tenant-a", "obj-1", "delete", "job"); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("action outside scope: expected ErrScopeDenied, got %v", err)
	}
	if _, err := service.Claim(token, "tenant-b", "obj-1", "read", "job"); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("tenant mismatch: expected ErrScopeDenied, got %v", err)
	}
	if _, err := service.Claim(token, "tenant-a", "obj-9", "read", "job"); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("object outside scope: expected ErrScopeDenied, got %v", err)
	}

	clock.advance(2 * time.Hour)
	if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired claim: expected ErrTicketExpired, got %v", err)
	}
	if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired ticket must not flip to consumed, got %v", err)
	}
}

func TestMalformedAndTamperedTokens(t *testing.T) {
	service, clock := newTestService(t)
	token, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, bad := range []string{"", "garbage", "v1.abc", "v2.a.b"} {
		if _, err := service.Claim(bad, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("token %q: expected ErrInvalidToken, got %v", bad, err)
		}
	}

	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + flipB64(parts[1]) + "." + parts[2]
	if _, err := service.Claim(tampered, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered token: expected ErrInvalidToken, got %v", err)
	}

	other, err := NewService(bytes32("ffffffffffffffffffffffffffffffff"))
	if err != nil {
		t.Fatalf("NewService other: %v", err)
	}
	foreign, err := other.Issue(futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
	if err != nil {
		t.Fatalf("foreign Issue: %v", err)
	}
	if _, err := service.Claim(foreign, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("foreign token: expected ErrInvalidToken, got %v", err)
	}

	if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); err != nil {
		t.Fatalf("original token should remain claimable: %v", err)
	}
}

func flipB64(s string) string {
	if s[0] == 'A' {
		return "B" + s[1:]
	}
	return "A" + s[1:]
}

func TestDelegateNarrowsScope(t *testing.T) {
	service, clock := newTestService(t)
	root, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1", "obj-2"}, []string{"read", "write"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	childScope := Scope{
		TenantID:  "tenant-a",
		Objects:   []string{"obj-1"},
		Actions:   []string{"read"},
		ExpiresAt: clock.now().Add(30 * time.Minute).Unix(),
	}
	child, err := service.Delegate(root, childScope)
	if err != nil {
		t.Fatalf("Delegate narrowing: %v", err)
	}
	if err := service.Validate(child); err != nil {
		t.Fatalf("Validate child: %v", err)
	}

	grandchild, err := service.Delegate(child, Scope{
		TenantID:  "tenant-a",
		Objects:   []string{"obj-1"},
		Actions:   []string{"read"},
		ExpiresAt: clock.now().Add(15 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("second-level Delegate: %v", err)
	}

	cases := []Scope{
		{TenantID: "tenant-b", Objects: []string{"obj-1"}, Actions: []string{"read"}, ExpiresAt: childScope.ExpiresAt},
		{TenantID: "tenant-a", Objects: []string{"obj-1", "obj-2"}, Actions: []string{"read"}, ExpiresAt: childScope.ExpiresAt},
		{TenantID: "tenant-a", Objects: []string{"obj-1"}, Actions: []string{"read", "write"}, ExpiresAt: childScope.ExpiresAt},
		{TenantID: "tenant-a", Objects: []string{"obj-1"}, Actions: []string{"read"}, ExpiresAt: clock.now().Add(2 * time.Hour).Unix()},
		{TenantID: "tenant-a", Objects: []string{"obj-3"}, Actions: []string{"read"}, ExpiresAt: childScope.ExpiresAt},
		{TenantID: "tenant-a", Objects: nil, Actions: []string{"read"}, ExpiresAt: childScope.ExpiresAt},
	}
	for i, widened := range cases {
		if _, err := service.Delegate(child, widened); !(errors.Is(err, ErrScopeWidened) || errors.Is(err, ErrMalformedScope)) {
			t.Fatalf("widening case %d: expected ErrScopeWidened or ErrMalformedScope, got %v", i, err)
		}
	}

	if _, err := service.Claim(grandchild, "tenant-a", "obj-1", "read", "job"); err != nil {
		t.Fatalf("grandchild claim: %v", err)
	}
	if _, err := service.Delegate(root, childScope); err != nil {
		t.Fatalf("root should remain delegatable after rejected attempts: %v", err)
	}
}

func TestConsumedTicketCannotDelegateOrReclaim(t *testing.T) {
	service, clock := newTestService(t)
	root, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1", "obj-2"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := service.Claim(root, "tenant-a", "obj-1", "read", "job"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := service.Delegate(root, futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"})); !errors.Is(err, ErrTicketConsumed) {
		t.Fatalf("delegate after consume: expected ErrTicketConsumed, got %v", err)
	}
	if err := service.Validate(root); !errors.Is(err, ErrTicketConsumed) {
		t.Fatalf("validate after consume: expected ErrTicketConsumed, got %v", err)
	}
}

func TestRevokeInvalidatesDescendants(t *testing.T) {
	service, clock := newTestService(t)
	root, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1", "obj-2"}, []string{"read", "write"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	child, err := service.Delegate(root, futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Delegate child: %v", err)
	}
	grandchild, err := service.Delegate(child, futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Delegate grandchild: %v", err)
	}
	sibling, err := service.Delegate(root, futureScope(clock, "tenant-a", []string{"obj-2"}, []string{"write"}))
	if err != nil {
		t.Fatalf("Delegate sibling: %v", err)
	}

	if err := service.Revoke(child); err != nil {
		t.Fatalf("Revoke child: %v", err)
	}

	for name, token := range map[string]string{"child": child, "grandchild": grandchild} {
		if err := service.Validate(token); !errors.Is(err, ErrTicketRevoked) {
			t.Fatalf("Validate %s after ancestor revoke: expected ErrTicketRevoked, got %v", name, err)
		}
		if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrTicketRevoked) {
			t.Fatalf("Claim %s after ancestor revoke: expected ErrTicketRevoked, got %v", name, err)
		}
		if _, err := service.Delegate(token, futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"})); !errors.Is(err, ErrTicketRevoked) {
			t.Fatalf("Delegate %s after revoke: expected ErrTicketRevoked, got %v", name, err)
		}
	}

	if err := service.Validate(root); err != nil {
		t.Fatalf("root should survive child revocation: %v", err)
	}
	if _, err := service.Claim(sibling, "tenant-a", "obj-2", "write", "job"); err != nil {
		t.Fatalf("sibling claim: %v", err)
	}

	if err := service.Revoke(root); err != nil {
		t.Fatalf("Revoke root: %v", err)
	}
	if err := service.Validate(root); !errors.Is(err, ErrTicketRevoked) {
		t.Fatalf("root after self-revoke: expected ErrTicketRevoked, got %v", err)
	}
	if err := service.Revoke(root); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
}

func TestConcurrentClaimAllowsExactlyOne(t *testing.T) {
	service, clock := newTestService(t)
	const goroutines = 64

	for run := 0; run < 25; run++ {
		token, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
		if err != nil {
			t.Fatalf("Issue run %d: %v", run, err)
		}
		var winners int32
		var start sync.WaitGroup
		start.Add(1)
		var done sync.WaitGroup
		for i := 0; i < goroutines; i++ {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); err == nil {
					atomic.AddInt32(&winners, 1)
				}
			}()
		}
		start.Done()
		done.Wait()
		if winners != 1 {
			t.Fatalf("run %d: expected exactly one winner, got %d", run, winners)
		}
		if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrTicketConsumed) {
			t.Fatalf("run %d post-check: expected ErrTicketConsumed, got %v", run, err)
		}
	}
}

func TestRevokeClaimInterleaving(t *testing.T) {
	service, clock := newTestService(t)
	const pairs = 200

	var claimWins int32
	for i := 0; i < pairs; i++ {
		token, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
		if err != nil {
			t.Fatalf("Issue %d: %v", i, err)
		}
		var start sync.WaitGroup
		start.Add(1)
		var done sync.WaitGroup
		done.Add(2)

		go func() {
			defer done.Done()
			start.Wait()
			if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); err == nil {
				atomic.AddInt32(&claimWins, 1)
			}
		}()
		go func() {
			defer done.Done()
			start.Wait()
			if err := service.Revoke(token); err != nil {
				t.Errorf("Revoke never fails for a known token: %v", err)
			}
		}()

		start.Done()
		done.Wait()

		// After the race settles, the state must be consistent: exactly one
		// side linearized first, and follow-up calls agree with it.
		claimErr := service.Validate(token)
		if claimErr == nil {
			t.Fatalf("pair %d: ticket must be either consumed or revoked after race", i)
		}
		switch {
		case errors.Is(claimErr, ErrTicketConsumed):
			if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrTicketConsumed) {
				t.Fatalf("pair %d: consumed ticket stayed claimable: %v", i, err)
			}
		case errors.Is(claimErr, ErrTicketRevoked):
			if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrTicketRevoked) {
				t.Fatalf("pair %d: revoked ticket stayed claimable: %v", i, err)
			}
		default:
			t.Fatalf("pair %d: unexpected post-race state %v", i, claimErr)
		}
	}

	if int(claimWins) > pairs {
		t.Fatalf("at most %d claims can win, got %d", pairs, claimWins)
	}
	t.Logf("interleaving result: claim linearized first=%d/%d", claimWins, pairs)
}

func TestExpiryAndRevokeInterleaveConsistency(t *testing.T) {
	service, clock := newTestService(t)
	root, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1", "obj-2"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	child, err := service.Delegate(root, futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	clock.advance(90 * time.Minute)
	if err := service.Validate(child); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired child: expected ErrTicketExpired, got %v", err)
	}
	if err := service.Revoke(root); err != nil {
		t.Fatalf("Revoke expired-root subtree: %v", err)
	}
	if _, err := service.Claim(child, "tenant-a", "obj-1", "read", "job"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired+revoked child reports expiry first: got %v", err)
	}
}

func TestIssueRejectsMalformedScope(t *testing.T) {
	service, clock := newTestService(t)
	base := futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"})
	cases := []Scope{
		{TenantID: "", Objects: []string{"obj-1"}, Actions: []string{"read"}, ExpiresAt: base.ExpiresAt},
		{TenantID: "tenant-a", Objects: []string{""}, Actions: []string{"read"}, ExpiresAt: base.ExpiresAt},
		{TenantID: "tenant-a", Objects: []string{"obj-1"}, Actions: nil, ExpiresAt: base.ExpiresAt},
		{TenantID: "tenant-a", Objects: []string{"obj-1"}, Actions: []string{"read"}, ExpiresAt: 0},
	}
	for i, scope := range cases {
		if _, err := service.Issue(scope); !errors.Is(err, ErrMalformedScope) {
			t.Fatalf("case %d: expected ErrMalformedScope, got %v", i, err)
		}
	}

	// Duplicates are normalized away rather than rejected.
	token, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1", "obj-1", "obj-2"}, []string{"read", "read"}))
	if err != nil {
		t.Fatalf("Issue with duplicates: %v", err)
	}
	parsed, err := parseToken(testKey, token)
	if err != nil {
		t.Fatalf("parseToken: %v", err)
	}
	if len(parsed.Objects) != 2 || len(parsed.Actions) != 1 {
		t.Fatalf("expected normalized sets, got objects=%v actions=%v", parsed.Objects, parsed.Actions)
	}
}

func TestRevokeAndValidateErrorPaths(t *testing.T) {
	service, clock := newTestService(t)
	token, err := service.Issue(futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := service.Revoke("not-a-token"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Revoke malformed: expected ErrInvalidToken, got %v", err)
	}
	if err := service.Validate("v1." + strings.Repeat("A", 10) + ".AAAA"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Validate malformed: expected ErrInvalidToken, got %v", err)
	}

	// A correctly signed token whose ID the service never held maps to
	// not-found after its signature checks out.
	var orphanTicket Ticket
	orphanTicket.ID = "deadbeef"
	orphanTicket.Scope = futureScope(clock, "tenant-a", []string{"obj-1"}, []string{"read"})
	orphan, err := signToken(testKey, orphanTicket)
	if err != nil {
		t.Fatalf("signToken orphan: %v", err)
	}
	if err := service.Validate(orphan); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("Validate orphan: expected ErrTicketNotFound, got %v", err)
	}
	if err := service.Revoke(orphan); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("Revoke orphan: expected ErrTicketNotFound, got %v", err)
	}

	// None of the erroring calls touched the real ticket.
	if _, err := service.Claim(token, "tenant-a", "obj-1", "read", "job"); err != nil {
		t.Fatalf("original ticket should remain claimable: %v", err)
	}
}
