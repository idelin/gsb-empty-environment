package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrFrozenPage is returned by SimDevice when a non-commit writer touches a
// frozen page.
var ErrFrozenPage = errors.New("netsec: page is frozen")

// ErrNoDevice is returned for accesses outside the simulated address range.
var ErrNoDevice = errors.New("netsec: address outside simulated device")

// SimDevice is an in-memory DeviceMemory used for tests and as a reference
// implementation. Pages frozen through Freeze reject ExternalWrite, while
// Write (the agent's commit channel) is only accepted on frozen pages.
type SimDevice struct {
	mu       sync.Mutex
	size     uint64
	mem      []byte
	frozen   map[uint64]struct{}
	failAddr map[uint64]error
}

// NewSimDevice creates a simulated device whose bytes all start at zero.
func NewSimDevice(size uint64) *SimDevice {
	return &SimDevice{
		size:     size,
		mem:      make([]byte, size),
		frozen:   make(map[uint64]struct{}),
		failAddr: make(map[uint64]error),
	}
}

// FailWriteFrom makes commit-channel writes at addresses >= addr fail with
// cause, used to exercise commit rollback.
func (d *SimDevice) FailWriteFrom(addr uint64, cause error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failAddr[addr] = cause
}

// ExternalWrite models a plugin or bus-initiated write; frozen pages reject
// it, which is the guarantee Commit relies on between freeze and commit.
func (d *SimDevice) ExternalWrite(addr uint64, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	end, ok := checkedAdd(addr, uint64(len(data)))
	if !ok || end > d.size {
		return fmt.Errorf("%w: %#x", ErrNoDevice, addr)
	}
	for i := range data {
		if _, frozen := d.frozen[((addr+uint64(i))/PageSize)*PageSize]; frozen {
			return fmt.Errorf("%w: %#x", ErrFrozenPage, addr)
		}
	}
	copy(d.mem[addr:end], data)
	return nil
}

// Read implements DeviceMemory.
func (d *SimDevice) Read(addr uint64, buf []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	end, ok := checkedAdd(addr, uint64(len(buf)))
	if !ok || end > d.size {
		return fmt.Errorf("%w: %#x", ErrNoDevice, addr)
	}
	copy(buf, d.mem[addr:end])
	return nil
}

// Write is the commit channel and requires every target byte to be frozen.
func (d *SimDevice) Write(addr uint64, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	end, ok := checkedAdd(addr, uint64(len(data)))
	if !ok || end > d.size {
		return fmt.Errorf("%w: %#x", ErrNoDevice, addr)
	}
	for i := range data {
		if _, frozen := d.frozen[((addr+uint64(i))/PageSize)*PageSize]; !frozen {
			return fmt.Errorf("netsec: commit write to unfrozen page at %#x", addr)
		}
	}
	if cause, fail := d.failAddr[addr]; fail {
		return cause
	}
	copy(d.mem[addr:end], data)
	return nil
}

// Freeze implements DeviceMemory and is idempotent per page.
func (d *SimDevice) Freeze(pageAddr uint64) error {
	if pageAddr%PageSize != 0 {
		return fmt.Errorf("netsec: freeze of unaligned page %#x", pageAddr)
	}
	if pageAddr >= d.size {
		return fmt.Errorf("%w: page %#x", ErrNoDevice, pageAddr)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frozen[pageAddr] = struct{}{}
	return nil
}

// UnfreezeAll implements DeviceMemory.
func (d *SimDevice) UnfreezeAll() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frozen = make(map[uint64]struct{})
	return nil
}

// FrozenPages returns the currently frozen page base addresses, sorted.
func (d *SimDevice) FrozenPages() []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	pages := make([]uint64, 0, len(d.frozen))
	for pg := range d.frozen {
		pages = append(pages, pg)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	return pages
}

// Load copies data into the simulated device without freeze enforcement.
func (d *SimDevice) Load(addr uint64, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	end, ok := checkedAdd(addr, uint64(len(data)))
	if !ok || end > d.size {
		return fmt.Errorf("%w: %#x", ErrNoDevice, addr)
	}
	copy(d.mem[addr:end], data)
	return nil
}
