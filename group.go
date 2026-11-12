package netsec

import (
	"errors"
	"fmt"
)

// Drivers that are permitted to own a device when its IOMMU group is handed
// to a user (tenant/VM). vfio-pci is the real VFIO PCI platform driver; the
// remaining names are the common placeholder ("stub") drivers used to park a
// device away from a host driver before it is claimed by VFIO.
var defaultAllowedDrivers = map[string]struct{}{
	"vfio-pci":   {},
	"pci-stub":   {},
	"pciback":    {},
	"xhci-pci":   {},
	"virtio-pci": {},
}

// ViolationKind categorizes why a group failed the integrity check.
type ViolationKind int

const (
	// ViolationHostDriver means at least one device is still bound to a
	// driver that is neither a VFIO driver nor an allowed stub driver.
	ViolationHostDriver ViolationKind = iota
	// ViolationUnbound means at least one device has no driver at all.
	ViolationUnbound
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationHostDriver:
		return "host driver bound"
	case ViolationUnbound:
		return "device unbound"
	default:
		return "unknown violation"
	}
}

// DeviceViolation describes a single group member that fails the policy.
type DeviceViolation struct {
	DeviceID string
	Driver   string
	Kind     ViolationKind
}

func (v DeviceViolation) String() string {
	if v.Kind == ViolationUnbound {
		return fmt.Sprintf("device %s has no driver bound", v.DeviceID)
	}
	return fmt.Sprintf("device %s bound to host driver %q", v.DeviceID, v.Driver)
}

// IntegrityError is returned when a group is not safe to assign because at
// least one member is not on a VFIO or stub driver.
type IntegrityError struct {
	GroupID    int
	Violations []DeviceViolation
}

func (e *IntegrityError) Error() string {
	msg := fmt.Sprintf("iommu group %d is not safe for passthrough:", e.GroupID)
	for _, v := range e.Violations {
		msg += "\n  - " + v.String()
	}
	return msg
}

// ErrGroupEmpty is returned when an IOMMU group contains no devices. Such a
// group cannot be meaningfully assigned to a container.
var ErrGroupEmpty = errors.New("iommu group contains no devices")

// CheckGroup enforces the assignment policy on a group snapshot: every member
// device must be bound to a VFIO driver or to an allowed stub driver. A
// device still on a host driver (or unbound) makes the whole group unusable,
// because a peer in the same IOMMU group could otherwise be reached through
// peer-to-peer DMA.
//
// It returns nil when the group is safe to assign, *IntegrityError when one
// or more members violate the policy, or ErrGroupEmpty for an empty group.
func CheckGroup(info GroupInfo, allowedDrivers map[string]struct{}) error {
	if len(info.Devices) == 0 {
		return ErrGroupEmpty
	}
	if allowedDrivers == nil {
		allowedDrivers = defaultAllowedDrivers
	}

	var violations []DeviceViolation
	for _, dev := range info.Devices {
		if _, ok := allowedDrivers[dev.Driver]; ok {
			continue
		}
		kind := ViolationHostDriver
		if dev.Driver == "" {
			kind = ViolationUnbound
		}
		violations = append(violations, DeviceViolation{
			DeviceID: dev.ID,
			Driver:   dev.Driver,
			Kind:     kind,
		})
	}

	if len(violations) > 0 {
		return &IntegrityError{GroupID: info.ID, Violations: violations}
	}
	return nil
}

// allowedDriverSet merges the package defaults with any caller-provided
// driver names and returns a lookup set.
func allowedDriverSet(extra []string) map[string]struct{} {
	set := make(map[string]struct{}, len(defaultAllowedDrivers)+len(extra))
	for name := range defaultAllowedDrivers {
		set[name] = struct{}{}
	}
	for _, name := range extra {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	return set
}
