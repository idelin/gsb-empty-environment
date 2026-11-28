package device

import (
	"fmt"
	"sync"
)

// Config describes an emulated ZNS namespace. A limit of 0 means "no limit"
// (the NVMe convention of 0 = no constraint is kept so callers can model large
// pools without tracking fake slots).
type Config struct {
	// NumZones is the number of zones in the namespace.
	NumZones int
	// ZoneSize is the descriptor size (in logical blocks) of every zone.
	ZoneSize int64
	// ZoneCapacity is the writable capacity of every zone in logical blocks.
	// It must be <= ZoneSize; the writable region ends at SLBA+ZoneCapacity.
	ZoneCapacity int64
	// MaxOpenZones (MOR). 0 means unconstrained.
	MaxOpenZones int
	// MaxActiveZones (MAR). 0 means unconstrained.
	MaxActiveZones int
}

// Stats is a counter snapshot exported by the emulated device.
type Stats struct {
	// PhysicalWrites counts logical blocks physically programmed on flash,
	// including GC migrations. It is the numerator of WAF.
	PhysicalWrites int64
	// AppendCount counts successful Write/ZoneAppend command invocations.
	AppendCount int64
	// ResetCount counts successful Zone Reset commands.
	ResetCount int64
	// EvictionCount counts implicit-open -> CLOSED evictions performed to free
	// an Open slot.
	EvictionCount int64
}

// Device is a thread-safe emulation of a ZNS namespace implementing the zone
// state machine, MOR/MAR resource accounting and strict write-pointer checks.
type Device struct {
	cfg Config
	mu  sync.RWMutex

	zones []*zone
	// data stores the currently programmed payload for each absolute LBA.
	data map[int64][]byte

	stats Stats
}

// New validates the configuration and constructs the namespace.
func New(cfg Config) (*Device, error) {
	if cfg.NumZones <= 0 {
		return nil, fmt.Errorf("%w: NumZones must be positive", ErrInvalidArgument)
	}
	if cfg.ZoneSize <= 0 {
		return nil, fmt.Errorf("%w: ZoneSize must be positive", ErrInvalidArgument)
	}
	if cfg.ZoneCapacity <= 0 || cfg.ZoneCapacity > cfg.ZoneSize {
		return nil, fmt.Errorf("%w: ZoneCapacity must satisfy 0 < ZoneCapacity <= ZoneSize", ErrInvalidArgument)
	}
	d := &Device{
		cfg:   cfg,
		zones: make([]*zone, cfg.NumZones),
		data:  make(map[int64][]byte),
	}
	for i := range d.zones {
		d.zones[i] = &zone{
			id:       i,
			state:    ZoneEmpty,
			slba:     int64(i) * cfg.ZoneSize,
			size:     cfg.ZoneSize,
			capacity: cfg.ZoneCapacity,
			wp:       int64(i) * cfg.ZoneSize,
		}
	}
	return d, nil
}

// Config returns the namespace configuration.
func (d *Device) Config() Config { return d.cfg }

// NumZones returns the number of zones.
func (d *Device) NumZones() int { return d.cfg.NumZones }

// zoneByLBA maps an absolute LBA to its zone, or returns an out-of-range error.
func (d *Device) zoneByLBA(lba int64) (*zone, error) {
	if lba < 0 || lba >= int64(d.cfg.NumZones)*d.cfg.ZoneSize {
		return nil, fmt.Errorf("%w: lba %d", ErrLBARange, lba)
	}
	return d.zones[lba/d.cfg.ZoneSize], nil
}

// Zone returns an immutable descriptor snapshot for the given zone id.
func (d *Device) Zone(id int) (ZoneInfo, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if id < 0 || id >= len(d.zones) {
		return ZoneInfo{}, zoneErr(id, "query", ErrZoneNotFound)
	}
	return d.zones[id].info(), nil
}

// Zones returns descriptor snapshots for the whole namespace.
func (d *Device) Zones() []ZoneInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]ZoneInfo, len(d.zones))
	for i, z := range d.zones {
		out[i] = z.info()
	}
	return out
}

// Stats returns a counter snapshot.
func (d *Device) Stats() Stats {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.stats
}

// countOpenLocked / countActiveLocked implement the resource accounting rules:
// Open   = IMPLICITLY_OPEN + EXPLICITLY_OPEN
// Active = Open + CLOSED
func (d *Device) countOpenLocked() int {
	n := 0
	for _, z := range d.zones {
		if z.state.IsOpen() {
			n++
		}
	}
	return n
}

func (d *Device) countActiveLocked() int {
	n := 0
	for _, z := range d.zones {
		if z.state.IsActive() {
			n++
		}
	}
	return n
}

// evictOneImplicitLocked closes the lowest-id IMPLICITLY_OPEN zone to release
// an Open slot. Per NVMe, explicit zones must never be auto-evicted.
func (d *Device) evictOneImplicitLocked() bool {
	for _, z := range d.zones {
		if z.state == ZoneImplicitlyOpen {
			z.state = ZoneClosed
			d.stats.EvictionCount++
			return true
		}
	}
	return false
}

// ensureOpenSlotLocked makes room for one additional open zone. It evicts a
// single implicitly open zone when MOR is full and fails when every open zone
// is explicitly protected.
func (d *Device) ensureOpenSlotLocked() error {
	if d.cfg.MaxOpenZones == 0 {
		return nil
	}
	if d.countOpenLocked() < d.cfg.MaxOpenZones {
		return nil
	}
	if !d.evictOneImplicitLocked() {
		return ErrTooManyOpenZones
	}
	return nil
}

// ensureActiveSlotLocked makes room for one additional ACTIVE zone. It is only
// consulted when activating a previously EMPTY zone; CLOSED/open zones already
// hold an active resource.
func (d *Device) ensureActiveSlotLocked() error {
	if d.cfg.MaxActiveZones == 0 {
		return nil
	}
	if d.countActiveLocked() < d.cfg.MaxActiveZones {
		return nil
	}
	return ErrTooManyActiveZones
}

// activateForWriteLocked transitions an EMPTY/CLOSED zone into an implicitly
// open zone, applying MOR/MAR accounting and implicit-only eviction.
func (d *Device) activateForWriteLocked(z *zone) error {
	switch z.state {
	case ZoneImplicitlyOpen, ZoneExplicitlyOpen:
		return nil
	case ZoneEmpty:
		if err := d.ensureActiveSlotLocked(); err != nil {
			return zoneErr(z.id, "write", err)
		}
		if err := d.ensureOpenSlotLocked(); err != nil {
			return zoneErr(z.id, "write", err)
		}
		z.state = ZoneImplicitlyOpen
		return nil
	case ZoneClosed:
		// The zone already owns an Active slot; only an Open slot is needed.
		if err := d.ensureOpenSlotLocked(); err != nil {
			return zoneErr(z.id, "write", err)
		}
		z.state = ZoneImplicitlyOpen
		return nil
	case ZoneFull:
		return zoneErr(z.id, "write", ErrInvalidZoneWrite)
	case ZoneReadOnly:
		return zoneErr(z.id, "write", ErrZoneReadOnly)
	case ZoneOffline:
		return zoneErr(z.id, "write", ErrZoneOffline)
	default:
		return zoneErr(z.id, "write", ErrInvalidZoneState)
	}
}

// Open issues a Zone Management Open action, making the zone EXPLICITLY_OPEN.
// Opening an EMPTY zone consumes both an Active and an Open slot.
func (d *Device) Open(zoneID int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if zoneID < 0 || zoneID >= len(d.zones) {
		return zoneErr(zoneID, string(opOpen), ErrZoneNotFound)
	}
	z := d.zones[zoneID]
	switch z.state {
	case ZoneExplicitlyOpen:
		return nil
	case ZoneImplicitlyOpen:
		// Promoting does not change any slot count.
		if err := d.ensureOpenSlotLocked(); err != nil {
			return zoneErr(z.id, string(opOpen), err)
		}
		z.state = ZoneExplicitlyOpen
		return nil
	case ZoneEmpty:
		if err := d.ensureActiveSlotLocked(); err != nil {
			return zoneErr(z.id, string(opOpen), err)
		}
		if err := d.ensureOpenSlotLocked(); err != nil {
			return zoneErr(z.id, string(opOpen), err)
		}
		z.state = ZoneExplicitlyOpen
		return nil
	case ZoneClosed:
		if err := d.ensureOpenSlotLocked(); err != nil {
			return zoneErr(z.id, string(opOpen), err)
		}
		z.state = ZoneExplicitlyOpen
		return nil
	case ZoneFull:
		return zoneErr(z.id, string(opOpen), ErrInvalidZoneState)
	case ZoneReadOnly:
		return zoneErr(z.id, string(opOpen), ErrZoneReadOnly)
	case ZoneOffline:
		return zoneErr(z.id, string(opOpen), ErrZoneOffline)
	default:
		return zoneErr(z.id, string(opOpen), ErrInvalidZoneState)
	}
}

// Close issues a Zone Management Close action. The zone keeps its Active slot
// but releases its Open slot.
func (d *Device) Close(zoneID int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if zoneID < 0 || zoneID >= len(d.zones) {
		return zoneErr(zoneID, string(opClose), ErrZoneNotFound)
	}
	z := d.zones[zoneID]
	switch z.state {
	case ZoneClosed:
		return nil
	case ZoneImplicitlyOpen, ZoneExplicitlyOpen:
		z.state = ZoneClosed
		return nil
	case ZoneFull:
		return zoneErr(z.id, string(opClose), ErrInvalidZoneState)
	case ZoneEmpty:
		return zoneErr(z.id, string(opClose), ErrInvalidZoneState)
	case ZoneReadOnly:
		return zoneErr(z.id, string(opClose), ErrZoneReadOnly)
	case ZoneOffline:
		return zoneErr(z.id, string(opClose), ErrZoneOffline)
	default:
		return zoneErr(z.id, string(opClose), ErrInvalidZoneState)
	}
}

// Finish issues a Zone Management Finish action: the zone becomes FULL and
// releases both Open and Active slots.
func (d *Device) Finish(zoneID int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if zoneID < 0 || zoneID >= len(d.zones) {
		return zoneErr(zoneID, string(opFinish), ErrZoneNotFound)
	}
	z := d.zones[zoneID]
	switch z.state {
	case ZoneFull:
		return nil
	case ZoneEmpty, ZoneImplicitlyOpen, ZoneExplicitlyOpen, ZoneClosed:
		z.state = ZoneFull
		z.wp = z.capacityEnd()
		return nil
	case ZoneReadOnly:
		return zoneErr(z.id, string(opFinish), ErrZoneReadOnly)
	case ZoneOffline:
		return zoneErr(z.id, string(opFinish), ErrZoneOffline)
	default:
		return zoneErr(z.id, string(opFinish), ErrInvalidZoneState)
	}
}

// Reset issues a Zone Management Reset action, returning the zone to EMPTY.
// Programmed payloads in the zone are dropped.
func (d *Device) Reset(zoneID int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if zoneID < 0 || zoneID >= len(d.zones) {
		return zoneErr(zoneID, string(opReset), ErrZoneNotFound)
	}
	z := d.zones[zoneID]
	switch z.state {
	case ZoneEmpty:
		return zoneErr(z.id, string(opReset), ErrInvalidZoneState)
	case ZoneOffline:
		return zoneErr(z.id, string(opReset), ErrZoneOffline)
	case ZoneImplicitlyOpen, ZoneExplicitlyOpen, ZoneClosed, ZoneFull, ZoneReadOnly:
		for lba := z.slba; lba < z.capacityEnd(); lba++ {
			delete(d.data, lba)
		}
		z.resetDescriptor()
		d.stats.ResetCount++
		return nil
	default:
		return zoneErr(z.id, string(opReset), ErrInvalidZoneState)
	}
}

// SetReadOnly transitions a writable zone to READ_ONLY (e.g. administrative
// action or device condition). Only descriptor queries and reads remain legal.
func (d *Device) SetReadOnly(zoneID int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if zoneID < 0 || zoneID >= len(d.zones) {
		return zoneErr(zoneID, string(opRO), ErrZoneNotFound)
	}
	z := d.zones[zoneID]
	if z.state == ZoneOffline {
		return zoneErr(z.id, string(opRO), ErrZoneOffline)
	}
	z.state = ZoneReadOnly
	return nil
}

// SetOffline transitions a zone to OFFLINE; all subsequent data access fails.
func (d *Device) SetOffline(zoneID int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if zoneID < 0 || zoneID >= len(d.zones) {
		return zoneErr(zoneID, string(opOffline), ErrZoneNotFound)
	}
	d.zones[zoneID].state = ZoneOffline
	return nil
}
