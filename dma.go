// Package netsec implements an in-process simulation of an IOMMU address
// translation service for a sandbox of virtual DMA-capable devices.
//
// The simulation is pure software: it never touches physical hardware, real
// DMA engines, kernel ioctls or platform IOV tables. A trusted Host owns the
// physical-page ownership table; an untrusted device-facing IOMMU translates
// I/O virtual addresses (IOVAs) into physical ranges on behalf of device
// DMA requests.
//
// Security model:
//
//   - Each device gets an independent translation domain with its own page
//     table and monotonically increasing generation counter.
//   - A page is 4096 bytes. Mappings carry read and/or write permissions.
//   - Every physical page belongs to at most one tenant. The ownership table
//     is maintained only through the trusted Host API and cannot be altered
//     through the device-facing translation API.
//   - A device-supplied scatter-gather list is validated in full before any
//     host memory callback runs. A missing page, insufficient permission,
//     address overflow, or mixed tenant/domain anywhere in the list fails
//     the whole request without side effects.
//   - Successful validation atomically takes a lease on every touched page.
//     Remapping, unmapping and revoking bump the domain generation, which
//     invalidates old translations, while leases already held keep a transfer
//     valid until released. Revoke only returns after every old lease ends.
package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// PageSize is the simulated page size in bytes.
const PageSize = 4096

// pageMask selects the in-page offset of an address.
const pageMask = PageSize - 1

// DefaultMaxTransfer is the default aggregate byte cap for one scatter-gather
// request. The cap bounds the work an attacker-controlled length can force.
const DefaultMaxTransfer = 64 * 1024 * 1024

// Perm is the permission set carried by a mapping.
type Perm uint8

const (
	PermRead  Perm = 1 << iota // device may read target memory
	PermWrite                  // device may write target memory
)

// String renders a permission set such as "rw" or "r-".
func (p Perm) String() string {
	r := '-'
	w := '-'
	if p&PermRead != 0 {
		r = 'r'
	}
	if p&PermWrite != 0 {
		w = 'w'
	}
	return string([]rune{r, w})
}

// Contains reports whether p contains every bit in want.
func (p Perm) Contains(want Perm) bool {
	return p&want == want
}

// TenantID identifies a trusted tenant that owns physical pages.
type TenantID uint64

// DeviceID identifies a DMA-capable device.
type DeviceID uint64

// IOVA is an I/O virtual address supplied by a device.
type IOVA uint64

// PhysAddr is a simulated physical address.
type PhysAddr uint64

// Sentinel errors returned by the package. All errors are wrapped with
// context, so callers should use errors.Is rather than comparing directly.
var (
	// ErrBadRequest is the base error for malformed device requests.
	ErrBadRequest = errors.New("dma: bad request")
	// ErrEmptyList marks an empty scatter-gather list or zero-length entry.
	ErrEmptyList = fmt.Errorf("%w: empty scatter-gather list", ErrBadRequest)
	// ErrOverflow marks an IOVA+length or address-space overflow.
	ErrOverflow = fmt.Errorf("%w: address overflow", ErrBadRequest)
	// ErrTooLarge marks a request exceeding the configured transfer cap.
	ErrTooLarge = fmt.Errorf("%w: transfer exceeds configured limit", ErrBadRequest)
	// ErrUnaligned marks a mapping whose IOVA or physical address is not
	// page aligned.
	ErrUnaligned = fmt.Errorf("%w: address must be %d-byte aligned", ErrBadRequest, PageSize)
	// ErrUnknownDevice marks a request from a device that has no domain.
	ErrUnknownDevice = errors.New("dma: unknown device")
	// ErrUnmapped marks an IOVA page with no translation in the current
	// generation.
	ErrUnmapped = errors.New("dma: iova page not mapped")
	// ErrPermission marks a mapping that lacks the requested permission.
	ErrPermission = errors.New("dma: permission denied")
	// ErrMixedTenant marks pages in one request belonging to different
	// tenants.
	ErrMixedTenant = errors.New("dma: mixed tenant pages in transfer")
	// ErrOwnerMismatch marks a page table entry whose tenant does not own
	// the backing physical page according to the Host ownership table.
	ErrOwnerMismatch = errors.New("dma: physical page ownership mismatch")
	// ErrConflict marks an attempt to map an IOVA that is already mapped or
	// a physical page that is already owned by another tenant.
	ErrConflict = errors.New("dma: mapping conflict")
	// ErrStale marks a lease used after its domain generation was superseded.
	ErrStale = errors.New("dma: stale generation lease")
	// ErrCallback marks a transfer aborted because a memory callback failed.
	ErrCallback = errors.New("dma: memory callback failed")
)

// Host is the trusted component that owns the physical-page ownership table.
// Its methods are not reachable through the device-facing IOMMU API: an
// attacker controlling device IDs, IOVAs and lengths cannot modify ownership.
type Host struct {
	mu     sync.RWMutex
	owners map[uint64]TenantID // physical frame number -> owning tenant
}

// NewHost creates an empty trusted host.
func NewHost() *Host {
	return &Host{owners: make(map[uint64]TenantID)}
}

// Assign records that the physical page containing addr belongs to tenant.
// An address must be page aligned and a physical frame may only be assigned
// once. Assigning the same frame again to the same tenant is idempotent.
func (h *Host) Assign(addr PhysAddr, tenant TenantID) error {
	if addr&pageMask != 0 {
		return fmt.Errorf("%w: physical address %#x", ErrUnaligned, uint64(addr))
	}
	pfn := uint64(addr) / PageSize
	h.mu.Lock()
	defer h.mu.Unlock()
	if owner, ok := h.owners[pfn]; ok && owner != tenant {
		return fmt.Errorf("%w: frame %#x belongs to tenant %d, refuse tenant %d",
			ErrConflict, pfn, owner, tenant)
	}
	h.owners[pfn] = tenant
	return nil
}

// OwnerOf returns the tenant owning the physical frame containing addr.
func (h *Host) OwnerOf(addr PhysAddr) (TenantID, bool) {
	pfn := uint64(addr) / PageSize
	h.mu.RLock()
	defer h.mu.RUnlock()
	tenant, ok := h.owners[pfn]
	return tenant, ok
}

// pte is a single simulated page table entry.
type pte struct {
	pfn    uint64   // mapped physical frame number
	perm   Perm     // device-visible permission set
	tenant TenantID // tenant the mapping was created for
}

// Domain is one device's independent translation domain. The mutex protects
// the page table, generation counter and lease accounting.
type Domain struct {
	id DeviceID

	mu       sync.Mutex
	tab      map[uint64]pte // IOVA frame number -> entry
	gen      uint64         // current translation generation
	leases   int            // leases taken against any generation
	cond     *sync.Cond     // signaled when leases return to zero
	maxBytes uint64         // per-request aggregate cap
	host     *Host
}

// IOMMU is the untrusted-facing translation service. Device requests reach
// the domains only through methods of this type.
type IOMMU struct {
	mu      sync.RWMutex
	domains map[DeviceID]*Domain
	host    *Host
}

// NewIOMMU creates a translation service backed by the trusted host.
func NewIOMMU(host *Host) *IOMMU {
	return &IOMMU{
		domains: make(map[DeviceID]*Domain),
		host:    host,
	}
}

// NewDomain gives device its own isolated translation domain. A device ID can
// only be registered once.
func (io *IOMMU) NewDomain(device DeviceID) (*Domain, error) {
	io.mu.Lock()
	defer io.mu.Unlock()
	if _, exists := io.domains[device]; exists {
		return nil, fmt.Errorf("%w: device %d already has a domain", ErrConflict, device)
	}
	d := &Domain{
		id:       device,
		tab:      make(map[uint64]pte),
		gen:      1,
		maxBytes: DefaultMaxTransfer,
		host:     io.host,
	}
	d.cond = sync.NewCond(&d.mu)
	io.domains[device] = d
	return d, nil
}

// domain resolves a device to its domain.
func (io *IOMMU) domain(device DeviceID) (*Domain, error) {
	io.mu.RLock()
	d, ok := io.domains[device]
	io.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: device %d", ErrUnknownDevice, device)
	}
	return d, nil
}

// Device returns the device that owns the domain.
func (d *Domain) Device() DeviceID { return d.id }

// Generation returns the current translation generation. Every mapping
// change (map, remap, unmap, revoke) advances the generation and therefore
// invalidates translations taken against earlier generations.
func (d *Domain) Generation() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gen
}

// ActiveLeases returns the number of currently held transfer leases. It is
// mainly useful for tests and diagnostics.
func (d *Domain) ActiveLeases() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.leases
}

// SetMaxTransfer overrides the aggregate per-request byte cap. Zero restores
// the package default.
func (d *Domain) SetMaxTransfer(limit uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if limit == 0 {
		d.maxBytes = DefaultMaxTransfer
	} else {
		d.maxBytes = limit
	}
}

// checkTarget validates alignment and host ownership of a mapping target and
// returns the IOVA frame number and physical frame number.
func (d *Domain) checkTarget(iova IOVA, phys PhysAddr, perm Perm) (uint64, uint64, error) {
	if perm == 0 {
		return 0, 0, fmt.Errorf("%w: mapping needs at least one permission", ErrBadRequest)
	}
	if iova&pageMask != 0 || phys&pageMask != 0 {
		return 0, 0, fmt.Errorf("%w: iova %#x phys %#x", ErrUnaligned, uint64(iova), uint64(phys))
	}
	return uint64(iova) / PageSize, uint64(phys) / PageSize, nil
}

// Map installs a page-aligned mapping from iova to phys for tenant, carrying
// the given permission set. The IOVA frame must not already be mapped and the
// physical frame must be assigned to the same tenant in the host table.
func (d *Domain) Map(iova IOVA, phys PhysAddr, perm Perm, tenant TenantID) error {
	ifn, pfn, err := d.checkTarget(iova, phys, perm)
	if err != nil {
		return err
	}
	owner, ok := d.host.OwnerOf(phys)
	if !ok {
		return fmt.Errorf("%w: physical frame %#x is not assigned", ErrOwnerMismatch, pfn)
	}
	if owner != tenant {
		return fmt.Errorf("%w: frame %#x owned by tenant %d, requested by %d",
			ErrOwnerMismatch, pfn, owner, tenant)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.tab[ifn]; exists {
		return fmt.Errorf("%w: iova frame %#x already mapped", ErrConflict, ifn)
	}
	d.tab[ifn] = pte{pfn: pfn, perm: perm, tenant: tenant}
	d.gen++
	return nil
}

// Remap replaces an existing mapping atomically and advances the generation,
// invalidating all translations from earlier generations. The new target must
// be assigned to tenant in the host table. Leases already taken are not
// affected and remain usable until released.
func (d *Domain) Remap(iova IOVA, phys PhysAddr, perm Perm, tenant TenantID) error {
	ifn, pfn, err := d.checkTarget(iova, phys, perm)
	if err != nil {
		return err
	}
	owner, ok := d.host.OwnerOf(phys)
	if !ok {
		return fmt.Errorf("%w: physical frame %#x is not assigned", ErrOwnerMismatch, pfn)
	}
	if owner != tenant {
		return fmt.Errorf("%w: frame %#x owned by tenant %d, requested by %d",
			ErrOwnerMismatch, pfn, owner, tenant)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	old, exists := d.tab[ifn]
	if !exists {
		return fmt.Errorf("%w: iova frame %#x", ErrUnmapped, ifn)
	}
	if old.tenant != tenant {
		return fmt.Errorf("%w: iova frame %#x belongs to tenant %d, requested by %d",
			ErrOwnerMismatch, ifn, old.tenant, tenant)
	}
	d.tab[ifn] = pte{pfn: pfn, perm: perm, tenant: tenant}
	d.gen++
	return nil
}

// Unmap removes a single mapping and advances the generation. It returns
// ErrUnmapped if the IOVA frame has no current mapping.
func (d *Domain) Unmap(iova IOVA) error {
	ifn := uint64(iova) / PageSize
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.tab[ifn]; !ok {
		return fmt.Errorf("%w: iova frame %#x", ErrUnmapped, ifn)
	}
	delete(d.tab, ifn)
	d.gen++
	return nil
}

// Revoke tears down every mapping in the domain, advances the generation so
// all old translations fail, and blocks until every lease taken before the
// revocation has been released. New requests cannot acquire mappings while
// the table is empty.
func (d *Domain) Revoke() {
	d.mu.Lock()
	d.tab = make(map[uint64]pte)
	d.gen++
	for d.leases != 0 {
		d.cond.Wait()
	}
	d.mu.Unlock()
}
