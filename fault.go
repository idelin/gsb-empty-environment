package aries

import (
	"errors"
	"sync"
)

// ErrPowerLoss simulates a sudden power failure: the machine dies instantly
// and stays dead until Reboot is called.
var ErrPowerLoss = errors.New("aries: simulated power loss")

// ErrDiskFull simulates ENOSPC: the write fails but the machine stays alive.
var ErrDiskFull = errors.New("aries: simulated disk full (ENOSPC)")

// FaultInjector is a deterministic, concurrency-safe chaos hook consulted by
// the WAL and the page store before every durable write.
//
//   - LogWritesLeft / PageWritesLeft: countdown of successful writes; when it
//     reaches zero a power loss is injected (log writes become torn).
//     -1 disables the countdown.
//   - DiskFull: every write fails with ErrDiskFull (non-fatal, retryable).
//   - Dead: set after a power loss; all writes fail until Reboot.
type FaultInjector struct {
	mu             sync.Mutex
	LogWritesLeft  int
	PageWritesLeft int
	DiskFull       bool
	Dead           bool

	// Counters for assertions in tests.
	PowerLossCount int
	DiskFullCount  int
	TornWrites     int
}

// NewFaultInjector returns an injector with all faults disabled.
func NewFaultInjector() *FaultInjector {
	return &FaultInjector{LogWritesLeft: -1, PageWritesLeft: -1}
}

// beforeLogWrite decides the fate of a log write of size bytes. It returns
// the number of payload bytes the WAL is allowed to persist (a short count
// models a torn write) plus the error to report to the caller.
func (inj *FaultInjector) beforeLogWrite(size int) (int, error) {
	if inj == nil {
		return size, nil
	}
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if inj.Dead {
		return 0, ErrPowerLoss
	}
	if inj.DiskFull {
		inj.DiskFullCount++
		return 0, ErrDiskFull
	}
	if inj.LogWritesLeft == 0 {
		inj.Dead = true
		inj.PowerLossCount++
		inj.TornWrites++
		return size / 2, ErrPowerLoss // torn tail: only half the record lands
	}
	if inj.LogWritesLeft > 0 {
		inj.LogWritesLeft--
	}
	return size, nil
}

// beforePageWrite decides the fate of a page write. Pages are assumed to be
// written atomically (sector atomicity), so a power loss here means the page
// simply never reaches disk.
func (inj *FaultInjector) beforePageWrite() error {
	if inj == nil {
		return nil
	}
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if inj.Dead {
		return ErrPowerLoss
	}
	if inj.DiskFull {
		inj.DiskFullCount++
		return ErrDiskFull
	}
	if inj.PageWritesLeft == 0 {
		inj.Dead = true
		inj.PowerLossCount++
		return ErrPowerLoss
	}
	if inj.PageWritesLeft > 0 {
		inj.PageWritesLeft--
	}
	return nil
}

// Reboot clears the dead state and disables the countdowns, modelling a
// machine restart after a crash.
func (inj *FaultInjector) Reboot() {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	inj.Dead = false
	inj.DiskFull = false
	inj.LogWritesLeft = -1
	inj.PageWritesLeft = -1
}
