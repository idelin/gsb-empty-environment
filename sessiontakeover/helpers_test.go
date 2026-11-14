package sessiontakeover

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

type fakeInspector struct {
	mu       sync.Mutex
	revoked  map[string]bool
	failNext bool
}

func newFakeInspector() *fakeInspector {
	return &fakeInspector{revoked: make(map[string]bool)}
}

func (i *fakeInspector) IsRevoked(_ context.Context, ref CredentialRef) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.failNext {
		i.failNext = false
		return false, errors.New("inspector unavailable")
	}
	return i.revoked[ref.ID], nil
}

func (i *fakeInspector) Revoke(id string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.revoked[id] = true
}

func cred(tenant, subject, credID string) CredentialRef {
	return CredentialRef{ID: credID, Identity: Identity{Tenant: tenant, Subject: subject}}
}

func connectReq(tenant, clientID, subject, credID string) ConnectRequest {
	return ConnectRequest{
		Tenant:    tenant,
		ClientID:  clientID,
		Presented: cred(tenant, subject, credID),
	}
}

func newTestRegistry(t *testing.T, policy ConflictPolicy, clock *fakeClock, inspector *fakeInspector) *Registry {
	t.Helper()
	if clock == nil {
		clock = newFakeClock()
	}
	if inspector == nil {
		inspector = newFakeInspector()
	}
	r := NewRegistry(Config{
		ConflictPolicy:       policy,
		Inspector:            inspector,
		Clock:                clock,
		InboundQoS2Window:    8,
		MaxOfflineMessages:   16,
		DefaultSessionExpiry: 0,
	})
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func mustConnect(t *testing.T, r *Registry, req ConnectRequest) *Handle {
	t.Helper()
	h, _, err := r.Connect(context.Background(), req)
	if err != nil {
		t.Fatalf("Connect(%s/%s): unexpected error %v", req.Tenant, req.ClientID, err)
	}
	if h == nil {
		t.Fatalf("Connect(%s/%s): nil handle", req.Tenant, req.ClientID)
	}
	return h
}

func assertReject(t *testing.T, r *Registry, req ConnectRequest, want Reason) {
	t.Helper()
	h, res, err := r.Connect(context.Background(), req)
	if err == nil {
		t.Fatalf("Connect expected rejection, got outcome=%v handle=%v", res.Outcome, h)
	}
	if h != nil {
		t.Fatalf("rejected Connect returned non-nil handle")
	}
	if res.Reason != want {
		t.Fatalf("reject reason = %v, want %v (err=%v)", res.Reason, want, err)
	}
}

func assertSubscriptions(t *testing.T, h *Handle, want []string) {
	t.Helper()
	subs, err := h.Subscriptions()
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	got := make(map[string]bool, len(subs))
	for _, sub := range subs {
		got[sub.Filter] = true
	}
	if len(got) != len(want) {
		t.Fatalf("subscription count = %d, want %d (%v)", len(got), len(want), got)
	}
	for _, filter := range want {
		if !got[filter] {
			t.Fatalf("missing subscription %q in %v", filter, got)
		}
	}
}
