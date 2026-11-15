package netsec

import (
	"math"
	"sync"
)

// IOMMU owns the host page-ownership table and all per-device translation
// domains. Its zero value is ready for use, although New is preferred.
type IOMMU struct {
	mu sync.Mutex

	cond       *sync.Cond
	tenants    map[TenantID]struct{}
	pageOwners map[PhysPage]TenantID
	devices    map[DeviceID]*deviceState
	nextDomain DomainID
}

type deviceState struct {
	id         DeviceID
	tenant     TenantID
	domain     DomainID
	generation Generation
	mappings   map[uint64]*mapping
	retired    map[*mapping]struct{}
}

type mapping struct {
	owner      *deviceState
	domain     DomainID
	tenant     TenantID
	iovaPage   uint64
	physPage   PhysPage
	access     AccPerm
	generation Generation
	active     bool
	refs       int
}

// New returns an empty IOMMU.
func New() *IOMMU {
	m := &IOMMU{}
	m.initLocked()
	return m
}

// NewIOMMU is an alias for New.
func NewIOMMU() *IOMMU {
	return New()
}

func (m *IOMMU) initLocked() {
	if m.cond == nil {
		m.cond = sync.NewCond(&m.mu)
	}
	if m.tenants == nil {
		m.tenants = make(map[TenantID]struct{})
	}
	if m.pageOwners == nil {
		m.pageOwners = make(map[PhysPage]TenantID)
	}
	if m.devices == nil {
		m.devices = make(map[DeviceID]*deviceState)
	}
	if m.nextDomain == 0 {
		m.nextDomain = 1
	}
}

// AddTenant registers a tenant. Adding the same tenant again is idempotent.
func (m *IOMMU) AddTenant(id TenantID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	m.tenants[id] = struct{}{}
	return nil
}

// RegisterTenant is an alias for AddTenant.
func (m *IOMMU) RegisterTenant(id TenantID) error {
	return m.AddTenant(id)
}

// AssignPage gives a physical page to a tenant in the host ownership table.
//
// The assignment is immutable: assigning an already-owned page to another
// tenant fails with ErrPageOwned. Reassigning it to the same tenant succeeds.
// Devices cannot call this through Transfer and cannot change page ownership.
func (m *IOMMU) AssignPage(tenant TenantID, page PhysPage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	if _, ok := m.tenants[tenant]; !ok {
		return ErrUnknownTenant
	}
	if owner, ok := m.pageOwners[page]; ok && owner != tenant {
		return ErrPageOwned
	}
	m.pageOwners[page] = tenant
	return nil
}

// AssignPages assigns count consecutive physical pages to a tenant. The
// operation is all-or-nothing.
func (m *IOMMU) AssignPages(tenant TenantID, first PhysPage, count uint64) error {
	if count == 0 {
		return ErrInvalidLength
	}
	if count-1 > math.MaxUint64-uint64(first) {
		return ErrOverflow
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	if _, ok := m.tenants[tenant]; !ok {
		return ErrUnknownTenant
	}
	for i := uint64(0); i < count; i++ {
		page := PhysPage(uint64(first) + i)
		if owner, ok := m.pageOwners[page]; ok && owner != tenant {
			return ErrPageOwned
		}
	}
	for i := uint64(0); i < count; i++ {
		m.pageOwners[PhysPage(uint64(first)+i)] = tenant
	}
	return nil
}

// PageOwner returns the tenant that owns a physical page.
func (m *IOMMU) PageOwner(page PhysPage) (TenantID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	owner, ok := m.pageOwners[page]
	return owner, ok
}

// AddDevice registers a device in a fresh IOMMU domain.
func (m *IOMMU) AddDevice(id DeviceID, tenant TenantID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	if _, ok := m.tenants[tenant]; !ok {
		return ErrUnknownTenant
	}
	if _, ok := m.devices[id]; ok {
		return ErrDeviceExists
	}
	domain := m.nextDomain
	if domain == 0 {
		return ErrGenerationExhausted
	}
	m.nextDomain++
	m.devices[id] = &deviceState{
		id:         id,
		tenant:     tenant,
		domain:     domain,
		generation: 1,
		mappings:   make(map[uint64]*mapping),
		retired:    make(map[*mapping]struct{}),
	}
	return nil
}

// RegisterDevice is an alias for AddDevice.
func (m *IOMMU) RegisterDevice(id DeviceID, tenant TenantID) error {
	return m.AddDevice(id, tenant)
}

// DeviceInfo returns a snapshot of a device's domain and current generation.
func (m *IOMMU) DeviceInfo(id DeviceID) (DeviceInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	dev, ok := m.devices[id]
	if !ok {
		return DeviceInfo{}, false
	}
	return DeviceInfo{
		Device:     dev.id,
		Tenant:     dev.tenant,
		Domain:     dev.domain,
		Generation: dev.generation,
	}, true
}

// Map installs one page translation for a device. IOVA must be page aligned.
// The physical page must already be owned by the device's tenant.
func (m *IOMMU) Map(device DeviceID, iova uint64, page PhysPage, access AccPerm) error {
	if iova%PageSize != 0 {
		return ErrInvalidIOVA
	}
	if !access.valid() {
		return ErrInvalidAccess
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	dev, err := m.deviceLocked(device)
	if err != nil {
		return err
	}
	if err := m.checkPageOwnerLocked(dev, page); err != nil {
		return err
	}
	key := iova / PageSize
	if _, ok := dev.mappings[key]; ok {
		return ErrAlreadyMapped
	}
	dev.mappings[key] = &mapping{
		owner:      dev,
		domain:     dev.domain,
		tenant:     dev.tenant,
		iovaPage:   key,
		physPage:   page,
		access:     access,
		generation: dev.generation,
		active:     true,
	}
	return nil
}

// MapRange installs count consecutive translations. The operation is
// all-or-nothing.
func (m *IOMMU) MapRange(device DeviceID, iova uint64, length uint64, first PhysPage, access AccPerm) error {
	if iova%PageSize != 0 {
		return ErrInvalidIOVA
	}
	if length == 0 || length%PageSize != 0 {
		return ErrInvalidLength
	}
	if length > math.MaxUint64-iova {
		return ErrOverflow
	}
	if !access.valid() {
		return ErrInvalidAccess
	}
	pages := length / PageSize
	if pages-1 > math.MaxUint64-uint64(first) {
		return ErrOverflow
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	dev, err := m.deviceLocked(device)
	if err != nil {
		return err
	}
	firstPage := iova / PageSize
	for i := uint64(0); i < pages; i++ {
		if _, ok := dev.mappings[firstPage+i]; ok {
			return ErrAlreadyMapped
		}
		if err := m.checkPageOwnerLocked(dev, PhysPage(uint64(first)+i)); err != nil {
			return err
		}
	}
	for i := uint64(0); i < pages; i++ {
		key := firstPage + i
		dev.mappings[key] = &mapping{
			owner:      dev,
			domain:     dev.domain,
			tenant:     dev.tenant,
			iovaPage:   key,
			physPage:   PhysPage(uint64(first) + i),
			access:     access,
			generation: dev.generation,
			active:     true,
		}
	}
	return nil
}

// Remap replaces an existing page translation and advances the device's
// generation. Existing leases on the old translation remain valid and can
// finish; new transfers observe only the new translation.
func (m *IOMMU) Remap(device DeviceID, iova uint64, page PhysPage, access AccPerm) error {
	if iova%PageSize != 0 {
		return ErrInvalidIOVA
	}
	if !access.valid() {
		return ErrInvalidAccess
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	dev, err := m.deviceLocked(device)
	if err != nil {
		return err
	}
	key := iova / PageSize
	old, ok := dev.mappings[key]
	if !ok {
		return ErrNotMapped
	}
	if err := m.checkPageOwnerLocked(dev, page); err != nil {
		return err
	}
	if dev.generation == math.MaxUint64 {
		return ErrGenerationExhausted
	}
	dev.generation++
	old.active = false
	if old.refs != 0 {
		dev.retired[old] = struct{}{}
	}
	dev.mappings[key] = &mapping{
		owner:      dev,
		domain:     dev.domain,
		tenant:     dev.tenant,
		iovaPage:   key,
		physPage:   page,
		access:     access,
		generation: dev.generation,
		active:     true,
	}
	return nil
}

// Unmap revokes every current translation in [iova, iova+length).
//
// The mapping is removed immediately so no new transfer can acquire it. Unmap
// then waits for all outstanding leases on those pages, including leases on
// translations retired by an earlier Remap, before returning.
func (m *IOMMU) Unmap(device DeviceID, iova uint64, length uint64) error {
	if iova%PageSize != 0 {
		return ErrInvalidIOVA
	}
	if length == 0 || length%PageSize != 0 {
		return ErrInvalidLength
	}
	if length > math.MaxUint64-iova {
		return ErrOverflow
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	dev, err := m.deviceLocked(device)
	if err != nil {
		return err
	}
	first := iova / PageSize
	count := length / PageSize

	affected := make([]*mapping, 0, count)
	for i := uint64(0); i < count; i++ {
		entry, ok := dev.mappings[first+i]
		if !ok {
			return ErrNotMapped
		}
		affected = append(affected, entry)
	}
	for entry := range dev.retired {
		if entry.iovaPage >= first && entry.iovaPage < first+count {
			affected = append(affected, entry)
		}
	}
	if dev.generation == math.MaxUint64 {
		return ErrGenerationExhausted
	}
	dev.generation++
	for i := uint64(0); i < count; i++ {
		key := first + i
		entry := dev.mappings[key]
		entry.active = false
		if entry.refs != 0 {
			dev.retired[entry] = struct{}{}
		}
		delete(dev.mappings, key)
	}
	for _, entry := range affected {
		for entry.refs != 0 {
			m.cond.Wait()
		}
	}
	return nil
}

// Revoke is an alias for Unmap.
func (m *IOMMU) Revoke(device DeviceID, iova uint64, length uint64) error {
	return m.Unmap(device, iova, length)
}

// Lookup returns the current translation for the page containing iova.
func (m *IOMMU) Lookup(device DeviceID, iova uint64) (MappingInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	dev, ok := m.devices[device]
	if !ok {
		return MappingInfo{}, false
	}
	entry, ok := dev.mappings[iova/PageSize]
	if !ok || !entry.active {
		return MappingInfo{}, false
	}
	return entry.info(), true
}

// Stats returns lease and active-mapping counts for a device.
func (m *IOMMU) Stats(device DeviceID) (DeviceStats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	dev, ok := m.devices[device]
	if !ok {
		return DeviceStats{}, false
	}
	stats := DeviceStats{ActiveMappings: len(dev.mappings)}
	for _, entry := range dev.mappings {
		stats.Leases += entry.refs
	}
	for entry := range dev.retired {
		stats.Leases += entry.refs
	}
	return stats, true
}

func (m *IOMMU) deviceLocked(id DeviceID) (*deviceState, error) {
	dev, ok := m.devices[id]
	if !ok {
		return nil, ErrUnknownDevice
	}
	return dev, nil
}

func (m *IOMMU) checkPageOwnerLocked(dev *deviceState, page PhysPage) error {
	owner, ok := m.pageOwners[page]
	if !ok {
		return ErrPageUnowned
	}
	if owner != dev.tenant {
		return ErrForeignPage
	}
	return nil
}

func (e *mapping) info() MappingInfo {
	return MappingInfo{
		Device:     e.owner.id,
		Tenant:     e.tenant,
		Domain:     e.domain,
		Generation: e.generation,
		IOVA:       e.iovaPage * PageSize,
		PhysPage:   e.physPage,
		Access:     e.access,
	}
}
