package main

import (
	"reflect"
	"testing"
)

// Two competitors interleaved: A's lease expires while it is stalled,
// B takes over, and A's late write must be rejected by the resource.
func TestStaleMasterCannotOverwrite(t *testing.T) {
	clock := NewClock()
	manager := NewLeaseManager(clock, NewStore())
	resource := NewResource()

	leaseA, err := manager.Grant("A", 10)
	if err != nil {
		t.Fatalf("grant A: %v", err)
	}
	if err := resource.Write(leaseA.Fence, "A:init"); err != nil {
		t.Fatalf("A first write: %v", err)
	}

	clock.Advance(4)
	leaseA, err = manager.Renew("A", leaseA.Fence, 10)
	if err != nil {
		t.Fatalf("renew A: %v", err)
	}
	if err := resource.Write(leaseA.Fence, "A:update"); err != nil {
		t.Fatalf("A rewrite with same fence: %v", err)
	}

	if _, err := manager.Grant("B", 10); err == nil {
		t.Fatal("B granted while A holds the lease")
	} else if _, ok := err.(*LeaseHeldError); !ok {
		t.Fatalf("expected LeaseHeldError, got %T: %v", err, err)
	}

	clock.Advance(11) // A's lease expires while A is stalled
	leaseB, err := manager.Grant("B", 10)
	if err != nil {
		t.Fatalf("grant B: %v", err)
	}
	if leaseB.Fence <= leaseA.Fence {
		t.Fatalf("fence not increasing: A=%d B=%d", leaseA.Fence, leaseB.Fence)
	}
	if err := resource.Write(leaseB.Fence, "B:takes-over"); err != nil {
		t.Fatalf("B write: %v", err)
	}

	// The expired master's late write must be rejected.
	err = resource.Write(leaseA.Fence, "A:stale")
	if err == nil {
		t.Fatal("stale master write was accepted")
	}
	if _, ok := err.(*StaleFenceError); !ok {
		t.Fatalf("expected StaleFenceError, got %T: %v", err, err)
	}

	want := []WriteRecord{
		{Fence: 1, Value: "A:init"},
		{Fence: 1, Value: "A:update"},
		{Fence: 2, Value: "B:takes-over"},
	}
	if got := resource.History(); !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %+v, want %+v", got, want)
	}
	if resource.Data() != "B:takes-over" {
		t.Fatalf("data = %q, stale master overwrote it", resource.Data())
	}
}

// Renewal extends time but never changes the fence number.
func TestRenewKeepsFence(t *testing.T) {
	clock := NewClock()
	manager := NewLeaseManager(clock, NewStore())

	lease, err := manager.Grant("A", 10)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	clock.Advance(9)
	renewed, err := manager.Renew("A", lease.Fence, 10)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed.Fence != lease.Fence {
		t.Fatalf("renew changed fence %d -> %d", lease.Fence, renewed.Fence)
	}
	if renewed.Expiry != 19 {
		t.Fatalf("expiry = %d, want 19", renewed.Expiry)
	}

	if _, err := manager.Renew("B", lease.Fence, 10); err != ErrNotHolder {
		t.Fatalf("renew by non-holder: %v", err)
	}
	clock.Advance(20)
	if _, err := manager.Renew("A", lease.Fence, 10); err != ErrLeaseExpired {
		t.Fatalf("renew after expiry: %v", err)
	}
}

// Recovery after a crash must never lower the fence number, and a
// persistence failure must prevent the grant entirely.
func TestRecoveryNeverLowersFence(t *testing.T) {
	clock := NewClock()
	store := NewStore()
	manager := NewLeaseManager(clock, store)
	resource := NewResource()

	leaseA, err := manager.Grant("A", 5)
	if err != nil {
		t.Fatalf("grant A: %v", err)
	}
	clock.Advance(6)
	leaseB, err := manager.Grant("B", 5)
	if err != nil {
		t.Fatalf("grant B: %v", err)
	}
	if err := resource.Write(leaseB.Fence, "B:data"); err != nil {
		t.Fatalf("B write: %v", err)
	}

	// Persistence failure: grant must fail and consume no fence.
	store.SetFailing(true)
	clock.Advance(6)
	if _, err := manager.Grant("C", 5); err != ErrStoreUnavailable {
		t.Fatalf("grant during store failure: %v", err)
	}
	store.SetFailing(false)
	if got := store.LoadFence(); got != leaseB.Fence {
		t.Fatalf("failed grant consumed a fence: durable=%d, want %d", got, leaseB.Fence)
	}

	// Crash and recover: the new manager reloads the durable fence.
	manager = NewLeaseManager(clock, store)
	leaseC, err := manager.Grant("C", 5)
	if err != nil {
		t.Fatalf("grant C after recovery: %v", err)
	}
	if leaseC.Fence <= leaseB.Fence {
		t.Fatalf("recovery lowered fence: before=%d after=%d", leaseB.Fence, leaseC.Fence)
	}
	if err := resource.Write(leaseC.Fence, "C:recovered"); err != nil {
		t.Fatalf("C write: %v", err)
	}

	// Both pre-crash masters are now stale at the resource.
	for _, stale := range []Lease{leaseA, leaseB} {
		if err := resource.Write(stale.Fence, "stale"); err == nil {
			t.Fatalf("pre-crash fence %d accepted after recovery", stale.Fence)
		}
	}
	if resource.Data() != "C:recovered" {
		t.Fatalf("data = %q, stale master overwrote it", resource.Data())
	}
}

// The clock only moves forward, and only when the simulator says so.
func TestClockCannotGoBackwards(t *testing.T) {
	clock := NewClock()
	clock.Advance(5)
	defer func() {
		if recover() == nil {
			t.Fatal("backward clock advance did not panic")
		}
		if clock.Now() != 5 {
			t.Fatalf("clock moved to %d", clock.Now())
		}
	}()
	clock.Advance(-1)
}
