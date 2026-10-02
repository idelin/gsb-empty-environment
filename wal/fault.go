package wal

import (
	"errors"
	"io"
	"sync"
)

// Injected fault errors.
var (
	ErrDiskFull   = errors.New("wal: no space left on device (injected)")
	ErrPowerLoss  = errors.New("wal: sudden power loss (injected)")
	ErrTornWrite  = errors.New("wal: torn write (injected)")
	ErrPacketLoss = errors.New("wal: network packet lost (injected)")
)

// FaultKind selects which hostile condition an Injector emulates.
type FaultKind int

const (
	// FaultNone disables injection; writes pass through.
	FaultNone FaultKind = iota
	// FaultDiskFull makes every write fail with ErrDiskFull while armed.
	FaultDiskFull
	// FaultPowerLoss silently drops every write while armed, emulating
	// the loss of volatile OS page-cache contents on a power failure.
	FaultPowerLoss
	// FaultTornWrite applies only half of the next write and then fails
	// with ErrTornWrite (one-shot), emulating a crash mid-write.
	FaultTornWrite
)

// Injector is a concurrency-safe fault-injection switchboard. It wraps an
// io.Writer and perturbs writes according to the currently armed fault.
type Injector struct {
	mu   sync.Mutex
	kind FaultKind

	// Statistics, useful for assertions in tests.
	FailedWrites  int
	DroppedWrites int
	TornWrites    int
}

// NewInjector returns an Injector with no fault armed.
func NewInjector() *Injector { return &Injector{} }

// Arm enables the given fault. FaultTornWrite is one-shot and disarms
// itself after firing once.
func (inj *Injector) Arm(kind FaultKind) {
	inj.mu.Lock()
	inj.kind = kind
	inj.mu.Unlock()
}

// Clear disables any armed fault (models the operator fixing the disk,
// restoring power, etc.).
func (inj *Injector) Clear() { inj.Arm(FaultNone) }

// Armed reports the currently armed fault.
func (inj *Injector) Armed() FaultKind {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	return inj.kind
}

// Wrap returns a writer that applies the armed faults on every Write.
func (inj *Injector) Wrap(w io.Writer) io.Writer { return &faultWriter{w: w, inj: inj} }

type faultWriter struct {
	w   io.Writer
	inj *Injector
}

func (fw *faultWriter) Write(p []byte) (int, error) {
	fw.inj.mu.Lock()
	kind := fw.inj.kind
	switch kind {
	case FaultDiskFull:
		fw.inj.FailedWrites++
		fw.inj.mu.Unlock()
		return 0, ErrDiskFull
	case FaultPowerLoss:
		fw.inj.DroppedWrites++
		fw.inj.mu.Unlock()
		return len(p), nil // acknowledged but never reaches disk
	case FaultTornWrite:
		fw.inj.TornWrites++
		fw.inj.kind = FaultNone // one-shot
		fw.inj.mu.Unlock()
		n := len(p) / 2
		if n > 0 {
			if _, err := fw.w.Write(p[:n]); err != nil {
				return n, err
			}
		}
		return n, ErrTornWrite
	}
	fw.inj.mu.Unlock()
	return fw.w.Write(p)
}
