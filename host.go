package netsec

import "errors"

// Sentinel errors reported by the coordinator and host implementations.
var (
	ErrInvalidRegion      = errors.New("netsec: invalid patch region")
	ErrSizeMismatch       = errors.New("netsec: patch bytes do not match region size")
	ErrEmptyBoundaryMap   = errors.New("netsec: plan declares no migratable boundary")
	ErrBoundaryOutOfRange = errors.New("netsec: boundary mapping leaves the patch region")
	ErrFreezeTimeout      = errors.New("netsec: thread did not freeze in time")
	ErrUnmappablePC       = errors.New("netsec: thread paused inside patched sequence at an unmappable instruction")
	ErrRetriesExhausted   = errors.New("netsec: unmappable threads never left the patched region")
	ErrThreadNotFrozen    = errors.New("netsec: thread must be frozen before its state is modified")
	ErrUnknownThread      = errors.New("netsec: unknown thread reference")
	ErrThreadExited       = errors.New("netsec: thread has already exited")
	ErrBadAddress         = errors.New("netsec: address outside simulated memory")
	ErrFlushFailed        = errors.New("netsec: instruction cache flush failed")
)

// Thread generation. Reusing an operating-system thread ID starts a new
// generation, so references captured before reuse must never act on the new
// occupant of the ID.
type Gen uint64

// ThreadRef identifies one live (or formerly live) thread by ID and generation.
type ThreadRef struct {
	ID  uint64
	Gen Gen
}

// ThreadStatus is the state of a thread at a freeze point.
type ThreadStatus int

const (
	StatusUnknown ThreadStatus = iota
	StatusFrozen
	StatusRunning
	StatusExited
)

// Host is the machine-level surface the coordinator drives. A real
// implementation would wrap process_vm_readv, ptrace/suspend and cacheflush;
// tests substitute an in-memory simulation.
type Host interface {
	// ListThreads enumerates the threads currently alive in the process.
	ListThreads() []ThreadRef

	// Freeze requests that a thread stop. Freezing an already frozen
	// thread is a no-op; freezing an exited thread returns ErrThreadExited.
	Freeze(ThreadRef) error

	// Status reports the current state of a thread.
	Status(ThreadRef) (ThreadStatus, error)

	// ReadPC returns the paused program counter of a frozen thread.
	ReadPC(ThreadRef) (Addr, error)

	// WritePC redirects a frozen thread to another instruction address.
	WritePC(ThreadRef, Addr) error

	// Resume lets a frozen thread run again. Resuming a running thread
	// is a no-op; resuming an exited thread returns ErrThreadExited.
	Resume(ThreadRef) error

	// ReadMemory/WriteMemory touch the target code page.
	ReadMemory(addr Addr, n int) ([]byte, error)
	WriteMemory(addr Addr, data []byte) error

	// FlushICache invalidates instruction caches covering the interval
	// after the code page has been rewritten.
	FlushICache(Region) error
}
