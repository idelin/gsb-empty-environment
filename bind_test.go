package netsec_test

import (
	"context"
	"errors"
	"testing"

	"example.org/netsec/q0422"
	"example.org/netsec/q0422/scripted"
)

// A failed bind must poison the connection: the bind response may leave the
// server connection in an anonymous or undefined authorization state, so it
// can never be reused for another tenant.
func TestBindFailurePoisonsConnection(t *testing.T) {
	svc := scripted.NewService()
	svc.AddBindRule("alice", scripted.BindRule{
		Password: "correct",
		Tenant:   "tenant-a",
	})
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "correct",
	})
	if got := svc.Dials(); got != 1 {
		t.Fatalf("expected 1 dial, got %d", got)
	}

	_, err := m.Acquire(ctx, netsec.BindRequest{
		Tenant: tenantB(), Username: "alice", Password: "wrong",
	})
	if !errors.Is(err, netsec.ErrBindFailed) {
		t.Fatalf("expected ErrBindFailed, got %v", err)
	}

	first := svc.Conns()[0]
	eventually(t, testTimeout(), first.IsClosed, "failed-bind connection to be closed")
	st := m.Stats()
	if st.ConnectionsPoisoned == 0 {
		t.Fatalf("expected poisoned connection counter > 0, stats=%+v", st)
	}
	if st.BindsFailed == 0 {
		t.Fatalf("expected BindsFailed > 0, stats=%+v", st)
	}

	// Next successful bind must run on a freshly dialed connection.
	mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "correct",
	})
	if got := svc.Dials(); got != 2 {
		t.Fatalf("expected a replacement dial (2 total), got %d", got)
	}
	if svc.Conns()[1].BoundTenant() != "tenant-a" {
		t.Fatalf("replacement connection not bound to tenant-a")
	}
}

// A non-empty username with an empty password is an anonymous bind per
// RFC 4513. The lease layer must refuse it even if the server would report
// success, and must do so without issuing any bind traffic.
func TestAnonymousDowngradeRejected(t *testing.T) {
	svc := scripted.NewService()
	// Register alice with an empty server-side password: the scripted bind
	// would succeed, modeling a permissive/legacy directory.
	svc.AddBindRule("alice", scripted.BindRule{Password: "", Tenant: "tenant-a"})
	m := newTestManager(svc)
	defer m.Close()

	_, err := m.Acquire(context.Background(), netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "",
	})
	if !errors.Is(err, netsec.ErrEmptyPassword) {
		t.Fatalf("expected ErrEmptyPassword, got %v", err)
	}
	if recs := svc.BindRecords(); len(recs) != 0 {
		t.Fatalf("no bind should reach the server, got records %+v", recs)
	}
	if got := svc.Dials(); got != 0 {
		t.Fatalf("no connection should be dialed on refused bind, got %d", got)
	}
	st := m.Stats()
	if st.AnonymousRefused == 0 {
		t.Fatalf("expected AnonymousRefused > 0, stats=%+v", st)
	}
	if st.BindsSucceeded != 0 {
		t.Fatalf("no successful bind allowed, stats=%+v", st)
	}
}

// Explicit anonymous bind (empty username AND empty password) is permitted
// at the protocol layer but the confirmer reports no principal; named
// identity rules still apply to authenticated binds.
func TestExplicitAnonymousBindHasNoPrincipal(t *testing.T) {
	svc := scripted.NewService()
	m := newTestManager(svc)
	defer m.Close()

	l, err := m.Acquire(context.Background(), netsec.BindRequest{
		Tenant: tenantA(), Username: "", Password: "",
	})
	if err != nil {
		t.Fatalf("explicit anonymous bind failed: %v", err)
	}
	if l.Tenant().ID != "tenant-a" {
		t.Fatalf("lease tenant mismatch: %s", l.Tenant().ID)
	}
}

// If the post-bind confirmation reports a different principal (or an error),
// the bind cannot be trusted and the connection is poisoned.
func TestIdentityConfirmationMismatch(t *testing.T) {
	svc := scripted.NewService()
	svc.AddBindRule("alice", scripted.BindRule{
		Password: "pw",
		Tenant:   "tenant-a",
		WhoAmI:   "cn=mallory,dc=evil", // server claims we are someone else
	})
	m := newTestManager(svc)
	defer m.Close()

	_, err := m.Acquire(context.Background(), netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	if !errors.Is(err, netsec.ErrIdentityMismatch) {
		t.Fatalf("expected ErrIdentityMismatch, got %v", err)
	}
	st := m.Stats()
	if st.IdentityRejected == 0 {
		t.Fatalf("expected IdentityRejected > 0, stats=%+v", st)
	}
	eventually(t, testTimeout(), func() bool {
		return svc.Conns()[0].IsClosed()
	}, "mismatched-identity connection to be closed")
}

// Confirmation errors are treated as identity rejection as well.
func TestIdentityConfirmationError(t *testing.T) {
	svc := scripted.NewService()
	svc.AddBindRule("alice", scripted.BindRule{
		Password:  "pw",
		Tenant:    "tenant-a",
		WhoAmIErr: errors.New("whoami unavailable"),
	})
	m := newTestManager(svc)
	defer m.Close()

	_, err := m.Acquire(context.Background(), netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	if !errors.Is(err, netsec.ErrIdentityMismatch) {
		t.Fatalf("expected ErrIdentityMismatch on confirmer error, got %v", err)
	}
}

// Each successful bind yields a strictly increasing generation/lease pair.
func TestGenerationAndLeaseAdvancePerBind(t *testing.T) {
	svc := scripted.NewService()
	svc.AddBindRule("alice", scripted.BindRule{Password: "pw", Tenant: "tenant-a"})
	svc.AddBindRule("bob", scripted.BindRule{Password: "pw", Tenant: "tenant-b"})
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	l1 := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	l2 := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantB(), Username: "bob", Password: "pw",
	})
	if l2.Generation() <= l1.Generation() {
		t.Fatalf("generation must advance: %d -> %d", l1.Generation(), l2.Generation())
	}
	if l2.LeaseID() <= l1.LeaseID() {
		t.Fatalf("lease id must advance: %d -> %d", l1.LeaseID(), l2.LeaseID())
	}
}
