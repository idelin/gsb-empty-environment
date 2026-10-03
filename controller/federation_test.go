package controller_test

import (
	"testing"
	"time"

	"leasefed/controller"
	"leasefed/store"
	"leasefed/witness"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// TestCrossDCFailover is the acceptance scenario:
//
//  1. DC1 primary, DC2 standby.
//  2. Fiber cut: DC1 loses contact with the Witness.
//  3. DC1 self-demotes when its local lease expires; the Witness confirms
//     DC1 is really gone (lease expiry) and grants DC2 a takeover lease.
//  4. Fiber healed: DC1 detects its credential is invalid and stays/becomes
//     a read-only standby; its stale writes are rejected by the fence.
func TestCrossDCFailover(t *testing.T) {
	const ttl = 300 * time.Millisecond

	judge := witness.New()
	link1 := controller.NewCuttableClient(witness.NewLocalClient(judge))
	link2 := controller.NewCuttableClient(witness.NewLocalClient(judge))

	dc1 := controller.New("dc1", link1, store.New(), ttl)
	dc2 := controller.New("dc2", link2, store.New(), ttl)
	dc1.Start()
	defer dc1.Stop()

	// --- Phase 1: DC1 wins the lease, DC2 is standby. ---
	waitFor(t, "dc1 becomes primary", dc1.IsPrimary)
	dc2.Start()
	defer dc2.Stop()
	time.Sleep(2 * ttl) // let dc2 try (and fail) to grab the lease
	if dc2.IsPrimary() {
		t.Fatal("dc2 must not be primary while dc1 holds the lease")
	}
	if err := dc1.Write("balance", "100"); err != nil {
		t.Fatalf("primary write failed: %v", err)
	}
	if err := dc2.Write("balance", "0"); err != controller.ErrNotPrimary {
		t.Fatalf("standby write must fail with ErrNotPrimary, got %v", err)
	}
	epoch1 := dc1.Epoch()

	// --- Phase 2: fiber cut, DC1 <-> Witness link is down. ---
	link1.Cut()

	// DC1 may serve writes only until its local lease expires...
	if !dc1.IsPrimary() {
		t.Fatal("dc1 should still be primary right after the cut (lease not yet expired)")
	}
	// ...then it must demote itself even without hearing from the Witness.
	waitFor(t, "dc1 self-demotes after local lease expiry", func() bool {
		return !dc1.IsPrimary()
	})

	// --- Phase 3: Witness confirms DC1 is gone, DC2 gets a takeover lease. ---
	waitFor(t, "dc2 promoted to primary", dc2.IsPrimary)
	epoch2 := dc2.Epoch()
	if epoch2 <= epoch1 {
		t.Fatalf("takeover must bump the fencing epoch: %d -> %d", epoch1, epoch2)
	}
	if err := dc2.Write("balance", "50"); err != nil {
		t.Fatalf("promoted dc2 write failed: %v", err)
	}

	// --- Phase 4: fiber healed, DC1 learns its credential is invalid. ---
	link1.Heal()
	waitFor(t, "dc1 stays a read-only standby after healing", func() bool {
		return dc1.Role() == controller.Standby && dc1.Store().Fence() >= epoch2
	})
	if err := dc1.Write("balance", "1"); err != controller.ErrNotPrimary {
		t.Fatalf("recovered dc1 must be read-only, got %v", err)
	}
	// A zombie writer on DC1 using the old epoch is fenced off at the
	// storage layer as well.
	if err := dc1.Store().Put("balance", "999", epoch1); err != store.ErrStaleFencingToken {
		t.Fatalf("stale-epoch write must be fenced, got %v", err)
	}
	if !dc2.IsPrimary() {
		t.Fatal("dc2 must remain the single primary after healing")
	}

	// Global invariant spot-check: never two primaries at once.
	if dc1.IsPrimary() && dc2.IsPrimary() {
		t.Fatal("dual primary detected")
	}
}

// TestNoDualPrimaryWhilePartitioned verifies that during the partition window
// (before DC1's lease expires) DC2 is NOT promoted: the judge only hands over
// after the lease fully expires, so there is never an overlap.
func TestNoDualPrimaryWhilePartitioned(t *testing.T) {
	const ttl = 300 * time.Millisecond

	judge := witness.New()
	link1 := controller.NewCuttableClient(witness.NewLocalClient(judge))
	link2 := controller.NewCuttableClient(witness.NewLocalClient(judge))
	dc1 := controller.New("dc1", link1, store.New(), ttl)
	dc2 := controller.New("dc2", link2, store.New(), ttl)
	dc1.Start()
	defer dc1.Stop()

	waitFor(t, "dc1 primary", dc1.IsPrimary)
	dc2.Start()
	defer dc2.Stop()
	link1.Cut()

	// Sample both controllers for longer than one ttl: at every instant at
	// most one of them may consider itself writable.
	deadline := time.Now().Add(2 * ttl)
	for time.Now().Before(deadline) {
		if dc1.IsPrimary() && dc2.IsPrimary() {
			t.Fatal("dual primary during partition")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitFor(t, "dc2 eventually promoted", dc2.IsPrimary)
}
