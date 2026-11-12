package netsec

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// FakeSysfs is a scratch-directory backed Sysfs implementation used by tests.
// It mirrors the real Linux layout closely enough for LinuxSysfs to parse it:
//
//	<root>/kernel/iommu_groups/<gid>/devices/<dev> -> <root>/devices/<dev>
//	<root>/devices/<dev>/driver -> <root>/drivers/<driverName>
type FakeSysfs struct {
	t    *testing.T
	root string
}

func newFakeSysfs(t *testing.T) *FakeSysfs {
	t.Helper()
	root := t.TempDir()
	return &FakeSysfs{t: t, root: root}
}

// Root returns the scratch sysfs root.
func (f *FakeSysfs) Root() string { return f.root }

// Linux returns a real LinuxSysfs reader pointed at the fake root, so tests
// exercise the production parsing/symlink-resolution code path.
func (f *FakeSysfs) Linux() *LinuxSysfs {
	return &LinuxSysfs{Root: f.root}
}

func (f *FakeSysfs) groupDevicesDir(groupID int) string {
	return filepath.Join(f.root, "kernel", "iommu_groups", strconv.Itoa(groupID), "devices")
}

func (f *FakeSysfs) deviceDir(devID string) string {
	return filepath.Join(f.root, "devices", devID)
}

func (f *FakeSysfs) driverDir(driverName string) string {
	return filepath.Join(f.root, "drivers", driverName)
}

// GroupDevicesDir implements groupDirResolver for tests that inject the fake
// directly rather than going through LinuxSysfs.
func (f *FakeSysfs) GroupDevicesDir(groupID int) string {
	return f.groupDevicesDir(groupID)
}

// InspectGroup implements Sysfs by delegating to the production reader.
func (f *FakeSysfs) InspectGroup(groupID int) (GroupInfo, error) {
	return f.Linux().InspectGroup(groupID)
}

// AddGroup creates an empty IOMMU group directory.
func (f *FakeSysfs) AddGroup(groupID int) {
	f.t.Helper()
	mustMkdir(f.t, f.groupDevicesDir(groupID))
}

// AddDevice creates a device and joins it to the given group, then binds it to
// driverName (pass "" to leave the device unbound).
func (f *FakeSysfs) AddDevice(groupID int, devID, driverName string) {
	f.t.Helper()
	f.AddGroup(groupID)

	devDir := f.deviceDir(devID)
	mustMkdir(f.t, devDir)

	// devices/<dev> symlink inside the group points at the real device dir.
	link := filepath.Join(f.groupDevicesDir(groupID), devID)
	mustSymlink(f.t, devDir, link)

	if driverName != "" {
		f.BindDriver(devID, driverName)
	}
}

// BindDriver points the device's "driver" symlink at the named driver.
func (f *FakeSysfs) BindDriver(devID, driverName string) {
	f.t.Helper()
	mustMkdir(f.t, f.driverDir(driverName))
	link := filepath.Join(f.deviceDir(devID), "driver")
	// Remove any existing binding first so rebind is a single atomic replace.
	if _, err := os.Lstat(link); err == nil {
		mustRemove(f.t, link)
	}
	mustSymlink(f.t, f.driverDir(driverName), link)
}

// Unbind removes the device's "driver" symlink, leaving it with no driver.
func (f *FakeSysfs) Unbind(devID string) {
	f.t.Helper()
	mustRemove(f.t, filepath.Join(f.deviceDir(devID), "driver"))
}

// HotplugDevice creates a brand-new member inside an already monitored group.
func (f *FakeSysfs) HotplugDevice(groupID int, devID, driverName string) {
	f.t.Helper()
	f.AddDevice(groupID, devID, driverName)
}

// RemoveDevice simulates hot-removal by deleting the group membership link and
// the device directory.
func (f *FakeSysfs) RemoveDevice(groupID int, devID string) {
	f.t.Helper()
	mustRemove(f.t, filepath.Join(f.groupDevicesDir(groupID), devID))
	mustRemoveAll(f.t, f.deviceDir(devID))
}

// RemoveGroup deletes the whole IOMMU group directory.
func (f *FakeSysfs) RemoveGroup(groupID int) {
	f.t.Helper()
	mustRemoveAll(f.t, filepath.Join(f.root, "kernel", "iommu_groups", strconv.Itoa(groupID)))
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", p, err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %q -> %q: %v", link, target, err)
	}
}

func mustRemove(t *testing.T, p string) {
	t.Helper()
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove %q: %v", p, err)
	}
}

func mustRemoveAll(t *testing.T, p string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("removeall %q: %v", p, err)
	}
}
