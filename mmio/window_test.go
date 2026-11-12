package mmio

import (
	"errors"
	"math"
	"testing"
)

// countingDevice is a countable device model: it records every register
// read and write so tests can prove that rejected accesses never reach the
// device and accepted accesses reach it exactly once. Register 0 has a
// read side effect (read-to-clear), like an interrupt-cause register.
type countingDevice struct {
	mem       []byte
	reads     []uint64 // per-register read count
	writes    []uint64 // per-register write count
	clearRegs map[uint64]bool
}

func newCountingDevice(length uint64) *countingDevice {
	return &countingDevice{
		mem:       make([]byte, length),
		reads:     make([]uint64, length/RegSize),
		writes:    make([]uint64, length/RegSize),
		clearRegs: map[uint64]bool{0: true},
	}
}

func (d *countingDevice) totalReads() (n uint64) {
	for _, c := range d.reads {
		n += c
	}
	return n
}

func (d *countingDevice) totalWrites() (n uint64) {
	for _, c := range d.writes {
		n += c
	}
	return n
}

func (d *countingDevice) Read(offset uint64, size uint) uint64 {
	var v uint64
	for i := uint(0); i < size; i++ {
		v |= uint64(d.mem[offset+uint64(i)]) << (8 * i)
	}
	for reg := offset / RegSize; reg <= (offset+uint64(size)-1)/RegSize; reg++ {
		d.reads[reg]++
		if d.clearRegs[reg] {
			for i := uint64(0); i < RegSize; i++ {
				d.mem[reg*RegSize+i] = 0
			}
		}
	}
	return v
}

func (d *countingDevice) Write(offset uint64, size uint, value uint64) {
	for i := uint(0); i < size; i++ {
		d.mem[offset+uint64(i)] = byte(value >> (8 * i))
	}
	for reg := offset / RegSize; reg <= (offset+uint64(size)-1)/RegSize; reg++ {
		d.writes[reg]++
	}
}

func (d *countingDevice) reg(reg uint64) uint32 {
	v := uint64(0)
	for i := uint64(0); i < RegSize; i++ {
		v |= uint64(d.mem[reg*RegSize+i]) << (8 * i)
	}
	return uint32(v)
}

const testBase = 0xF000_0000

func newGrantedWindow(t *testing.T, pages uint64) (*Window, *countingDevice) {
	t.Helper()
	w, err := NewWindow(testBase, pages*PageSize)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	for p := uint64(0); p < pages; p++ {
		w.GrantPage(p)
	}
	return w, newCountingDevice(pages*PageSize)
}

func TestUnalignedAccessRejected(t *testing.T) {
	w, dev := newGrantedWindow(t, 1)
	for _, addr := range []uint64{testBase + 1, testBase + 2, testBase + 3} {
		if _, err := w.Read(dev, addr, 4); !errors.Is(err, ErrUnaligned) {
			t.Fatalf("Read(%#x): got %v, want ErrUnaligned", addr, err)
		}
		if err := w.Write(dev, addr, 4, 0xdeadbeef); !errors.Is(err, ErrUnaligned) {
			t.Fatalf("Write(%#x): got %v, want ErrUnaligned", addr, err)
		}
	}
	if _, err := w.Read(dev, testBase, 3); !errors.Is(err, ErrBadSize) {
		t.Fatalf("Read size 3: got %v, want ErrBadSize", err)
	}
	if got := dev.totalReads() + dev.totalWrites(); got != 0 {
		t.Fatalf("device touched %d times by rejected accesses", got)
	}
}

func TestAddressWraparoundRejected(t *testing.T) {
	w, dev := newGrantedWindow(t, 1)
	if _, err := w.Read(dev, math.MaxUint64-1, 4); !errors.Is(err, ErrWraparound) {
		t.Fatalf("Read near top: got %v, want ErrWraparound", err)
	}
	if err := w.Write(dev, math.MaxUint64-1, 4, 1); !errors.Is(err, ErrWraparound) {
		t.Fatalf("Write near top: got %v, want ErrWraparound", err)
	}

	// Window ending exactly at the top of the address space: an 8-byte
	// access starting at the last 4 bytes wraps past 2^64.
	top, err := NewWindow(math.MaxUint64-PageSize+1, PageSize)
	if err != nil {
		t.Fatalf("NewWindow at top: %v", err)
	}
	top.GrantPage(0)
	if _, err := top.Read(dev, math.MaxUint64-3, 8); !errors.Is(err, ErrWraparound) {
		t.Fatalf("Read wrapping past 2^64: got %v, want ErrWraparound", err)
	}
	if got := dev.totalReads() + dev.totalWrites(); got != 0 {
		t.Fatalf("device touched %d times by wrapping accesses", got)
	}
}

func TestLastRegisterReadOnlyNoPartialWrite(t *testing.T) {
	w, dev := newGrantedWindow(t, 1)
	w.SetReadOnly(1, true) // second register read-only

	// 8-byte write spans registers 0 (writable) and 1 (read-only): the
	// whole access must be rejected before register 0 is written.
	err := w.Write(dev, testBase, 8, 0x1122334455667788)
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Write spanning read-only register: got %v, want ErrReadOnly", err)
	}
	if got := dev.totalWrites(); got != 0 {
		t.Fatalf("partial write happened: %d device writes", got)
	}
	if got := dev.reg(0); got != 0 {
		t.Fatalf("register 0 modified by rejected write: %#x", got)
	}

	// Clearing the mask lets the same cross-register write through once.
	w.SetReadOnly(1, false)
	if err := w.Write(dev, testBase, 8, 0x1122334455667788); err != nil {
		t.Fatalf("Write after clearing mask: %v", err)
	}
	if dev.writes[0] != 1 || dev.writes[1] != 1 || dev.totalWrites() != 2 {
		t.Fatalf("accepted write should touch each register once, got %v", dev.writes[:2])
	}
	if dev.reg(0) != 0x55667788 || dev.reg(1) != 0x11223344 {
		t.Fatalf("wrong register values: %#x %#x", dev.reg(0), dev.reg(1))
	}
}

func TestUngrantedPageRejected(t *testing.T) {
	w, err := NewWindow(testBase, 2*PageSize)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	w.GrantPage(0)
	dev := newCountingDevice(2 * PageSize)

	if _, err := w.Read(dev, testBase+PageSize, 4); !errors.Is(err, ErrPageDenied) {
		t.Fatalf("Read on ungranted page: got %v, want ErrPageDenied", err)
	}
	if err := w.Write(dev, testBase+PageSize, 4, 1); !errors.Is(err, ErrPageDenied) {
		t.Fatalf("Write on ungranted page: got %v, want ErrPageDenied", err)
	}
	if _, err := w.Read(dev, testBase+2*PageSize, 4); !errors.Is(err, ErrOutOfWindow) {
		t.Fatalf("Read past window: got %v, want ErrOutOfWindow", err)
	}
	if got := dev.totalReads() + dev.totalWrites(); got != 0 {
		t.Fatalf("device touched %d times by denied accesses", got)
	}
}

func TestReadSideEffectRegisterReadExactlyOnce(t *testing.T) {
	w, dev := newGrantedWindow(t, 1)
	// Register 0 clears on read. Seed it directly, bypassing the window.
	dev.Write(0, 4, 0xAB)
	dev.writes[0] = 0

	v, err := w.Read(dev, testBase, 4)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != 0xAB {
		t.Fatalf("got %#x, want 0xAB; a check-read would have cleared it first", v)
	}
	if dev.reads[0] != 1 || dev.totalReads() != 1 {
		t.Fatalf("side-effect register read %d times, want exactly 1", dev.reads[0])
	}
	if got := dev.reg(0); got != 0 {
		t.Fatalf("read-to-clear register not cleared: %#x", got)
	}
}

func TestReadOnlyMaskDoesNotBlockReads(t *testing.T) {
	w, dev := newGrantedWindow(t, 1)
	w.SetReadOnly(2, true)
	if _, err := w.Read(dev, testBase+8, 4); err != nil {
		t.Fatalf("Read on read-only register should be allowed: %v", err)
	}
	if dev.reads[2] != 1 {
		t.Fatalf("register 2 read %d times, want 1", dev.reads[2])
	}
}
