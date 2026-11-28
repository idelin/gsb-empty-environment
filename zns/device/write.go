package device

import (
	"fmt"
)

// prepareWriteLocked validates a sequential write request without mutating any
// zone state. It returns the target zone or a protocol error. All checks happen
// before activation so a rejected write never consumes MOR/MAR resources.
func (d *Device) prepareWriteLocked(lba, blocks int64) (*zone, error) {
	if blocks <= 0 {
		return nil, fmt.Errorf("%w: blocks must be positive", ErrInvalidArgument)
	}
	if lba < 0 {
		return nil, fmt.Errorf("%w: lba %d", ErrLBARange, lba)
	}
	z, err := d.zoneByLBA(lba)
	if err != nil {
		return nil, err
	}
	switch z.state {
	case ZoneFull:
		return nil, zoneErr(z.id, "write", ErrInvalidZoneWrite)
	case ZoneReadOnly:
		return nil, zoneErr(z.id, "write", ErrZoneReadOnly)
	case ZoneOffline:
		return nil, zoneErr(z.id, "write", ErrZoneOffline)
	}
	// Strict write-pointer rule: a regular write must begin exactly at WP.
	if lba != z.wp {
		return nil, zoneErr(z.id, "write", ErrInvalidZoneWrite)
	}
	// The writable region ends at SLBA+ZoneCapacity, which may be below
	// SLBA+ZoneSize. Never program beyond capacity.
	if lba+blocks > z.capacityEnd() {
		return nil, zoneErr(z.id, "write", ErrInvalidZoneWrite)
	}
	return z, nil
}

// programLocked activates the zone if needed, stores the payload and advances
// the write pointer, transitioning to FULL exactly at the capacity boundary.
func (d *Device) programLocked(z *zone, startLBA, blocks int64, payload []byte) error {
	if err := d.activateForWriteLocked(z); err != nil {
		return err
	}
	for i := int64(0); i < blocks; i++ {
		// Copy the payload so later caller mutations cannot corrupt flash state.
		d.data[startLBA+i] = append([]byte(nil), payload...)
	}
	z.wp += blocks
	d.stats.PhysicalWrites += blocks
	d.stats.AppendCount++
	if z.wp == z.capacityEnd() {
		// Capacity exhausted: release Open and Active resources automatically.
		z.state = ZoneFull
	}
	return nil
}

// Write issues a regular zone write. The starting LBA must equal the target
// zone's WritePointer and the request must not cross SLBA+ZoneCapacity.
func (d *Device) Write(lba, blocks int64, payload []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, err := d.prepareWriteLocked(lba, blocks)
	if err != nil {
		return err
	}
	return d.programLocked(z, lba, blocks, payload)
}

// ZoneAppend atomically allocates the next sequential position for the zone,
// programs the payload there and returns the assigned starting LBA. The host
// does not need to know the current write pointer.
func (d *Device) ZoneAppend(zoneID int, blocks int64, payload []byte) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if zoneID < 0 || zoneID >= len(d.zones) {
		return 0, zoneErr(zoneID, "ZoneAppend", ErrZoneNotFound)
	}
	z := d.zones[zoneID]
	// Reuse the same legality checks with the current write pointer as target.
	if _, err := d.prepareWriteLocked(z.wp, blocks); err != nil {
		return 0, err
	}
	assigned := z.wp
	if err := d.programLocked(z, assigned, blocks, payload); err != nil {
		return 0, err
	}
	return assigned, nil
}

// Read returns the payload programmed at an LBA. Reads are allowed in every
// state except OFFLINE; an unprogrammed LBA returns ErrInvalidZoneWrite.
func (d *Device) Read(lba int64) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	z, err := d.zoneByLBA(lba)
	if err != nil {
		return nil, err
	}
	if z.state == ZoneOffline {
		return nil, zoneErr(z.id, "read", ErrZoneOffline)
	}
	p, ok := d.data[lba]
	if !ok {
		return nil, zoneErr(z.id, "read", ErrInvalidZoneWrite)
	}
	return append([]byte(nil), p...), nil
}
