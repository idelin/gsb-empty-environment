package bootjournal

import (
	"bytes"
	"errors"
	"fmt"
)

// Physical NOR-flash geometry.
const (
	// WordSize is the minimum programming granularity. A word is programmed
	// atomically as part of a word-at-a-time power-fault sequence.
	WordSize = 4
	// PageSize is the erase granularity: an entire page is erased at once.
	PageSize = 256
)

// FaultyFlash models a NOR flash with realistic constraints:
//   - every bit starts set to 1;
//   - Program can only clear bits (1 -> 0); setting a bit (0 -> 1) is an error;
//   - Erase clears whole pages back to all ones;
//   - power loss can be injected after any individual word program or page
//     erase, leaving every earlier mutation durable and the later ones lost.
type FaultyFlash struct {
	pages int
	data  []byte
	// failAfter arms a one-shot fault: after the next mutating operation
	// (a word program or a page erase) completes, Fault trips.
	failAfter int
	ops       int
	faulted   bool
}

// ErrInvalidProgram is returned when a program would set an already cleared
// bit, which NOR flash cannot do without an intervening page erase.
var ErrInvalidProgram = errors.New("bootjournal: flash program would set a cleared bit")

// NewFlash creates an erased flash holding pages whole pages of PageSize.
func NewFlash(pages int) *FaultyFlash {
	if pages <= 0 {
		panic("bootjournal: flash must contain at least one page")
	}
	f := &FaultyFlash{pages: pages, data: make([]byte, pages*PageSize)}
	for i := range f.data {
		f.data[i] = 0xFF
	}
	return f
}

// Size returns the total flash capacity in bytes.
func (f *FaultyFlash) Size() int { return len(f.data) }

// Fault reports whether the flash has already suffered a power loss.
func (f *FaultyFlash) Fault() bool { return f.faulted }

// ArmFault schedules a power loss after the next mutating operation.
func (f *FaultyFlash) ArmFault() { f.failAfter = f.ops + 1 }

// Read copies len(p) bytes starting at offset.
func (f *FaultyFlash) Read(offset int, p []byte) error {
	if offset < 0 || offset+len(p) > len(f.data) {
		return errors.New("bootjournal: flash read out of range")
	}
	copy(p, f.data[offset:offset+len(p)])
	return nil
}

// Program writes p at offset, clearing bits only. Bytes within an already
// programmed word may be programmed again as long as only 1 -> 0 transitions
// occur. The power-fault boundary is the word: a fault is charged once per
// fully programmed four bytes (plus one for a sub-word tail), so a loss can
// only leave an aligned word prefix durable, never a torn word.
func (f *FaultyFlash) Program(offset int, p []byte) error {
	if offset < 0 || offset+len(p) > len(f.data) {
		return errors.New("bootjournal: flash program out of range")
	}
	// Validate the whole request first: a real controller refuses an illegal
	// program rather than partially applying it.
	for i, b := range p {
		if f.data[offset+i]&b != b {
			return fmt.Errorf("%w at offset %d byte %d: have %08b want %08b", ErrInvalidProgram, offset, i, f.data[offset+i], b)
		}
	}
	start := offset
	for i, b := range p {
		f.data[offset+i] &= b
		if (start+i+1)%WordSize == 0 && f.chargeFault() {
			return ErrPowerLoss
		}
	}
	if len(p)%WordSize != 0 && f.chargeFault() {
		return ErrPowerLoss
	}
	return nil
}

// chargeFault accounts one mutating operation and reports whether the armed
// one-shot power loss trips at this boundary.
func (f *FaultyFlash) chargeFault() bool {
	f.ops++
	if f.failAfter != 0 && f.ops >= f.failAfter {
		f.failAfter = 0
		f.faulted = true
		return true
	}
	return false
}

// ErasePage resets every byte of the page containing pageOffset to 0xFF.
func (f *FaultyFlash) ErasePage(pageOffset int) error {
	if pageOffset < 0 || pageOffset >= len(f.data) {
		return errors.New("bootjournal: flash erase out of range")
	}
	base := (pageOffset / PageSize) * PageSize
	f.ops++
	if f.failAfter != 0 && f.ops >= f.failAfter {
		f.failAfter = 0
		f.faulted = true
		return ErrPowerLoss
	}
	for i := base; i < base+PageSize; i++ {
		f.data[i] = 0xFF
	}
	return nil
}

// EraseRange erases every page intersecting [offset, offset+length).
func (f *FaultyFlash) EraseRange(offset, length int) error {
	startPage := offset / PageSize
	pageCount := (offset + length + PageSize - 1) / PageSize
	for page := startPage; page < pageCount; page++ {
		if err := f.ErasePage(page * PageSize); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot returns a detached copy of the flash contents, used to reopen a
// device after simulated power loss without sharing mutable state.
func (f *FaultyFlash) Snapshot() []byte {
	return bytes.Clone(f.data)
}

// Restore replaces flash contents with a previously captured snapshot while
// preserving (and disarming) the fault configuration.
func (f *FaultyFlash) Restore(data []byte) error {
	if len(data) != len(f.data) {
		return errors.New("bootjournal: restored flash has wrong size")
	}
	f.data = bytes.Clone(data)
	f.failAfter = 0
	f.faulted = false
	return nil
}
