package lease

import (
	"fmt"
	"testing"
	"time"
)

// TestRootLeaseLifecycle covers acquire / renew / release at the root.
func TestRootLeaseLifecycle(t *testing.T) {
	root := NewRootCoordinator(100)

	l, err := root.AcquireRegionLease("region-a", 40, 2*time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if l.Quota != 40 || l.HolderID != "region-a" {
		t.Fatalf("unexpected lease: %+v", l)
	}
	if got := root.UsedQuota(); got != 40 {
		t.Fatalf("used quota = %d, want 40", got)
	}

	before := l.ExpiresAt
	time.Sleep(50 * time.Millisecond)
	nl, err := root.RenewRegionLease(l.ID, 2*time.Second)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !nl.ExpiresAt.After(before) {
		t.Fatalf("renew did not extend expiry: %v -> %v", before, nl.ExpiresAt)
	}

	root.ReleaseRegionLease(l.ID)
	if got := root.UsedQuota(); got != 0 {
		t.Fatalf("used quota after release = %d, want 0", got)
	}
	if _, err := root.RenewRegionLease(l.ID, time.Second); err != ErrLeaseNotFound {
		t.Fatalf("renew after release: err = %v, want ErrLeaseNotFound", err)
	}
}

// TestRootQuotaEnforcement ensures regions cannot oversubscribe the
// cluster quota.
func TestRootQuotaEnforcement(t *testing.T) {
	root := NewRootCoordinator(100)
	if _, err := root.AcquireRegionLease("region-a", 60, time.Minute); err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	if _, err := root.AcquireRegionLease("region-b", 60, time.Minute); err != ErrQuotaExhausted {
		t.Fatalf("acquire b: err = %v, want ErrQuotaExhausted", err)
	}
	if _, err := root.AcquireRegionLease("region-b", 40, time.Minute); err != nil {
		t.Fatalf("acquire b with fitting quota: %v", err)
	}
	if got := root.ActiveLeases(); got != 2 {
		t.Fatalf("active leases = %d, want 2", got)
	}
}

// TestSubLeaseBoundedByRegionQuota ensures a gateway cannot mint more
// sub-lease quota than its root lease carries.
func TestSubLeaseBoundedByRegionQuota(t *testing.T) {
	root := NewRootCoordinator(1000)
	gw := NewGateway("region-a", root, 3, time.Minute, 50*time.Millisecond)
	if err := gw.AcquireRootLease(); err != nil {
		t.Fatalf("root lease: %v", err)
	}

	var ids []string
	for i := 0; i < 3; i++ {
		l, err := gw.RequestSubLease(fmt.Sprintf("worker-%d", i), 1)
		if err != nil {
			t.Fatalf("sub-lease %d: %v", i, err)
		}
		ids = append(ids, l.ID)
	}
	if _, err := gw.RequestSubLease("worker-3", 1); err != ErrQuotaExhausted {
		t.Fatalf("4th sub-lease: err = %v, want ErrQuotaExhausted", err)
	}

	gw.ReleaseSubLease(ids[0])
	if _, err := gw.RequestSubLease("worker-3", 1); err != nil {
		t.Fatalf("sub-lease after release: %v", err)
	}
}

// TestSubLeaseCannotOutliveRootLease verifies the expiry cap.
func TestSubLeaseCannotOutliveRootLease(t *testing.T) {
	root := NewRootCoordinator(1000)
	gw := NewGateway("region-a", root, 10, 60*time.Millisecond, time.Hour)
	if err := gw.AcquireRootLease(); err != nil {
		t.Fatalf("root lease: %v", err)
	}
	sub, err := gw.RequestSubLease("worker-0", 1)
	if err != nil {
		t.Fatalf("sub-lease: %v", err)
	}
	rl := gw.RootLease()
	if sub.ExpiresAt.After(rl.ExpiresAt) {
		t.Fatalf("sub-lease %v outlives root lease %v", sub.ExpiresAt, rl.ExpiresAt)
	}
}

// TestWorkerDropIsReclaimedLocally is the core acceptance test for
// fault isolation: a worker goes offline (stops renewing, never
// releases), its sub-lease times out and is reclaimed by the gateway
// locally, and the root observes ZERO additional requests.
func TestWorkerDropIsReclaimedLocally(t *testing.T) {
	root := NewRootCoordinator(1000)
	gw := NewGateway("region-a", root, 10, time.Minute, 40*time.Millisecond)
	if err := gw.AcquireRootLease(); err != nil {
		t.Fatalf("root lease: %v", err)
	}

	sub, err := gw.RequestSubLease("worker-7", 1)
	if err != nil {
		t.Fatalf("sub-lease: %v", err)
	}
	if got := gw.UsedQuota(); got != 1 {
		t.Fatalf("used = %d, want 1", got)
	}

	// Worker-7 drops offline here: no renewal, no release.
	rootReqsBefore := root.Requests()

	// Wait beyond the sub-lease TTL, then let the gateway reap.
	time.Sleep(2 * 40 * time.Millisecond)
	if n := gw.ReapExpired(); n != 1 {
		t.Fatalf("reaped = %d, want 1", n)
	}
	if got := gw.UsedQuota(); got != 0 {
		t.Fatalf("used after reap = %d, want 0", got)
	}
	if got := gw.Reaped(); got != 1 {
		t.Fatalf("reaped metric = %d, want 1", got)
	}

	// Root was never contacted during the drop/timeout/reclaim window.
	if got := root.Requests(); got != rootReqsBefore {
		t.Fatalf("root requests changed during worker drop: %d -> %d, want flat",
			rootReqsBefore, got)
	}

	// The expired sub-lease can no longer be renewed.
	if _, err := gw.RenewSubLease(sub.ID); err != ErrLeaseNotFound {
		t.Fatalf("renew dropped lease: err = %v, want ErrLeaseNotFound", err)
	}
}
