package netsec

import (
	"errors"
	"testing"
	"time"
)

// Hot-removing a member revokes the group and detaches it.
func TestHotRemoveAndGroupGoneRevoke(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(13, "0000:06:00.0", "vfio-pci")
	f.AddDevice(13, "0000:06:00.1", "vfio-pci")

	mgr := newTestManager(t, f)
	defer mgr.Close()

	lease, err := mgr.Attach(13, "tenant-a")
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	f.RemoveDevice(13, "0000:06:00.1")
	rev := waitRevocation(t, lease)
	if rev.Reason != RevokeHotRemove {
		t.Fatalf("expected RevokeHotRemove, got %s (%s)", rev.Reason, rev.Detail)
	}
	if mgr.IsAttached(13) {
		t.Fatal("group must be detached after hot-remove")
	}

	// The entire IOMMU group disappearing also revokes.
	lease2, err := mgr.Attach(13, "tenant-a")
	if err != nil {
		t.Fatalf("re-attach failed: %v", err)
	}
	f.RemoveGroup(13)
	rev2 := waitRevocation(t, lease2)
	if rev2.Reason != RevokeGroupGone {
		t.Fatalf("expected RevokeGroupGone, got %s", rev2.Reason)
	}
}

// The revoke hook fires exactly once per runtime revocation.
func TestRevokeHookFired(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(17, "0000:07:00.0", "vfio-pci")

	hooks := make(chan Revocation, 4)
	mgr := newTestManager(t, f, WithRevokeHook(func(r Revocation) {
		hooks <- r
	}))
	defer mgr.Close()

	lease, err := mgr.Attach(17, "tenant-a")
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	f.BindDriver("0000:07:00.0", "nvme")
	rev := waitRevocation(t, lease)

	select {
	case hooked := <-hooks:
		if hooked.Reason != rev.Reason || hooked.GroupID != 17 || hooked.TenantID != "tenant-a" {
			t.Fatalf("hook received unexpected revocation: %+v", hooked)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoke hook was not invoked")
	}
}

// Close revokes all active leases with RevokeClosed and marks the manager
// unusable for further attachments.
func TestCloseRevokesAllLeases(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(21, "0000:08:00.0", "vfio-pci")
	f.AddDevice(22, "0000:09:00.0", "pci-stub")

	mgr := newTestManager(t, f)

	l1, err := mgr.Attach(21, "tenant-a")
	if err != nil {
		t.Fatalf("Attach g21 failed: %v", err)
	}
	l2, err := mgr.Attach(22, "tenant-b")
	if err != nil {
		t.Fatalf("Attach g22 failed: %v", err)
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	for i, lease := range []*Lease{l1, l2} {
		select {
		case rev := <-lease.Revoked():
			if rev.Reason != RevokeClosed {
				t.Fatalf("lease %d: expected RevokeClosed, got %s", i, rev.Reason)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("lease %d not revoked by Close", i)
		}
	}

	if _, err := mgr.Attach(21, "tenant-a"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("expected ErrManagerClosed after Close, got %v", err)
	}
	// Close is idempotent.
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

// Releasing an unknown group is a distinct error; double release is harmless.
func TestReleaseUnknownGroup(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddDevice(31, "0000:0a:00.0", "vfio-pci")

	mgr := newTestManager(t, f)
	defer mgr.Close()

	if _, err := mgr.Attach(31, "tenant-a"); err != nil {
		t.Fatalf("Attach failed: %v", err)
	}
	if err := mgr.Release(31); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if err := mgr.Release(31); !errors.Is(err, ErrUnknownGroup) {
		t.Fatalf("expected ErrUnknownGroup on double release, got %v", err)
	}
}

// Direct unit tests for the pure CheckGroup policy.
func TestCheckGroupPolicy(t *testing.T) {
	clean := GroupInfo{ID: 1, Devices: []Device{
		{ID: "a", Driver: "vfio-pci"},
		{ID: "b", Driver: "pciback"},
	}}
	if err := CheckGroup(clean, nil); err != nil {
		t.Fatalf("clean group rejected: %v", err)
	}

	empty := GroupInfo{ID: 2}
	if err := CheckGroup(empty, nil); !errors.Is(err, ErrGroupEmpty) {
		t.Fatalf("expected ErrGroupEmpty, got %v", err)
	}

	hostDriven := GroupInfo{ID: 3, Devices: []Device{
		{ID: "a", Driver: "vfio-pci"},
		{ID: "b", Driver: "e1000e"},
	}}
	err := CheckGroup(hostDriven, nil)
	var integrity *IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("expected IntegrityError, got %v", err)
	}
	if integrity.Violations[0].DeviceID != "b" || integrity.Violations[0].Kind != ViolationHostDriver {
		t.Fatalf("unexpected violation: %+v", integrity.Violations[0])
	}

	// Custom allow-list via WithAllowedDrivers should accept an extra driver.
	custom := allowedDriverSet([]string{"my-vfio"})
	withCustom := GroupInfo{ID: 4, Devices: []Device{{ID: "a", Driver: "my-vfio"}}}
	if err := CheckGroup(withCustom, custom); err != nil {
		t.Fatalf("custom allowed driver rejected: %v", err)
	}
}

// The polling watcher emits add/remove events for watched directories.
func TestPollingWatcherEvents(t *testing.T) {
	f := newFakeSysfs(t)
	f.AddGroup(40)
	dir := f.GroupDevicesDir(40)

	w := NewPollingWatcher(5 * time.Millisecond)
	defer w.Close()
	if err := w.Add(dir); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	devDir := f.deviceDir("0000:0b:00.0")
	mustMkdir(t, devDir)
	mustSymlink(t, devDir, dir+"/0000:0b:00.0")

	if ev := nextEvent(t, w); ev.Op != OpAdd {
		t.Fatalf("expected OpAdd, got %s", ev.Op)
	}

	mustRemove(t, dir+"/0000:0b:00.0")
	if ev := nextEvent(t, w); ev.Op != OpRemove {
		t.Fatalf("expected OpRemove, got %s", ev.Op)
	}
}

func nextEvent(t *testing.T, w *PollingWatcher) Event {
	t.Helper()
	select {
	case ev := <-w.Events():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher event")
		return Event{}
	}
}
