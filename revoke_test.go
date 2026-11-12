package netsec

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type invalidateRequest struct {
	id  uint64
	gen uint64
}

// mockDriver is a fake page driver: it records invalidation requests and lets
// the test confirm them in any order, with any outcome.
type mockDriver struct {
	mu       sync.Mutex
	requests []invalidateRequest
	syncFail map[uint64]error // ids whose Invalidate call fails synchronously
}

func (m *mockDriver) Invalidate(id uint64, gen uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, invalidateRequest{id: id, gen: gen})
	if err, ok := m.syncFail[id]; ok {
		return err
	}
	return nil
}

func (m *mockDriver) genFor(t *testing.T, id uint64) uint64 {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.requests) - 1; i >= 0; i-- {
		if m.requests[i].id == id {
			return m.requests[i].gen
		}
	}
	t.Fatalf("driver never asked to invalidate mapping %d", id)
	return 0
}

func (m *mockDriver) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

type revokeOutcome struct {
	res RevokeResult
	err error
}

// startRevoke launches Revoke and waits until the driver has seen exactly
// wantRequests invalidation requests, i.e. the fence is up and the
// transaction is waiting for confirmations.
func startRevoke(t *testing.T, c *Coordinator, d *mockDriver, wantRequests int) chan revokeOutcome {
	t.Helper()
	out := make(chan revokeOutcome, 1)
	go func() {
		res, err := c.Revoke(context.Background())
		out <- revokeOutcome{res: res, err: err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for d.requestCount() < wantRequests {
		if time.Now().After(deadline) {
			t.Fatalf("driver saw %d invalidation requests, want %d", d.requestCount(), wantRequests)
		}
		time.Sleep(time.Millisecond)
	}
	return out
}

func mustRegister(t *testing.T, c *Coordinator, ids ...uint64) {
	t.Helper()
	for _, id := range ids {
		if err := c.Register(id); err != nil {
			t.Fatalf("Register(%d): %v", id, err)
		}
	}
}

func TestRevokeConfirmsOutOfOrder(t *testing.T) {
	d := &mockDriver{}
	c := NewCoordinator(d)
	mustRegister(t, c, 1, 2, 3)

	out := startRevoke(t, c, d, 3)

	// Confirm in reverse order; Revoke must not finish before the last one.
	for _, id := range []uint64{3, 1} {
		c.Confirm(Confirmation{MappingID: id, Generation: d.genFor(t, id)})
		select {
		case <-out:
			t.Fatal("Revoke returned before all mappings were confirmed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	c.Confirm(Confirmation{MappingID: 2, Generation: d.genFor(t, 2)})

	o := <-out
	if o.err != nil {
		t.Fatalf("Revoke: %v", o.err)
	}
	if !reflect.DeepEqual(o.res.Revoked, []uint64{1, 2, 3}) {
		t.Fatalf("Revoked = %v, want [1 2 3]", o.res.Revoked)
	}
	for _, id := range []uint64{1, 2, 3} {
		if c.Active(id) {
			t.Fatalf("mapping %d still active after successful revocation", id)
		}
	}
}

func TestRevokeIgnoresStaleGenerationAfterIDReuse(t *testing.T) {
	d := &mockDriver{}
	c := NewCoordinator(d)
	mustRegister(t, c, 7)

	out := startRevoke(t, c, d, 1)
	oldGen := d.genFor(t, 7)

	// A stale report from an older generation of id 7 must be dropped.
	c.Confirm(Confirmation{MappingID: 7, Generation: oldGen - 1})
	select {
	case <-out:
		t.Fatal("Revoke accepted a stale-generation confirmation")
	case <-time.After(20 * time.Millisecond):
	}

	c.Confirm(Confirmation{MappingID: 7, Generation: oldGen})
	o := <-out
	if o.err != nil {
		t.Fatalf("Revoke: %v", o.err)
	}

	// The id is free again and gets a fresh generation on re-registration.
	mustRegister(t, c, 7)
	d2out := startRevoke(t, c, d, 2)
	newGen := d.genFor(t, 7)
	if newGen == oldGen {
		t.Fatal("generation was not bumped when mapping id 7 was reused")
	}

	// A late duplicate of the first transaction's confirmation must not
	// complete the second transaction.
	c.Confirm(Confirmation{MappingID: 7, Generation: oldGen})
	select {
	case <-d2out:
		t.Fatal("second Revoke completed on a confirmation for the old generation")
	case <-time.After(20 * time.Millisecond):
	}
	c.Confirm(Confirmation{MappingID: 7, Generation: newGen})
	if o := <-d2out; o.err != nil {
		t.Fatalf("second Revoke: %v", o.err)
	}
}

func TestRevokeBlocksNewTenantsDuringTransaction(t *testing.T) {
	d := &mockDriver{}
	c := NewCoordinator(d)
	mustRegister(t, c, 1)

	out := startRevoke(t, c, d, 1)

	// New tenant applications are fenced off while revocation runs.
	if err := c.Register(2); !errors.Is(err, ErrRevoking) {
		t.Fatalf("Register during revocation = %v, want ErrRevoking", err)
	}
	// Re-registering a mapping that is being revoked is also blocked.
	if err := c.Register(1); !errors.Is(err, ErrRevoking) {
		t.Fatalf("Register of in-flight id = %v, want ErrRevoking", err)
	}

	c.Confirm(Confirmation{MappingID: 1, Generation: d.genFor(t, 1)})
	if o := <-out; o.err != nil {
		t.Fatalf("Revoke: %v", o.err)
	}

	// Once the transaction completes, the fence lifts for fresh ids.
	mustRegister(t, c, 2)
	if !c.Active(2) {
		t.Fatal("mapping 2 not active after fence lifted")
	}
}

func TestFailedMappingStaysQuarantined(t *testing.T) {
	d := &mockDriver{}
	c := NewCoordinator(d)
	mustRegister(t, c, 1, 2)

	out := startRevoke(t, c, d, 2)

	c.Confirm(Confirmation{MappingID: 1, Generation: d.genFor(t, 1)})
	c.Confirm(Confirmation{MappingID: 2, Generation: d.genFor(t, 2), Err: errors.New("tlb shootdown timeout")})

	o := <-out
	if !errors.Is(o.err, ErrIncompleteRevocation) {
		t.Fatalf("Revoke err = %v, want ErrIncompleteRevocation", o.err)
	}
	if !reflect.DeepEqual(o.res.Revoked, []uint64{1}) || !reflect.DeepEqual(o.res.Failed, []uint64{2}) {
		t.Fatalf("result = %+v, want Revoked=[1] Failed=[2]", o.res)
	}

	// The failed mapping stays isolated and its id can never be reused.
	if !c.Quarantined(2) {
		t.Fatal("mapping 2 not quarantined after failed invalidation")
	}
	if err := c.Register(2); !errors.Is(err, ErrMappingQuarantined) {
		t.Fatalf("re-Register(2) = %v, want ErrMappingQuarantined", err)
	}
	// The successfully revoked id is reusable, and other new ids are fine.
	mustRegister(t, c, 1, 3)

	// A later revocation must not try to invalidate the quarantined mapping
	// again, and quarantine survives further transactions.
	d2out := startRevoke(t, c, d, 4)
	for _, id := range []uint64{1, 3} {
		c.Confirm(Confirmation{MappingID: id, Generation: d.genFor(t, id)})
	}
	if o := <-d2out; o.err != nil {
		t.Fatalf("second Revoke: %v", o.err)
	}
	if !c.Quarantined(2) {
		t.Fatal("quarantine on mapping 2 lost after second revocation")
	}
}

func TestSyncInvalidateFailureQuarantines(t *testing.T) {
	d := &mockDriver{syncFail: map[uint64]error{9: errors.New("driver offline")}}
	c := NewCoordinator(d)
	mustRegister(t, c, 8, 9)

	out := startRevoke(t, c, d, 2)
	c.Confirm(Confirmation{MappingID: 8, Generation: d.genFor(t, 8)})

	o := <-out
	if !errors.Is(o.err, ErrIncompleteRevocation) {
		t.Fatalf("Revoke err = %v, want ErrIncompleteRevocation", o.err)
	}
	if !c.Quarantined(9) {
		t.Fatal("mapping 9 not quarantined after synchronous driver failure")
	}
	if err := c.Register(9); !errors.Is(err, ErrMappingQuarantined) {
		t.Fatalf("re-Register(9) = %v, want ErrMappingQuarantined", err)
	}
}

func TestRevokeResultSortedRegardlessOfConfirmOrder(t *testing.T) {
	d := &mockDriver{}
	c := NewCoordinator(d)
	mustRegister(t, c, 5, 4, 3, 2, 1)

	out := startRevoke(t, c, d, 5)
	for _, id := range []uint64{2, 5, 1, 4, 3} {
		c.Confirm(Confirmation{MappingID: id, Generation: d.genFor(t, id)})
	}
	o := <-out
	if o.err != nil {
		t.Fatalf("Revoke: %v", o.err)
	}
	if !reflect.DeepEqual(o.res.Revoked, []uint64{1, 2, 3, 4, 5}) {
		t.Fatalf("Revoked = %v, want sorted [1 2 3 4 5]", o.res.Revoked)
	}
}
