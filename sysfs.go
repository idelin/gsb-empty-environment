package netsec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// DefaultSysfsRoot is the conventional mount point of the sysfs virtual
// filesystem on a Linux host.
const DefaultSysfsRoot = "/sys"

// Device is a single member of an IOMMU group.
//
// ID is the device identifier as it appears under
// /sys/kernel/iommu_groups/<gid>/devices (for example a PCI B/D/F such as
// "0000:01:00.0"). Driver is the kernel driver the device is currently bound
// to; it is empty when the device is unbound. Path is the resolved absolute
// sysfs path of the device.
type Device struct {
	ID     string
	Driver string
	Path   string
}

// GroupInfo is a point-in-time snapshot of one IOMMU group.
type GroupInfo struct {
	ID      int
	Devices []Device
}

// Sysfs abstracts the subset of sysfs that the VFIO proxy needs to inspect.
//
// The real implementation talks to the Linux sysfs tree; tests substitute a
// fake implementation backed by a scratch directory, which keeps the whole
// subsystem unit-testable off-host.
type Sysfs interface {
	// InspectGroup returns a snapshot of the group identified by id.
	InspectGroup(id int) (GroupInfo, error)
}

// SysfsError wraps a failure while reading sysfs so callers can distinguish
// a broken/absent group from a security violation.
type SysfsError struct {
	Op   string
	Path string
	Err  error
}

func (e *SysfsError) Error() string {
	return "sysfs " + e.Op + " " + e.Path + ": " + e.Err.Error()
}

func (e *SysfsError) Unwrap() error { return e.Err }

// LinuxSysfs reads IOMMU group data from a sysfs tree rooted at Root.
type LinuxSysfs struct {
	Root string
}

// NewLinuxSysfs returns a Sysfs reader rooted at DefaultSysfsRoot.
func NewLinuxSysfs() *LinuxSysfs {
	return &LinuxSysfs{Root: DefaultSysfsRoot}
}

func (s *LinuxSysfs) groupPath(id int) string {
	return filepath.Join(s.Root, "kernel", "iommu_groups", strconv.Itoa(id))
}

// InspectGroup implements Sysfs.
func (s *LinuxSysfs) InspectGroup(id int) (GroupInfo, error) {
	groupDir := s.groupPath(id)
	entries, err := os.ReadDir(filepath.Join(groupDir, "devices"))
	if err != nil {
		return GroupInfo{ID: id}, &SysfsError{Op: "readdir", Path: groupDir, Err: err}
	}

	info := GroupInfo{ID: id}
	for _, entry := range entries {
		name := entry.Name()
		link := filepath.Join(groupDir, "devices", name)
		// The entries are symlinks such as ../../../../devices/pci0000:00/...
		resolved, err := filepath.EvalSymlinks(link)
		if err != nil {
			return GroupInfo{ID: id}, &SysfsError{Op: "resolve", Path: link, Err: err}
		}
		driver, err := readDriverName(resolved)
		if err != nil {
			return GroupInfo{ID: id}, err
		}
		info.Devices = append(info.Devices, Device{
			ID:     name,
			Driver: driver,
			Path:   resolved,
		})
	}

	sort.Slice(info.Devices, func(i, j int) bool {
		return info.Devices[i].ID < info.Devices[j].ID
	})
	return info, nil
}

// readDriverName returns the driver a device is bound to, or "" when the
// device has no driver. The driver is exposed as a "driver" symlink inside
// the device directory whose basename is the driver name.
func readDriverName(devicePath string) (string, error) {
	driverLink := filepath.Join(devicePath, "driver")
	target, err := os.Readlink(driverLink)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", &SysfsError{Op: "readlink", Path: driverLink, Err: err}
	}
	return filepath.Base(target), nil
}
