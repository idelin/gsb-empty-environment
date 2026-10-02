package roaring

import (
	"errors"
	"math/rand"
	"sync"
)

// Faults surfaced by the fault-injection layer.
var (
	// ErrDiskFull simulates ENOSPC while persisting a bitmap.
	ErrDiskFull = errors.New("roaring: simulated disk full (ENOSPC)")
	// ErrPowerLoss simulates a sudden power cut mid-commit.
	ErrPowerLoss = errors.New("roaring: simulated sudden power loss")
	// ErrCorrupt is returned when data fails integrity checks and no
	// intact backup is available.
	ErrCorrupt = errors.New("roaring: data corrupted and unrecoverable")
	// ErrTimeout is returned by transports when no packet arrives in time.
	ErrTimeout = errors.New("roaring: transport timeout")
)

// FaultInjector controls deterministic chaos engineering hooks. All knobs
// are disabled by default (negative counters / zero rates).
//
//   - DiskFullAfter:       a Save writes at most N bytes, then fails with ErrDiskFull.
//   - CrashAfterBytes:     a Save writes at most N bytes, then "the machine dies"
//     (ErrPowerLoss); the partial temp file is left behind and never committed.
//   - CorruptMainOnCommit: after a successful commit, the main data file is
//     bit-flipped to emulate torn writes / bit-rot.
//   - PacketLossRate:      probability [0,1) of dropping a packet.
//   - PacketCorruptRate:   probability [0,1) of flipping a payload byte.
//   - DropFirstPackets:    deterministically drop the first N packets.
type FaultInjector struct {
	mu                  sync.Mutex
	DiskFullAfter       int
	CrashAfterBytes     int
	CorruptMainOnCommit bool
	PacketLossRate      float64
	PacketCorruptRate   float64
	DropFirstPackets    int
	Rand                *rand.Rand
}

// NewFaultInjector returns a disabled injector with a deterministic RNG.
func NewFaultInjector() *FaultInjector {
	return &FaultInjector{
		DiskFullAfter:   -1,
		CrashAfterBytes: -1,
		Rand:            rand.New(rand.NewSource(1)),
	}
}

// Reset disables every fault knob.
func (f *FaultInjector) Reset() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DiskFullAfter = -1
	f.CrashAfterBytes = -1
	f.CorruptMainOnCommit = false
	f.PacketLossRate = 0
	f.PacketCorruptRate = 0
	f.DropFirstPackets = 0
}

// writeBudget returns how many bytes may still be written and which fault,
// if any, triggers once the budget is exhausted.
func (f *FaultInjector) writeBudget(written int) (budget int, err error) {
	if f == nil {
		return -1, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	budget = -1
	if f.CrashAfterBytes >= 0 {
		if rem := f.CrashAfterBytes - written; budget < 0 || rem < budget {
			budget = rem
			err = ErrPowerLoss
		}
	}
	if f.DiskFullAfter >= 0 {
		if rem := f.DiskFullAfter - written; budget < 0 || rem < budget {
			budget = rem
			err = ErrDiskFull
		}
	}
	return budget, err
}

func (f *FaultInjector) shouldDrop() bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DropFirstPackets > 0 {
		f.DropFirstPackets--
		return true
	}
	return f.PacketLossRate > 0 && f.Rand.Float64() < f.PacketLossRate
}

func (f *FaultInjector) shouldCorrupt() bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.PacketCorruptRate > 0 && f.Rand.Float64() < f.PacketCorruptRate
}

func (f *FaultInjector) corruptMainOnCommit() bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.CorruptMainOnCommit
}
