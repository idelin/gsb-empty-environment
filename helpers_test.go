package netsec_test

import (
	"context"
	"testing"
	"time"

	"example.org/netsec/q0422"
	"example.org/netsec/q0422/scripted"
)

const testDrainTimeout = 250 * time.Millisecond

func testTimeout() time.Duration { return 2 * time.Second }

func tenantA() netsec.Tenant { return netsec.Tenant{ID: "tenant-a", BaseDN: "ou=a"} }
func tenantB() netsec.Tenant { return netsec.Tenant{ID: "tenant-b", BaseDN: "ou=b"} }

func newTestManager(svc *scripted.Service) *netsec.Manager {
	return netsec.NewManager(netsec.Config{
		Dialer:       svc.Dialer(),
		Confirmer:    scripted.Confirmer(),
		DrainTimeout: testDrainTimeout,
	})
}

func entry(dn, tenant string) *netsec.Entry {
	return &netsec.Entry{
		DN:         dn,
		Attributes: map[string][]string{"cn": {dn}, "x-tenant": {tenant}},
	}
}

func mustAcquire(t *testing.T, ctx context.Context, m *netsec.Manager,
	req netsec.BindRequest) netsec.Lease {
	t.Helper()
	l, err := m.Acquire(ctx, req)
	if err != nil {
		t.Fatalf("Acquire(%s): unexpected error: %v", req.Username, err)
	}
	return l
}

func drainCursor(t *testing.T, ctx context.Context, c netsec.Cursor) []*netsec.Entry {
	t.Helper()
	var out []*netsec.Entry
	for {
		e, ok := c.Next(ctx)
		if !ok {
			if err := c.Err(); err != nil {
				t.Fatalf("unexpected cursor error: %v", err)
			}
			return out
		}
		out = append(out, e)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func eventually(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	waitFor(t, timeout, cond, msg)
}
