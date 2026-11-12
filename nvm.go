package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// ErrPowerLoss is returned when a simulated power cut interrupts a non-volatile
// write. A caller must treat it as a hard stop: no PIN or PUK comparison may
// happen after the error.
var ErrPowerLoss = errors.New("netsec: simulated power loss")

const slotCount = 2

// SlotNVM is a small non-volatile memory with two independent slots. A write to
// one slot may be interrupted at any point without affecting the other slot.
// The implementation does not have to provide atomic state updates; the
// journal in state.go uses CRC and generation numbers to make commits
// crash-safe.
type SlotNVM interface {
	ReadSlot(slot int) (data []byte, present bool, err error)
	WriteSlot(slot int, data []byte) error
}

// MemoryNVM is an in-memory SlotNVM suitable for a reader simulator and tests.
// Each WriteSlot call is one injectable write point.
type MemoryNVM struct {
	mu     sync.Mutex
	slots  [slotCount][]byte
	writes int
}

// NewMemoryNVM returns an empty, unwritten non-volatile memory.
func NewMemoryNVM() *MemoryNVM {
	return &MemoryNVM{}
}

// ReadSlot implements SlotNVM.
func (m *MemoryNVM) ReadSlot(slot int) ([]byte, bool, error) {
	if slot < 0 || slot >= slotCount {
		return nil, false, fmt.Errorf("netsec: invalid NVM slot %d", slot)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.slots[slot]
	if len(data) == 0 {
		return nil, false, nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, true, nil
}

// WriteSlot implements SlotNVM. The whole slice is written as one write point.
func (m *MemoryNVM) WriteSlot(slot int, data []byte) error {
	if slot < 0 || slot >= slotCount {
		return fmt.Errorf("netsec: invalid NVM slot %d", slot)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes++
	stored := make([]byte, len(data))
	copy(stored, data)
	m.slots[slot] = stored
	return nil
}

// WriteCount reports how many successful writes have happened. It is useful in
// tests that enumerate every write point.
func (m *MemoryNVM) WriteCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}

// FaultMode selects where a power cut interrupts a write.
type FaultMode uint8

const (
	// FailBeforeWrite leaves the target slot untouched, modelling power loss
	// before the memory cell changes.
	FailBeforeWrite FaultMode = iota
	// FailAfterWrite commits the target slot and then reports power loss,
	// modelling a power cut immediately after the write completed.
	FailAfterWrite
	// FailTornWrite writes only the first half of the record and reports power
	// loss, modelling a torn page write. CRC validation rejects the fragment.
	FailTornWrite
)

// FaultNVM injects exactly one power cut at the n-th write point (1-based).
// Reads are passed straight through. After the fault, the wrapper disables
// itself so the test can model a reboot on the same memory image.
type FaultNVM struct {
	Base   SlotNVM
	FailAt int
	Mode   FaultMode

	mu     sync.Mutex
	writes int
}

// NewFaultNVM wraps base and fails the failAt-th write using mode. A failAt of
// zero disables injection.
func NewFaultNVM(base SlotNVM, failAt int, mode FaultMode) *FaultNVM {
	return &FaultNVM{Base: base, FailAt: failAt, Mode: mode}
}

// ReadSlot implements SlotNVM.
func (f *FaultNVM) ReadSlot(slot int) ([]byte, bool, error) {
	return f.Base.ReadSlot(slot)
}

// WriteSlot implements SlotNVM and injects the configured fault once.
func (f *FaultNVM) WriteSlot(slot int, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.writes++
	if f.FailAt > 0 && f.writes == f.FailAt {
		switch f.Mode {
		case FailBeforeWrite:
			return ErrPowerLoss
		case FailAfterWrite:
			if err := f.Base.WriteSlot(slot, data); err != nil {
				return err
			}
			return ErrPowerLoss
		case FailTornWrite:
			torn := data[:len(data)/2]
			if err := f.Base.WriteSlot(slot, torn); err != nil {
				return err
			}
			return ErrPowerLoss
		default:
			return fmt.Errorf("netsec: unknown fault mode %d", f.Mode)
		}
	}
	return f.Base.WriteSlot(slot, data)
}

// SetFailAt changes the injection point. Zero disables further injection.
func (f *FaultNVM) SetFailAt(failAt int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FailAt = failAt
}

// WriteCount reports the number of write attempts seen by the wrapper.
func (f *FaultNVM) WriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}
