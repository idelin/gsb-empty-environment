package netsec

import (
	"bytes"
	"fmt"
	"sync"
)

// Device is the block-device substitute used by a Volume.
//
// SectorSize must equal PhysicalSectorSize. SectorCount describes the complete
// device, including the private state-log area at the beginning.
type Device interface {
	SectorSize() int
	SectorCount() uint64
	ReadSector(sector uint64, dst []byte) error
	WriteSector(sector uint64, src []byte) error
	Sync() error
}

// DeviceOperation identifies an operation passed to a FaultInjector.
type DeviceOperation uint8

const (
	DeviceRead DeviceOperation = iota + 1
	DeviceWrite
	DeviceSync
)

// FaultContext describes a device operation. For writes, Data is the exact
// buffer that would be committed. A hook may set TornBytes and return an error
// to persist only that prefix. TornBytes is also honored without an explicit
// error, in which case ErrInjected is returned.
type FaultContext struct {
	Operation DeviceOperation
	Sector    uint64
	Data      []byte
	TornBytes int
}

// FaultInjector can fail or tear individual device operations.
type FaultInjector func(*FaultContext) error

// MemoryDevice is an in-memory, fault-injectable Device. Its methods are safe
// for concurrent use.
type MemoryDevice struct {
	mu      sync.RWMutex
	faultMu sync.RWMutex
	sectors uint64
	size    int
	data    []byte
	fault   FaultInjector

	readCount  uint64
	writeCount uint64
	syncCount  uint64
}

// NewMemoryDevice allocates a zero-filled memory device using
// PhysicalSectorSize-byte sectors.
func NewMemoryDevice(sectors uint64) *MemoryDevice {
	return &MemoryDevice{
		sectors: sectors,
		size:    PhysicalSectorSize,
		data:    make([]byte, int(sectors)*PhysicalSectorSize),
	}
}

func (d *MemoryDevice) SectorSize() int {
	return d.size
}

func (d *MemoryDevice) SectorCount() uint64 {
	return d.sectors
}

// SetFaultInjector installs or removes a fault injector.
func (d *MemoryDevice) SetFaultInjector(inject FaultInjector) {
	d.faultMu.Lock()
	d.fault = inject
	d.faultMu.Unlock()
}

func (d *MemoryDevice) ReadSector(sector uint64, dst []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.readCount++
	if sector >= d.sectors {
		return fmt.Errorf("%w: physical sector %d", ErrInvalidSector, sector)
	}
	if len(dst) != d.size {
		return fmt.Errorf("%w: got %d bytes", ErrInvalidSize, len(dst))
	}
	if inject := d.faultInjector(); inject != nil {
		ctx := &FaultContext{Operation: DeviceRead, Sector: sector}
		if err := inject(ctx); err != nil {
			return err
		}
	}
	off := int(sector) * d.size
	copy(dst, d.data[off:off+d.size])
	return nil
}

func (d *MemoryDevice) WriteSector(sector uint64, src []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.writeCount++
	if sector >= d.sectors {
		return fmt.Errorf("%w: physical sector %d", ErrInvalidSector, sector)
	}
	if len(src) != d.size {
		return fmt.Errorf("%w: got %d bytes", ErrInvalidSize, len(src))
	}

	buf := append([]byte(nil), src...)
	var hookErr error
	torn := 0
	if inject := d.faultInjector(); inject != nil {
		ctx := &FaultContext{Operation: DeviceWrite, Sector: sector, Data: buf}
		hookErr = inject(ctx)
		torn = ctx.TornBytes
		buf = ctx.Data
		if len(buf) != d.size {
			return fmt.Errorf("%w: fault hook changed write length", ErrInvalidSize)
		}
	}

	off := int(sector) * d.size
	if torn > 0 || hookErr != nil {
		if torn < 0 {
			torn = 0
		}
		if torn > d.size {
			torn = d.size
		}
		copy(d.data[off:off+torn], buf[:torn])
		if hookErr != nil {
			return hookErr
		}
		return ErrInjected
	}
	copy(d.data[off:off+d.size], buf)
	return nil
}

func (d *MemoryDevice) Sync() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.syncCount++
	if inject := d.faultInjector(); inject != nil {
		ctx := &FaultContext{Operation: DeviceSync}
		if err := inject(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *MemoryDevice) faultInjector() FaultInjector {
	d.faultMu.RLock()
	defer d.faultMu.RUnlock()
	return d.fault
}

// Snapshot returns a detached copy of the entire device.
func (d *MemoryDevice) Snapshot() []byte {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]byte(nil), d.data...)
}

// RestoreSnapshot replaces the entire device contents.
func (d *MemoryDevice) RestoreSnapshot(snapshot []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(snapshot) != len(d.data) {
		return fmt.Errorf("%w: snapshot has %d bytes", ErrInvalidSize, len(snapshot))
	}
	copy(d.data, snapshot)
	return nil
}

// CopySector duplicates a raw physical sector, bypassing fault injection. It is
// intended for adversarial storage tests.
func (d *MemoryDevice) CopySector(dst, src uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if dst >= d.sectors || src >= d.sectors {
		return fmt.Errorf("%w: copy %d -> %d", ErrInvalidSector, src, dst)
	}
	srcOff := int(src) * d.size
	dstOff := int(dst) * d.size
	copy(d.data[dstOff:dstOff+d.size], d.data[srcOff:srcOff+d.size])
	return nil
}

// SwapSectors exchanges two raw physical sectors, bypassing fault injection.
func (d *MemoryDevice) SwapSectors(a, b uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a >= d.sectors || b >= d.sectors {
		return fmt.Errorf("%w: swap %d and %d", ErrInvalidSector, a, b)
	}
	aOff := int(a) * d.size
	bOff := int(b) * d.size
	tmp := append([]byte(nil), d.data[aOff:aOff+d.size]...)
	copy(d.data[aOff:aOff+d.size], d.data[bOff:bOff+d.size])
	copy(d.data[bOff:bOff+d.size], tmp)
	return nil
}

// Sector returns a detached copy of one raw physical sector.
func (d *MemoryDevice) Sector(sector uint64) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if sector >= d.sectors {
		return nil, fmt.Errorf("%w: physical sector %d", ErrInvalidSector, sector)
	}
	off := int(sector) * d.size
	return append([]byte(nil), d.data[off:off+d.size]...), nil
}

// SectorIsZero reports whether a physical sector is entirely zero.
func (d *MemoryDevice) SectorIsZero(sector uint64) (bool, error) {
	buf, err := d.Sector(sector)
	if err != nil {
		return false, err
	}
	return bytes.Equal(buf, make([]byte, d.size)), nil
}

// Counts returns cumulative read, write, and sync counts.
func (d *MemoryDevice) Counts() (reads, writes, syncs uint64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.readCount, d.writeCount, d.syncCount
}
