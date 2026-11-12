// Package mmio mediates plugin access to a virtual device's MMIO register
// window. Access is constrained simultaneously by page grants and a
// register-level read-only mask. Every access is validated across its whole
// range before the device is touched exactly once, so a rejected access
// never causes partial writes and registers with read side effects are
// never read merely for checking.
package mmio

import (
	"errors"
	"fmt"
	"math"
)

const (
	// PageSize is the granularity of page grants.
	PageSize = 4096
	// RegSize is the granularity of the read-only mask.
	RegSize = 4
)

var (
	ErrBadSize     = errors.New("mmio: unsupported access size")
	ErrUnaligned   = errors.New("mmio: unaligned access")
	ErrWraparound  = errors.New("mmio: address range wraps around")
	ErrOutOfWindow = errors.New("mmio: access outside window")
	ErrPageDenied  = errors.New("mmio: page not granted")
	ErrReadOnly    = errors.New("mmio: register is read-only")
)

// Device is the virtual device backing the window. Offsets are relative to
// the window base. Reads may have side effects, so a Window performs
// exactly one Device call per accepted access and never reads to validate.
type Device interface {
	Read(offset uint64, size uint) uint64
	Write(offset uint64, size uint, value uint64)
}

// Window authorizes plugin accesses against page grants and a register
// read-only mask before forwarding a single access to the device.
type Window struct {
	base   uint64
	length uint64
	grants []uint64 // page grant bitmap
	ro     []uint64 // register read-only bitmap
}

// NewWindow creates a Window over [base, base+length). Both must be
// page-aligned; the window may extend to the top of the address space but
// must not wrap around.
func NewWindow(base, length uint64) (*Window, error) {
	if base%PageSize != 0 || length%PageSize != 0 || length == 0 {
		return nil, fmt.Errorf("mmio: base and length must be non-zero page-aligned, got base=%#x length=%#x", base, length)
	}
	if length-1 > math.MaxUint64-base {
		return nil, fmt.Errorf("mmio: window [%#x, %#x) wraps around", base, base+length)
	}
	return &Window{
		base:   base,
		length: length,
		grants: make([]uint64, (length/PageSize+63)/64),
		ro:     make([]uint64, (length/RegSize+63)/64),
	}, nil
}

func setBit(bitmap []uint64, idx uint64, on bool) {
	if on {
		bitmap[idx/64] |= 1 << (idx % 64)
	} else {
		bitmap[idx/64] &^= 1 << (idx % 64)
	}
}

func getBit(bitmap []uint64, idx uint64) bool {
	return bitmap[idx/64]&(1<<(idx%64)) != 0
}

// GrantPage allows accesses landing in the given window-relative page.
func (w *Window) GrantPage(page uint64) {
	w.checkPage(page)
	setBit(w.grants, page, true)
}

// RevokePage denies accesses landing in the given window-relative page.
func (w *Window) RevokePage(page uint64) {
	w.checkPage(page)
	setBit(w.grants, page, false)
}

// SetReadOnly marks the given window-relative register read-only (writes
// rejected) or writable.
func (w *Window) SetReadOnly(reg uint64, readOnly bool) {
	if reg >= w.length/RegSize {
		panic(fmt.Sprintf("mmio: register %d out of range", reg))
	}
	setBit(w.ro, reg, readOnly)
}

func (w *Window) checkPage(page uint64) {
	if page >= w.length/PageSize {
		panic(fmt.Sprintf("mmio: page %d out of range", page))
	}
}

// check validates the whole range [addr, addr+size) without touching the
// device and returns the window-relative offset on success.
func (w *Window) check(addr uint64, size uint, write bool) (uint64, error) {
	switch size {
	case 1, 2, 4, 8:
	default:
		return 0, fmt.Errorf("%w: %d bytes", ErrBadSize, size)
	}
	if addr > math.MaxUint64-uint64(size-1) {
		return 0, fmt.Errorf("%w: addr=%#x size=%d", ErrWraparound, addr, size)
	}
	if addr%uint64(size) != 0 {
		return 0, fmt.Errorf("%w: addr=%#x size=%d", ErrUnaligned, addr, size)
	}
	if addr < w.base {
		return 0, fmt.Errorf("%w: addr=%#x below base %#x", ErrOutOfWindow, addr, w.base)
	}
	offset := addr - w.base
	if offset > w.length || uint64(size) > w.length-offset {
		return 0, fmt.Errorf("%w: addr=%#x size=%d exceeds window", ErrOutOfWindow, addr, size)
	}
	firstPage := offset / PageSize
	lastPage := (offset + uint64(size) - 1) / PageSize
	for page := firstPage; page <= lastPage; page++ {
		if !getBit(w.grants, page) {
			return 0, fmt.Errorf("%w: page %d", ErrPageDenied, page)
		}
	}
	if write {
		firstReg := offset / RegSize
		lastReg := (offset + uint64(size) - 1) / RegSize
		for reg := firstReg; reg <= lastReg; reg++ {
			if getBit(w.ro, reg) {
				return 0, fmt.Errorf("%w: register %d", ErrReadOnly, reg)
			}
		}
	}
	return offset, nil
}

// Read validates the full range and then performs exactly one device read.
func (w *Window) Read(dev Device, addr uint64, size uint) (uint64, error) {
	offset, err := w.check(addr, size, false)
	if err != nil {
		return 0, err
	}
	return dev.Read(offset, size), nil
}

// Write validates the full range and then performs exactly one device
// write; a rejected write touches no register at all.
func (w *Window) Write(dev Device, addr uint64, size uint, value uint64) error {
	offset, err := w.check(addr, size, true)
	if err != nil {
		return err
	}
	dev.Write(offset, size, value)
	return nil
}
