package netsec

import (
	"errors"
	"testing"
	"time"
)

const testPollInterval = 10 * time.Millisecond

func newTestManager(t *testing.T, f *FakeSysfs, opts ...Option) *Manager {
	t.Helper()
	finalOpts := append([]Option{WithPolling(testPollInterval)}, opts...)
	return NewManager(f, finalOpts...)
}

// waitRevocation blocks until the lease is revoked or the timeout elapses.
func waitRevocation(t *testing.T, lease *Lease) Revocation {
	t.Helper()
	select {
	case rev := <-lease.Revoked():
		return rev
	case <-time.After(3 * time.Second):
		t.Fatalf("group %d lease was not revoked in time", lease.GroupID)
		return Revocation{}
	}
}

// Scenario 1: a group containing a host-driver device (and an unbound device)
// must fail the integrity check and be refused at attach.
func TestCheckGroupRejectsHostDriverAndUnbound(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(5, "0000:01:00.0", "vfio-pci")
	f.AddDevice(5, "0000:01:00.1", "nvme")
	f.AddDevice(5, "0000:01:00.2", "")

	mgr := newTestManager(t, f)

	_, err := mgr.ValidateGroup(5)
	var integrity *IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("expected IntegrityError, got %v", err)
	}
	if len(integrity.Violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %v", len(integrity.Violations), integrity.Violations)
	}

	gotKinds := map[ViolationKind]bool{}
	gotDrivers := map[string]bool{}
	for _, v := range integrity.Violations {
		gotKinds[v.Kind] = true
		gotDrivers[v.Driver] = true
	}
	if !gotKinds[ViolationHostDriver] || !gotKinds[ViolationUnbound] {
		t.Fatalf("expected host-driver and unbound violations, got %v", integrity.Violations)
	}
	if !gotDrivers["nvme"] {
		t.Fatalf("expected nvme to be flagged, got %v", gotDrivers)
	}

	// Attach must refuse for the same reason.
	if _, err := mgr.Attach(5, "tenant-a"); !errors.As(err, &integrity) {
		t.Fatalf("Attach should reject host-driver group, got %v", err)
	}
	if mgr.IsAttached(5) {
		t.Fatal("group must not be attached after a failed integrity check")
	}
}

// A clean group (every member on a VFIO or stub driver) passes and attaches.
func TestCleanGroupAttachesAndValidates(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(7, "0000:02:00.0", "vfio-pci")
	f.AddDevice(7, "0000:02:00.1", "pci-stub")

	mgr := newTestManager(t, f)
	defer mgr.Close()

	info, err := mgr.ValidateGroup(7)
	if err != nil {
		t.Fatalf("clean group should validate: %v", err)
	}
	if len(info.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(info.Devices))
	}

	lease, err := mgr.Attach(7, "tenant-a")
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}
	if !lease.IsActive() {
		t.Fatal("fresh lease should be active")
	}
	if mgr.TenantOf(7) != "tenant-a" {
		t.Fatalf("unexpected owner %q", mgr.TenantOf(7))
	}

	// Empty and unknown groups are refused.
	f.AddGroup(99)
	if _, err := mgr.Attach(99, "tenant-a"); !errors.Is(err, ErrGroupEmpty) {
		t.Fatalf("expected ErrGroupEmpty, got %v", err)
	}
	if _, err := mgr.Attach(4242, "tenant-a"); err == nil {
		t.Fatal("expected error for nonexistent group")
	}
}

// Scenario 2: the same group cannot be shared across tenants or attached to a
// second container; a second request from the same tenant is idempotent.
func TestCrossTenantAndSecondContainerRejected(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(3, "0000:03:00.0", "vfio-pci")

	mgr := newTestManager(t, f)
	defer mgr.Close()

	leaseA, err := mgr.Attach(3, "tenant-a")
	if err != nil {
		t.Fatalf("first Attach failed: %v", err)
	}

	// A different tenant is refused (cross-tenant sharing always denied).
	if _, err := mgr.Attach(3, "tenant-b"); !errors.Is(err, ErrTenantConflict) {
		t.Fatalf("expected ErrTenantConflict, got %v", err)
	}

	// Even the same tenant cannot create a second container attachment; the
	// existing lease is returned instead.
	again, err := mgr.Attach(3, "tenant-a")
	if err != nil {
		t.Fatalf("idempotent re-attach failed: %v", err)
	}
	if again != leaseA {
		t.Fatal("re-attach by the same tenant must return the original lease")
	}

	// Still exactly one attachment, owned by tenant-a.
	if mgr.TenantOf(3) != "tenant-a" || !mgr.IsAttached(3) {
		t.Fatal("group ownership changed unexpectedly")
	}

	// After orderly release, tenant-b may take the group.
	if err := mgr.Release(3); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if leaseA.IsActive() {
		t.Fatal("released lease should no longer be active")
	}
	if _, err := mgr.Attach(3, "tenant-b"); err != nil {
		t.Fatalf("group should be reusable after release, got %v", err)
	}
	if mgr.TenantOf(3) != "tenant-b" {
		t.Fatalf("expected tenant-b to own the group after re-attach")
	}
}

// Scenario 3: a new device hot-plugged into a running group revokes it
// immediately, regardless of what driver the newcomer uses.
func TestHotplugNewMemberRevokesGroup(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(11, "0000:04:00.0", "vfio-pci")

	mgr := newTestManager(t, f)
	defer mgr.Close()

	lease, err := mgr.Attach(11, "tenant-a")
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// Hot-plug a second member already parked on vfio-pci: even a "safe"
	// newcomer must revoke, because the group membership changed at runtime.
	f.HotplugDevice(11, "0000:04:00.1", "vfio-pci")

	rev := waitRevocation(t, lease)
	if rev.Reason != RevokeHotplug {
		t.Fatalf("expected RevokeHotplug, got %s (%s)", rev.Reason, rev.Detail)
	}
	if mgr.IsAttached(11) {
		t.Fatal("group must be detached after hot-plug revocation")
	}

	// A hot-plugged host-driver device is likewise revoked (hot-plug wins).
	lease2, err := mgr.Attach(11, "tenant-a")
	if err != nil {
		t.Fatalf("re-attach failed: %v", err)
	}
	f.HotplugDevice(11, "0000:04:00.2", "mlx5_core")
	rev2 := waitRevocation(t, lease2)
	if rev2.Reason != RevokeHotplug {
		t.Fatalf("expected hot-plug revocation for host-driven device, got %s", rev2.Reason)
	}
}

// Scenario 4: rebinding a running member to another driver revokes the group
// immediately, including when a host driver grabs the device.
func TestRuntimeRebindRevokesGroup(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(8, "0000:05:00.0", "vfio-pci")

	mgr := newTestManager(t, f)
	defer mgr.Close()

	lease, err := mgr.Attach(8, "tenant-a")
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// A host driver claims the device at runtime.
	f.BindDriver("0000:05:00.0", "ixgbe")

	rev := waitRevocation(t, lease)
	if rev.Reason != RevokeRebind {
		t.Fatalf("expected RevokeRebind, got %s (%s)", rev.Reason, rev.Detail)
	}
	if mgr.IsAttached(8) {
		t.Fatal("group must be detached after rebind revocation")
	}

	// Unbinding a member (driver symlink removed) also revokes.
	f.BindDriver("0000:05:00.0", "vfio-pci")
	lease2, err := mgr.Attach(8, "tenant-a")
	if err != nil {
		t.Fatalf("re-attach failed: %v", err)
	}
	f.Unbind("0000:05:00.0")
	rev2 := waitRevocation(t, lease2)
	if rev2.Reason != RevokeRebind {
		t.Fatalf("expected rebind revocation on unbind, got %s (%s)", rev2.Reason, rev2.Detail)
	}

	// Switching between two allowed drivers still revokes: any runtime binding
	// change to a leased member is treated as a violation of the lease.
	f.BindDriver("0000:05:00.0", "vfio-pci")
	lease3, err := mgr.Attach(8, "tenant-a")
	if err != nil {
		t.Fatalf("re-attach failed: %v", err)
	}
	f.BindDriver("0000:05:00.0", "pci-stub")
	rev3 := waitRevocation(t, lease3)
	if rev3.Reason != RevokeRebind {
		t.Fatalf("expected rebind revocation between allowed drivers, got %s", rev3.Reason)
	}
}
