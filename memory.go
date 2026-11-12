package netsec

import (
	"fmt"
	"math"
)

// MemoryType is a UEFI memory-map type. The numeric values match the values
// used by the UEFI specification so a boot loader can pass its map through
// without translation.
type MemoryType uint32

const (
	MemoryReserved MemoryType = iota
	MemoryLoaderCode
	MemoryLoaderData
	MemoryBootServicesCode
	MemoryBootServicesData
	MemoryRuntimeServicesCode
	MemoryRuntimeServicesData
	MemoryConventional
	MemoryUnusable
	MemoryACPIReclaim
	MemoryACPINVS
	MemoryMMIO
	MemoryMMIOPortSpace
	MemoryPalCode
	MemoryPersistent
)

// String returns a stable name suitable for diagnostics.
func (t MemoryType) String() string {
	switch t {
	case MemoryReserved:
		return "reserved"
	case MemoryLoaderCode:
		return "loader-code"
	case MemoryLoaderData:
		return "loader-data"
	case MemoryBootServicesCode:
		return "boot-services-code"
	case MemoryBootServicesData:
		return "boot-services-data"
	case MemoryRuntimeServicesCode:
		return "runtime-services-code"
	case MemoryRuntimeServicesData:
		return "runtime-services-data"
	case MemoryConventional:
		return "conventional"
	case MemoryUnusable:
		return "unusable"
	case MemoryACPIReclaim:
		return "acpi-reclaim"
	case MemoryACPINVS:
		return "acpi-nvs"
	case MemoryMMIO:
		return "mmio"
	case MemoryMMIOPortSpace:
		return "mmio-port-space"
	case MemoryPalCode:
		return "pal-code"
	case MemoryPersistent:
		return "persistent"
	default:
		return fmt.Sprintf("memory-type(%d)", uint32(t))
	}
}

// IsACPITableMemory reports whether tables may reside in this memory type.
// ACPI tables are only accepted from ACPI reclaimable memory or ACPI NVS.
func (t MemoryType) IsACPITableMemory() bool {
	return t == MemoryACPIReclaim || t == MemoryACPINVS
}

// MemoryRegion is one entry of a firmware memory map. The half-open interval
// [Base, Base+Length) is covered by the region.
type MemoryRegion struct {
	Base   uint64
	Length uint64
	Type   MemoryType
}

// End returns the exclusive end address of the region. The boolean is false
// when Base+Length would wrap around the 64-bit address space.
func (r MemoryRegion) End() (uint64, bool) {
	return addWithoutWrap(r.Base, r.Length)
}

// Contains reports whether [start, start+length) lies wholly inside the
// region and whether that range itself does not wrap.
func (r MemoryRegion) Contains(start, length uint64) bool {
	end, ok := addWithoutWrap(start, length)
	if !ok {
		return false
	}
	regionEnd, ok := r.End()
	if !ok {
		return false
	}
	return start >= r.Base && end <= regionEnd
}

// MemoryMap is the memory map supplied by the boot loader. The order of
// regions is irrelevant; lookups return the first matching region.
type MemoryMap struct {
	Regions []MemoryRegion
}

// Validate rejects a memory map whose region ranges wrap around the 64-bit
// address space. Such a map cannot be used for safe containment checks.
func (m MemoryMap) Validate() error {
	for i, region := range m.Regions {
		if _, ok := region.End(); !ok {
			return fmt.Errorf("%w: region[%d] base=%#x length=%#x",
				ErrAddressWrap, i, region.Base, region.Length)
		}
	}
	return nil
}

// findRegion returns a region that wholly contains [start, start+length).
// When acpiOnly is true, non-ACPI-table memory types are ignored.
func (m MemoryMap) findRegion(start, length uint64, acpiOnly bool) (MemoryRegion, bool) {
	for _, region := range m.Regions {
		if acpiOnly && !region.Type.IsACPITableMemory() {
			continue
		}
		if region.Contains(start, length) {
			return region, true
		}
	}
	return MemoryRegion{}, false
}

func addWithoutWrap(base, length uint64) (uint64, bool) {
	if length > math.MaxUint64-base {
		return 0, false
	}
	return base + length, true
}
