package tenantcache

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock               { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }
func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func mustAdd(t *testing.T, e *Engine, id, parent string, g int64) {
	t.Helper()
	if err := e.AddTenant(id, parent, g); err != nil {
		t.Fatalf("AddTenant(%s): %v", id, err)
	}
}

func mustPut(t *testing.T, e *Engine, tenant, key string, size int64) *Certificate {
	t.Helper()
	cert, err := e.Put(tenant, key, size)
	if err != nil {
		t.Fatalf("Put(%s,%s,%d): %v", tenant, key, size, err)
	}
	return cert
}

func borrowedOf(t *testing.T, e *Engine, id string) int64 {
	t.Helper()
	return e.Stats().Tenants[id].Borrowed
}

func guaranteedOf(t *testing.T, e *Engine, id string) int64 {
	t.Helper()
	return e.Stats().Tenants[id].Guaranteed
}

// Guarantees may never overcommit physical capacity, at root or child level.
func TestGuaranteeAdmission(t *testing.T) {
	e, err := NewEngine(Config{Capacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, e, "A", "", 60)
	mustAdd(t, e, "B", "", 40)
	if err := e.AddTenant("C", "", 1); err == nil {
		t.Fatal("expected admission failure: 60+40+1 > 100")
	} else {
		var unsat *UnsatReason
		if !errors.As(err, &unsat) || unsat.Code != "admission_guarantee" {
			t.Fatalf("expected admission_guarantee unsat, got %v", err)
		}
	}
	if err := e.SetGuarantee("B", 41); err == nil {
		t.Fatal("expected SetGuarantee 41 to fail")
	}
	if err := e.SetGuarantee("B", 40); err != nil {
		t.Fatalf("SetGuarantee 40 should succeed: %v", err)
	}

	// child guarantees are carved from the parent's guarantee
	e2, _ := NewEngine(Config{Capacity: 100})
	mustAdd(t, e2, "P", "", 50)
	mustAdd(t, e2, "c1", "P", 30)
	if err := e2.AddTenant("c2", "P", 21); err == nil {
		t.Fatal("expected child admission failure: 30+21 > 50")
	}
	mustAdd(t, e2, "c2", "P", 20)
	if err := e2.SetGuarantee("P", 40); err == nil {
		t.Fatal("expected SetGuarantee below child sum to fail")
	}
}

// Hierarchy is dynamically adjustable, with admission re-checked and cycles rejected.
func TestReparent(t *testing.T) {
	e, _ := NewEngine(Config{Capacity: 100})
	mustAdd(t, e, "P", "", 60)
	mustAdd(t, e, "Q", "", 40)
	mustAdd(t, e, "c1", "P", 30)
	if err := e.Reparent("c1", "Q"); err != nil {
		t.Fatalf("reparent c1 under Q (30<=40) should succeed: %v", err)
	}
	if err := e.Reparent("c1", ""); err == nil {
		t.Fatal("reparent to root should fail: 60+40+30 > 100")
	}
	if err := e.Reparent("Q", "c1"); err == nil {
		t.Fatal("reparent Q under its own child should fail (cycle)")
	}
	if err := e.Reparent("c1", "c1"); err == nil {
		t.Fatal("self-parenting should fail")
	}
}

// Under pressure, borrowed usage is reclaimed by fair share; guarantees of
// others are never touched.
func TestFairReclaimUnderPressure(t *testing.T) {
	e, _ := NewEngine(Config{Capacity: 100})
	mustAdd(t, e, "A", "", 50)
	mustAdd(t, e, "B", "", 0)
	mustAdd(t, e, "C", "", 0)
	for i := 0; i < 5; i++ {
		mustPut(t, e, "A", fmt.Sprintf("a%d", i), 10) // 50 guaranteed
	}
	for i := 0; i < 5; i++ {
		mustPut(t, e, "B", fmt.Sprintf("b%d", i), 10) // 50 borrowed (pool=50)
	}
	cert := mustPut(t, e, "C", "c0", 20)
	if cert.Class != "borrowed" {
		t.Fatalf("expected borrowed class, got %s", cert.Class)
	}
	// fair: demands B=50, C=20 over pool 50 -> C satisfied (20), B gets 30
	if got := borrowedOf(t, e, "B"); got != 30 {
		t.Fatalf("B borrowed = %d, want 30 (reclaimed down to fair share)", got)
	}
	if got := guaranteedOf(t, e, "A"); got != 50 {
		t.Fatalf("A guaranteed = %d, want 50 (never infringed)", got)
	}
	if len(cert.Evicted) != 2 || cert.Evicted[0].Reason != "over-fair-share" {
		t.Fatalf("expected 2 over-fair-share evictions, got %+v", cert.Evicted)
	}
	if cert.FairShares["B"] != 30 || cert.FairShares["C"] != 20 {
		t.Fatalf("unexpected fair shares in cert: %v", cert.FairShares)
	}
}

// When the fair rules cannot free enough, the allocation fails with an
// explicit unsat reason; others' guarantees stay intact.
func TestUnsatCertificate(t *testing.T) {
	e, _ := NewEngine(Config{Capacity: 100})
	mustAdd(t, e, "A", "", 60)
	mustAdd(t, e, "B", "", 0)
	mustAdd(t, e, "C", "", 0)
	for i := 0; i < 6; i++ {
		mustPut(t, e, "A", fmt.Sprintf("a%d", i), 10) // 60 guaranteed
	}
	for i := 0; i < 4; i++ {
		mustPut(t, e, "B", fmt.Sprintf("b%d", i), 10) // 40 borrowed
	}
	cert, err := e.Put("C", "big", 50)
	if err == nil {
		t.Fatal("expected unsat: only 40 reclaimable from B's fair-share excess")
	}
	if cert.Unsat == nil || cert.Unsat.Code != "capacity_exhausted" {
		t.Fatalf("expected capacity_exhausted unsat, got %+v", cert.Unsat)
	}
	if cert.Unsat.ProtectedBytes < 60 {
		t.Fatalf("A's guarantee should be reported protected: %+v", cert.Unsat)
	}
	if got := guaranteedOf(t, e, "A"); got != 60 {
		t.Fatalf("A guaranteed = %d, want 60", got)
	}
	// B yielded only its excess over fair share (fair B = 20), not everything.
	if got := borrowedOf(t, e, "B"); got != 20 {
		t.Fatalf("B borrowed = %d, want 20 (fair share floor)", got)
	}
}

// Idle tenants lend their guarantee to the pool; when they return, borrowers
// yield gradually (bounded fraction per tick), not all at once.
func TestIdleLendingAndGradualReturn(t *testing.T) {
	clk := newFakeClock()
	e, _ := NewEngine(Config{
		Capacity: 100, Clock: clk.now,
		IdleAfter: 10 * time.Minute, ReclaimFraction: 0.25,
	})
	mustAdd(t, e, "A", "", 60)
	mustAdd(t, e, "B", "", 0)
	clk.advance(20 * time.Minute) // A goes idle
	if !e.Stats().Tenants["A"].Idle {
		t.Fatal("A should be idle")
	}
	for i := 0; i < 9; i++ {
		mustPut(t, e, "B", fmt.Sprintf("b%d", i), 10) // borrows into A's lent guarantee
	}
	if got := borrowedOf(t, e, "B"); got != 90 {
		t.Fatalf("B borrowed = %d, want 90 (idle guarantee lent out)", got)
	}
	e.Touch("A") // A returns: pool shrinks to 40, B's fair share is 40
	if e.Stats().Tenants["A"].Idle {
		t.Fatal("A should be active again")
	}
	prev := borrowedOf(t, e, "B")
	steps := 0
	for prev > 40 && steps < 100 {
		e.Tick()
		cur := borrowedOf(t, e, "B")
		if cur >= prev {
			t.Fatalf("borrowed usage must decrease each tick: %d -> %d", prev, cur)
		}
		prev, steps = cur, steps+1
	}
	if steps < 2 {
		t.Fatalf("yield must be gradual, took only %d tick(s)", steps)
	}
	if prev != 40 {
		t.Fatalf("B borrowed = %d, want exactly fair share 40", prev)
	}
}

// Deleting a tenant with active read leases must not release leased bytes
// early; they are freed when the last lease is released.
func TestDeleteWithActiveLease(t *testing.T) {
	e, _ := NewEngine(Config{Capacity: 100, Source: func(key string) ([]byte, error) {
		return []byte("payload-" + key), nil
	}})
	mustAdd(t, e, "A", "", 50)
	mustAdd(t, e, "B", "", 0)
	lease, _, err := e.Get("A", "k1") // guaranteed entry, leased
	if err != nil {
		t.Fatal(err)
	}
	size := int64(len("payload-k1"))
	mustPut(t, e, "B", "b1", 10)
	blease, _, err := e.Get("B", "b1") // borrowed entry, leased
	if err != nil {
		t.Fatal(err)
	}
	used := e.Stats().Used
	if err := e.DeleteTenant("A"); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteTenant("B"); err != nil {
		t.Fatal(err)
	}
	if got := e.Stats().Used; got != used {
		t.Fatalf("leased bytes freed early: used=%d want %d", got, used)
	}
	if string(lease.Data()) != "payload-k1" {
		t.Fatalf("lease unreadable after tenant delete: %q", lease.Data())
	}
	lease.Release()
	if got := e.Stats().Used; got != used-size {
		t.Fatalf("after release used=%d want %d", got, used-size)
	}
	blease.Release()
	if got := e.Stats().Used; got != 0 {
		t.Fatalf("all resources should be released, used=%d", got)
	}
}

// Loading reservations are billed while in flight and released on failure.
func TestReservationBillingAndFailureRelease(t *testing.T) {
	var observedReserved int64
	inject := errors.New("injected commit failure")
	var e *Engine
	e, _ = NewEngine(Config{
		Capacity: 100,
		Source:   func(key string) ([]byte, error) { return make([]byte, 30), nil },
		Fail: func(op string) error {
			if op == "commit" {
				observedReserved = e.Stats().Used // reservation must be billed now
				return inject
			}
			return nil
		},
	})
	mustAdd(t, e, "A", "", 50)
	_, _, err := e.Get("A", "k")
	if !errors.Is(err, inject) {
		t.Fatalf("expected injected failure, got %v", err)
	}
	if observedReserved != 30 {
		t.Fatalf("reservation not billed during load: used=%d want 30", observedReserved)
	}
	if got := e.Stats().Used; got != 0 {
		t.Fatalf("reservation leaked after failure: used=%d", got)
	}
}

// Source failures also release the reservation.
func TestSourceFailureRelease(t *testing.T) {
	srcErr := errors.New("source boom")
	e, _ := NewEngine(Config{
		Capacity: 100,
		Source:   func(key string) ([]byte, error) { return nil, srcErr },
	})
	mustAdd(t, e, "A", "", 50)
	if _, _, err := e.Get("A", "k"); !errors.Is(err, srcErr) {
		t.Fatalf("expected source error, got %v", err)
	}
	if got := e.Stats().Used; got != 0 {
		t.Fatalf("usage leaked: %d", got)
	}
}

// One tenant exceeding its limit must not clear everyone else's cache:
// reclamation is proportional to fair-share excess only.
func TestOneTenantOverLimitDoesNotClearOthers(t *testing.T) {
	e, _ := NewEngine(Config{Capacity: 100})
	for _, id := range []string{"A", "B", "C", "D"} {
		mustAdd(t, e, id, "", 0)
	}
	for _, id := range []string{"A", "B", "C"} {
		for i := 0; i < 3; i++ {
			mustPut(t, e, id, fmt.Sprintf("%s%d", id, i), 10) // 30 borrowed each
		}
	}
	cert := mustPut(t, e, "D", "d0", 20) // free=10, needs 10 more
	if got := borrowedOf(t, e, "B"); got != 30 {
		t.Fatalf("B borrowed = %d, want 30 (untouched)", got)
	}
	if got := borrowedOf(t, e, "C"); got != 30 {
		t.Fatalf("C borrowed = %d, want 30 (untouched)", got)
	}
	// fair share is 26 each; only A (first by deterministic order) yields ~its excess
	if got := borrowedOf(t, e, "A"); got != 20 {
		t.Fatalf("A borrowed = %d, want 20", got)
	}
	for _, ev := range cert.Evicted {
		if ev.Reason != "over-fair-share" {
			t.Fatalf("unexpected eviction %+v", ev)
		}
	}
}

// Eviction order is deterministic: identical scenarios produce identical
// eviction sequences.
func TestDeterministicEvictionOrder(t *testing.T) {
	build := func() *Engine {
		clk := newFakeClock()
		e, _ := NewEngine(Config{Capacity: 100, Clock: clk.now})
		for _, id := range []string{"A", "B"} {
			mustAdd(t, e, id, "", 0)
		}
		for i := 0; i < 6; i++ {
			if i < 3 {
				mustPut(t, e, "A", fmt.Sprintf("a%d", i), 10)
			}
			mustPut(t, e, "B", fmt.Sprintf("b%d", i), 10)
		}
		return e
	}
	e1, e2 := build(), build()
	c1 := mustPut(t, e1, "A", "new", 40)
	c2 := mustPut(t, e2, "A", "new", 40)
	if fmt.Sprintf("%v", c1.Evicted) != fmt.Sprintf("%v", c2.Evicted) {
		t.Fatalf("non-deterministic eviction:\n%v\n%v", c1.Evicted, c2.Evicted)
	}
	// fair-share victims yield first (over-fair-share), then the requester's
	// own entries in LRU/seq order (own-lru).
	want := []Eviction{
		{Tenant: "B", Key: "b0", Size: 10, Reason: "over-fair-share"},
		{Tenant: "A", Key: "a0", Size: 10, Reason: "own-lru"},
		{Tenant: "A", Key: "a1", Size: 10, Reason: "own-lru"},
	}
	if fmt.Sprintf("%v", c1.Evicted) != fmt.Sprintf("%v", want) {
		t.Fatalf("eviction order = %v, want %v", c1.Evicted, want)
	}
}

// Full lifecycle returns all resources.
func TestResourceRelease(t *testing.T) {
	e, _ := NewEngine(Config{Capacity: 100, Source: func(key string) ([]byte, error) {
		return []byte(key), nil
	}})
	mustAdd(t, e, "A", "", 60)
	mustAdd(t, e, "B", "", 40)
	mustPut(t, e, "A", "x", 30)
	l1, _, _ := e.Get("A", "y")
	l2, _, _ := e.Get("B", "z")
	if e.Stats().Used == 0 {
		t.Fatal("expected usage")
	}
	l1.Release()
	l2.Release()
	if err := e.DeleteTenant("A"); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteTenant("B"); err != nil {
		t.Fatal(err)
	}
	s := e.Stats()
	if s.Used != 0 || s.Free != 100 {
		t.Fatalf("resources not fully released: %+v", s)
	}
}

func TestGetMissWithoutSource(t *testing.T) {
	e, _ := NewEngine(Config{Capacity: 100})
	mustAdd(t, e, "A", "", 10)
	if _, _, err := e.Get("A", "k"); err == nil ||
		!strings.Contains(err.Error(), "no source") {
		t.Fatalf("expected no-source error, got %v", err)
	}
}
