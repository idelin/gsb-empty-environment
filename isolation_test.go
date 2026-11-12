package netsec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.org/netsec/q0422"
	"example.org/netsec/q0422/scripted"
)

func twoTenantService() *scripted.Service {
	svc := scripted.NewService()
	svc.AddBindRule("alice", scripted.BindRule{Password: "pw", Tenant: "tenant-a"})
	svc.AddBindRule("bob", scripted.BindRule{Password: "pw", Tenant: "tenant-b"})
	svc.SetSearchPlan(scripted.SearchPlan{
		Tenant:  "tenant-a",
		Entries: []*netsec.Entry{entry("cn=a1", "tenant-a"), entry("cn=a2", "tenant-a")},
	})
	svc.SetSearchPlan(scripted.SearchPlan{
		Tenant:  "tenant-b",
		Entries: []*netsec.Entry{entry("cn=b1", "tenant-b")},
	})
	return svc
}

// Happy path: entries are delivered and all carry the current tenant tag.
func TestSearchDeliversOnlyOwnTenantEntries(t *testing.T) {
	svc := twoTenantService()
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	la := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	cur, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	entries := drainCursor(t, ctx, cur)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if scripted.EntryTenant(e) != "tenant-a" {
			t.Fatalf("cross-tenant entry delivered: %+v", e)
		}
	}
	if m.Stats().EntriesDelivered != 2 {
		t.Fatalf("EntriesDelivered=%d", m.Stats().EntriesDelivered)
	}
}

// A stale lease from a previous binding must not be usable after rebind.
func TestStaleLeaseRejectedAfterRebind(t *testing.T) {
	svc := twoTenantService()
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	la := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	_ = mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantB(), Username: "bob", Password: "pw",
	})

	if _, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()}); !errors.Is(err, netsec.ErrLeaseInvalid) {
		t.Fatalf("expected ErrLeaseInvalid for stale lease, got %v", err)
	}
}

// Cancelling a search and then receiving the terminal result is safe: the
// next bind proceeds once the terminal response is drained, on the same
// connection, without any cancelled entry being delivered.
func TestCancelThenTerminalReusesSameConnection(t *testing.T) {
	svc := twoTenantService()
	svc.SetSearchPlan(scripted.SearchPlan{
		Tenant:       "tenant-a",
		Entries:      []*netsec.Entry{entry("cn=a1", "tenant-a")},
		HoldTerminal: true,
	})
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	la := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	dialsBefore := svc.Dials()
	connBefore := svc.Conns()[0]

	cur, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	opID := cur.OpID()

	// Consume the single entry, then abandon the search before result-done.
	e, ok := cur.Next(ctx)
	if !ok || scripted.EntryTenant(e) != "tenant-a" {
		t.Fatalf("missing first entry: ok=%v err=%v", ok, cur.Err())
	}
	if err := cur.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// While the terminal is held, a rebind must not complete.
	done := make(chan error, 1)
	go func() {
		_, err := m.Acquire(ctx, netsec.BindRequest{
			Tenant: tenantB(), Username: "bob", Password: "pw",
		})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("rebind completed before terminal response: %v", err)
	case <-time.After(60 * time.Millisecond):
	}

	// Release the terminal; the drain completes and the same connection is
	// rebound to tenant B.
	connBefore.ReleaseTerminal(opID)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rebind after terminal: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("rebind did not complete after terminal release")
	}

	if got := svc.Dials(); got != dialsBefore {
		t.Fatalf("connection must be reused after clean terminal, dials %d -> %d",
			dialsBefore, got)
	}
	if connBefore.IsClosed() {
		t.Fatal("connection was closed despite clean terminal drain")
	}
	if connBefore.BoundTenant() != "tenant-b" {
		t.Fatalf("connection not rebound to tenant-b: %s", connBefore.BoundTenant())
	}
	st := m.Stats()
	if st.ConnectionsPoisoned != 0 {
		t.Fatalf("no poisoning expected, stats=%+v", st)
	}
}

// If the terminal response never arrives, the drain deadline must poison
// and replace the connection rather than reuse a possibly contaminated one.
func TestCancelWithoutTerminalPoisonsConnection(t *testing.T) {
	svc := twoTenantService()
	svc.SetSearchPlan(scripted.SearchPlan{
		Tenant:       "tenant-a",
		Entries:      []*netsec.Entry{entry("cn=a1", "tenant-a")},
		HoldTerminal: true, // never released
	})
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	la := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	cur, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, ok := cur.Next(ctx); !ok {
		t.Fatalf("expected one entry, err=%v", cur.Err())
	}
	_ = cur.Close()
	oldConn := svc.Conns()[0]

	start := time.Now()
	_, err = m.Acquire(ctx, netsec.BindRequest{
		Tenant: tenantB(), Username: "bob", Password: "pw",
	})
	elapsed := time.Since(start)
	if elapsed < testDrainTimeout {
		t.Fatalf("rebind returned before drain timeout (%v)", elapsed)
	}
	if !errors.Is(err, netsec.ErrDrainTimeout) {
		t.Fatalf("expected ErrDrainTimeout, got %v", err)
	}
	if !oldConn.IsClosed() {
		t.Fatal("un-terminated connection must be closed")
	}
	st := m.Stats()
	if st.ConnectionsPoisoned == 0 {
		t.Fatalf("expected poisoning, stats=%+v", st)
	}

	// The poisoned connection is replaced; a retry binds the new one.
	mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantB(), Username: "bob", Password: "pw",
	})
	if svc.Dials() != 2 {
		t.Fatalf("expected exactly one replacement dial, dials=%d", svc.Dials())
	}
	newConn := svc.Conns()[1]
	if newConn == oldConn {
		t.Fatal("replacement must be a different physical connection")
	}
	if newConn.BoundTenant() != "tenant-b" {
		t.Fatalf("replacement connection not bound to tenant-b, got %q", newConn.BoundTenant())
	}
}

// Late entries arriving for a cancelled search are drained silently and are
// never delivered to the cursor, even though the connection is reused.
func TestLateEntriesAfterCancelAreDrainedNotDelivered(t *testing.T) {
	svc := twoTenantService()
	svc.SetSearchPlan(scripted.SearchPlan{
		Tenant:       "tenant-a",
		Entries:      []*netsec.Entry{entry("cn=a1", "tenant-a")},
		HoldTerminal: true,
	})
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	la := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	cur, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	opID := cur.OpID()
	if _, ok := cur.Next(ctx); !ok {
		t.Fatalf("expected first entry: %v", cur.Err())
	}
	_ = cur.Close()
	conn := svc.Conns()[0]

	// Inject late entries for the abandoned operation before the terminal.
	conn.InjectLateEntry(opID, entry("cn=late-a", "tenant-a"))
	conn.InjectLateEntry(opID, entry("cn=late-b", "tenant-a"))

	// The cancelled cursor must not surface those entries.
	readCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	if e, ok := cur.Next(readCtx); ok {
		t.Fatalf("cancelled cursor delivered a late entry: %+v", e)
	}

	conn.ReleaseTerminal(opID)
	_ = mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantB(), Username: "bob", Password: "pw",
	})

	st := m.Stats()
	if st.CanceledEntriesDrained < 2 {
		t.Fatalf("expected >=2 drained cancelled entries, stats=%+v", st)
	}
	if svc.Dials() != 1 {
		t.Fatalf("connection should be reused after clean terminal, dials=%d", svc.Dials())
	}
}

// The headline security property: after rebinding from tenant A to tenant
// B, any late messages still tagged with A's generation/lease must never be
// delivered to B's cursor, and B only ever sees B entries.
func TestZeroCrossIdentityDeliveryAfterRebind(t *testing.T) {
	svc := twoTenantService()
	svc.SetSearchPlan(scripted.SearchPlan{
		Tenant:       "tenant-a",
		Entries:      []*netsec.Entry{entry("cn=a1", "tenant-a")},
		HoldTerminal: true,
	})
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	la := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	curA, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()})
	if err != nil {
		t.Fatalf("Search A: %v", err)
	}
	opA := curA.OpID()
	if _, ok := curA.Next(ctx); !ok {
		t.Fatalf("expected A entry: %v", curA.Err())
	}
	_ = curA.Close()
	conn := svc.Conns()[0]

	// Release A's terminal so the bind drain completes on the same
	// connection, then rebind to B.
	conn.ReleaseTerminal(opA)
	lb := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantB(), Username: "bob", Password: "pw",
	})
	if conn.BoundTenant() != "tenant-b" {
		t.Fatalf("rebind did not select tenant-b: %s", conn.BoundTenant())
	}

	// A late A message arriving on the wire after the B binding. It carries
	// A's old operation id and therefore matches no current op and must be
	// isolated.
	conn.InjectLateEntry(opA, entry("cn=evil-a", "tenant-a"))
	dropBefore := m.Stats().LateOrDropped
	eventually(t, testTimeout(), func() bool {
		return m.Stats().LateOrDropped > dropBefore
	}, "late A message to be dropped after rebind")

	// B's own search must return only B entries.
	curB, err := m.Search(ctx, lb, netsec.SearchRequest{Tenant: tenantB()})
	if err != nil {
		t.Fatalf("Search B: %v", err)
	}
	bEntries := drainCursor(t, ctx, curB)
	if len(bEntries) != 1 {
		t.Fatalf("expected exactly 1 B entry, got %d", len(bEntries))
	}
	for _, e := range bEntries {
		if scripted.EntryTenant(e) != "tenant-b" {
			t.Fatalf("cross-identity delivery detected: %+v", e)
		}
		if e.DN == "cn=evil-a" {
			t.Fatalf("tenant A entry leaked to tenant B cursor")
		}
	}

	// Aggregate invariant: nothing delivered anywhere ever carried the
	// wrong tenant relative to the active binding. Re-check stats and the A
	// cursor.
	if e, ok := curA.Next(ctx); ok {
		t.Fatalf("closed A cursor received post-rebind entry: %+v", e)
	}
}

// Bind is exclusive: a search started concurrently with an in-flight bind
// blocks until the new lease is issued and then runs under the new tenant.
func TestBindIsExclusiveSearchesWait(t *testing.T) {
	svc := twoTenantService()
	svc.SetSearchPlan(scripted.SearchPlan{
		Tenant:       "tenant-a",
		Entries:      []*netsec.Entry{entry("cn=a1", "tenant-a")},
		HoldTerminal: true,
	})
	m := newTestManager(svc)
	defer m.Close()
	ctx := context.Background()

	la := mustAcquire(t, ctx, m, netsec.BindRequest{
		Tenant: tenantA(), Username: "alice", Password: "pw",
	})
	curA, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()})
	if err != nil {
		t.Fatalf("Search A: %v", err)
	}
	opA := curA.OpID()
	if _, ok := curA.Next(ctx); !ok {
		t.Fatalf("expected A entry: %v", curA.Err())
	}
	_ = curA.Close()

	type result struct {
		l   netsec.Lease
		err error
	}
	bindDone := make(chan result, 1)
	go func() {
		l, err := m.Acquire(ctx, netsec.BindRequest{
			Tenant: tenantB(), Username: "bob", Password: "pw",
		})
		bindDone <- result{l, err}
	}()

	// Give the bind time to enter its exclusive/drain phase.
	time.Sleep(40 * time.Millisecond)

	searchDone := make(chan error, 1)
	go func() {
		// Using the stale lease on purpose; it must block until binding
		// finishes, then be rejected rather than run under tenant A.
		_, err := m.Search(ctx, la, netsec.SearchRequest{Tenant: tenantA()})
		searchDone <- err
	}()
	select {
	case err := <-searchDone:
		t.Fatalf("search ran during exclusive bind window: %v", err)
	case <-time.After(40 * time.Millisecond):
	}

	svc.Conns()[0].ReleaseTerminal(opA)
	select {
	case r := <-bindDone:
		if r.err != nil {
			t.Fatalf("bind: %v", r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("bind never completed")
	}
	select {
	case err := <-searchDone:
		if !errors.Is(err, netsec.ErrLeaseInvalid) {
			t.Fatalf("stale lease after blocked search: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked search never resumed")
	}
}
