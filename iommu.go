// Package netsec implements IOMMU domain management for a VFIO-style
// device passthrough mapping interface on a Go virtualization host.
//
// Security invariants enforced by this package:
//   - Reserved IOVA ranges (including the MSI interrupt window
//     0xFEE00000-0xFEEFFFFF) can never be mapped.
//   - IOVA ranges within a domain never overlap.
//   - Mapping permissions never exceed the permissions of the
//     underlying guest physical pages.
//   - Guest physical pages must belong to the guest that owns the domain.
//   - A device's domain is determined solely by its IOMMU group.
package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// PageSize is the granularity of all mappings and guest memory regions.
const PageSize uint64 = 4096

// Security and validation errors returned by mapping operations.
var (
	ErrAddressWrap   = errors.New("iommu: address range wraps around")
	ErrUnaligned     = errors.New("iommu: address or size not page aligned")
	ErrInvalidSize   = errors.New("iommu: mapping size must be positive")
	ErrReservedIOVA  = errors.New("iommu: IOVA range intersects a reserved window")
	ErrOverlap       = errors.New("iommu: IOVA range overlaps an existing mapping")
	ErrPermExceed    = errors.New("iommu: requested permissions exceed guest page permissions")
	ErrForeignPage   = errors.New("iommu: guest physical range not owned by this guest")
	ErrGroupAttached = errors.New("iommu: IOMMU group already attached to another domain")
	ErrNotFound      = errors.New("iommu: object not found")
	ErrUnmapPartial  = errors.New("iommu: unmap range partially covers an existing mapping")
)

// Perm is a bitmask of access permissions for a mapping or memory region.
type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
)

// Contains reports whether p grants every permission in q.
func (p Perm) Contains(q Perm) bool { return p&q == q }

// Range is a half-open address interval [Base, End).
type Range struct {
	Base uint64
	End  uint64
}

// Len returns the size of the range in bytes.
func (r Range) Len() uint64 { return r.End - r.Base }

// Overlaps reports whether r and o share at least one byte.
func (r Range) Overlaps(o Range) bool { return r.Base < o.End && o.Base < r.End }

// Contains reports whether addr lies within r.
func (r Range) Contains(addr uint64) bool { return r.Base <= addr && addr < r.End }

// rangeFrom converts a base+size pair into a Range, rejecting zero sizes
// and ranges that wrap around the 64-bit address space.
func rangeFrom(base, size uint64) (Range, error) {
	if size == 0 {
		return Range{}, ErrInvalidSize
	}
	end := base + size
	if end < base || end == 0 {
		return Range{}, ErrAddressWrap
	}
	return Range{Base: base, End: end}, nil
}

// aligned reports whether every value is a multiple of PageSize.
func aligned(values ...uint64) bool {
	for _, v := range values {
		if v%PageSize != 0 {
			return false
		}
	}
	return true
}

// MemoryRegion is a contiguous range of guest physical memory owned by a
// guest, together with the maximum permissions any mapping of these pages
// may carry.
type MemoryRegion struct {
	Base uint64
	Size uint64
	Perm Perm
}

func (r MemoryRegion) interval() Range { return Range{Base: r.Base, End: r.Base + r.Size} }

// Guest represents a virtual machine and the physical address space it owns.
type Guest struct {
	ID      string
	regions []MemoryRegion // sorted by Base, non-overlapping
}

// AddRegion registers a range of guest physical memory owned by the guest.
// Regions must be page aligned, non-wrapping, and non-overlapping.
func (g *Guest) AddRegion(base, size uint64, perm Perm) error {
	if !aligned(base, size) {
		return ErrUnaligned
	}
	rg, err := rangeFrom(base, size)
	if err != nil {
		return err
	}
	i := sort.Search(len(g.regions), func(i int) bool {
		return g.regions[i].Base >= base
	})
	if i < len(g.regions) && g.regions[i].interval().Overlaps(rg) {
		return ErrOverlap
	}
	if i > 0 && g.regions[i-1].interval().Overlaps(rg) {
		return ErrOverlap
	}
	g.regions = append(g.regions, MemoryRegion{})
	copy(g.regions[i+1:], g.regions[i:])
	g.regions[i] = MemoryRegion{Base: base, Size: size, Perm: perm}
	return nil
}

// covers reports whether every page of rg is owned by the guest and
// grants at least perm. A zero perm checks ownership only.
func (g *Guest) covers(rg Range, perm Perm) bool {
	pos := rg.Base
	for pos < rg.End {
		i := sort.Search(len(g.regions), func(i int) bool {
			return g.regions[i].interval().End > pos
		})
		if i >= len(g.regions) || !g.regions[i].interval().Contains(pos) {
			return false
		}
		if !g.regions[i].Perm.Contains(perm) {
			return false
		}
		pos = g.regions[i].interval().End
	}
	return true
}

// mapping is one IOVA -> GPA translation installed in a domain.
type mapping struct {
	iova Range
	gpa  uint64
	perm Perm
}

// Domain is an IOMMU domain: an isolated IOVA address space bound to
// exactly one guest. Devices reach a domain only through their IOMMU group.
type Domain struct {
	ID       string
	guest    *Guest
	mappings []mapping // sorted by iova.Base, non-overlapping
	reserved []Range   // sorted by Base, non-overlapping
	groups   map[int]bool
}

// Guest returns the guest that owns this domain.
func (d *Domain) Guest() *Guest { return d.guest }

// AddReservedRange adds a domain-specific reserved IOVA window on top of
// the platform defaults. Reserved windows can never be mapped.
func (d *Domain) AddReservedRange(base, size uint64) error {
	rg, err := rangeFrom(base, size)
	if err != nil {
		return err
	}
	for _, r := range d.reserved {
		if r.Overlaps(rg) {
			return ErrOverlap
		}
	}
	d.reserved = append(d.reserved, rg)
	sort.Slice(d.reserved, func(i, j int) bool { return d.reserved[i].Base < d.reserved[j].Base })
	return nil
}

func (d *Domain) isReserved(rg Range) bool {
	i := sort.Search(len(d.reserved), func(i int) bool {
		return d.reserved[i].End > rg.Base
	})
	return i < len(d.reserved) && d.reserved[i].Overlaps(rg)
}

// Map installs a translation of [iova, iova+size) to the guest physical
// range [gpa, gpa+size) with the given permissions. It enforces, in order:
// alignment, address wraparound, reserved windows, guest ownership and
// permission ceilings, and IOVA overlap.
func (d *Domain) Map(iova, gpa, size uint64, perm Perm) error {
	if !aligned(iova, gpa, size) {
		return ErrUnaligned
	}
	ioRange, err := rangeFrom(iova, size)
	if err != nil {
		return err
	}
	gpaRange, err := rangeFrom(gpa, size)
	if err != nil {
		return err
	}
	if d.isReserved(ioRange) {
		return fmt.Errorf("%w: [0x%x, 0x%x)", ErrReservedIOVA, ioRange.Base, ioRange.End)
	}
	if !d.guest.covers(gpaRange, perm) {
		if !d.guest.covers(gpaRange, 0) {
			return fmt.Errorf("%w: GPA [0x%x, 0x%x)", ErrForeignPage, gpaRange.Base, gpaRange.End)
		}
		return fmt.Errorf("%w: GPA [0x%x, 0x%x) perm %d", ErrPermExceed, gpaRange.Base, gpaRange.End, perm)
	}
	i := sort.Search(len(d.mappings), func(i int) bool {
		return d.mappings[i].iova.End > ioRange.Base
	})
	if i < len(d.mappings) && d.mappings[i].iova.Overlaps(ioRange) {
		return fmt.Errorf("%w: [0x%x, 0x%x)", ErrOverlap, ioRange.Base, ioRange.End)
	}
	d.mappings = append(d.mappings, mapping{})
	copy(d.mappings[i+1:], d.mappings[i:])
	d.mappings[i] = mapping{iova: ioRange, gpa: gpa, perm: perm}
	return nil
}

// Unmap removes every mapping fully contained in [iova, iova+size).
// A range that partially covers an existing mapping is rejected to avoid
// silently splitting translations.
func (d *Domain) Unmap(iova, size uint64) error {
	if !aligned(iova, size) {
		return ErrUnaligned
	}
	rg, err := rangeFrom(iova, size)
	if err != nil {
		return err
	}
	kept := d.mappings[:0]
	removed := false
	for _, m := range d.mappings {
		if !m.iova.Overlaps(rg) {
			kept = append(kept, m)
			continue
		}
		if rg.Base <= m.iova.Base && m.iova.End <= rg.End {
			removed = true
			continue
		}
		return ErrUnmapPartial
	}
	if !removed {
		return ErrNotFound
	}
	d.mappings = kept
	return nil
}

// Lookup translates an IOVA to its guest physical address and permissions.
func (d *Domain) Lookup(iova uint64) (gpa uint64, perm Perm, ok bool) {
	i := sort.Search(len(d.mappings), func(i int) bool {
		return d.mappings[i].iova.End > iova
	})
	if i >= len(d.mappings) || !d.mappings[i].iova.Contains(iova) {
		return 0, 0, false
	}
	m := d.mappings[i]
	return m.gpa + (iova - m.iova.Base), m.perm, true
}

// Group is an IOMMU group: the smallest set of devices isolated from the
// rest of the system. All devices in a group share one domain, and the
// domain of a device is decided only by its group.
type Group struct {
	ID      int
	devices []string
	domain  *Domain
}

// Manager owns guests, domains, and IOMMU groups, and routes devices to
// domains exclusively through their group.
type Manager struct {
	guests  map[string]*Guest
	domains map[string]*Domain
	groups  map[int]*Group
	devices map[string]*Group
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{
		guests:  make(map[string]*Guest),
		domains: make(map[string]*Domain),
		groups:  make(map[int]*Group),
		devices: make(map[string]*Group),
	}
}

// NewGuest registers a guest with the given ID.
func (m *Manager) NewGuest(id string) (*Guest, error) {
	if _, ok := m.guests[id]; ok {
		return nil, fmt.Errorf("iommu: guest %q already exists", id)
	}
	g := &Guest{ID: id}
	m.guests[id] = g
	return g, nil
}

// NewDomain creates an IOMMU domain bound to the given guest, preloaded
// with the platform reserved IOVA windows.
func (m *Manager) NewDomain(id, guestID string) (*Domain, error) {
	g, ok := m.guests[guestID]
	if !ok {
		return nil, fmt.Errorf("%w: guest %q", ErrNotFound, guestID)
	}
	if _, ok := m.domains[id]; ok {
		return nil, fmt.Errorf("iommu: domain %q already exists", id)
	}
	d := &Domain{
		ID:       id,
		guest:    g,
		reserved: DefaultReservedRanges(),
		groups:   make(map[int]bool),
	}
	m.domains[id] = d
	return d, nil
}

// RegisterDevice records that a device belongs to the given IOMMU group.
func (m *Manager) RegisterDevice(deviceID string, groupID int) error {
	if _, ok := m.devices[deviceID]; ok {
		return fmt.Errorf("iommu: device %q already registered", deviceID)
	}
	grp, ok := m.groups[groupID]
	if !ok {
		grp = &Group{ID: groupID}
		m.groups[groupID] = grp
	}
	grp.devices = append(grp.devices, deviceID)
	m.devices[deviceID] = grp
	return nil
}

// AttachDevice attaches the device's whole IOMMU group to the domain.
// If the group is already attached to a different domain the request is
// rejected: a group, and therefore every device in it, lives in exactly
// one domain.
func (m *Manager) AttachDevice(domainID, deviceID string) error {
	d, ok := m.domains[domainID]
	if !ok {
		return fmt.Errorf("%w: domain %q", ErrNotFound, domainID)
	}
	grp, ok := m.devices[deviceID]
	if !ok {
		return fmt.Errorf("%w: device %q", ErrNotFound, deviceID)
	}
	if grp.domain != nil && grp.domain != d {
		return fmt.Errorf("%w: group %d bound to domain %q", ErrGroupAttached, grp.ID, grp.domain.ID)
	}
	grp.domain = d
	d.groups[grp.ID] = true
	return nil
}

// DomainOfDevice resolves the domain a device DMAs into. The answer is
// determined solely by the device's IOMMU group, never by device identity.
func (m *Manager) DomainOfDevice(deviceID string) (*Domain, error) {
	grp, ok := m.devices[deviceID]
	if !ok {
		return nil, fmt.Errorf("%w: device %q", ErrNotFound, deviceID)
	}
	if grp.domain == nil {
		return nil, fmt.Errorf("%w: group %d unattached", ErrNotFound, grp.ID)
	}
	return grp.domain, nil
}
