package engine

import "errors"

// Engine-level sentinel errors.
var (
	// ErrNoSpace means no zone in the relevant pool can accept writes right now;
	// the caller should run GC (or use WriteWithGC which does this automatically).
	ErrNoSpace = errors.New("zns engine: no writable or empty zone available")

	// ErrNothingToGC means no FULL zone exists that could be reclaimed.
	ErrNothingToGC = errors.New("zns engine: no FULL zone available for garbage collection")

	// ErrGCInvariant means a pre-reset safety check failed: the victim zone
	// still references valid logical blocks and must not be reset.
	ErrGCInvariant = errors.New("zns engine: GC invariant violated, victim still holds valid blocks")

	// ErrUnknownLBA means a read targeted a logical block that was never written.
	ErrUnknownLBA = errors.New("zns engine: logical LBA has no mapping")

	// ErrInvalidConfig means the engine configuration is self-contradictory.
	ErrInvalidConfig = errors.New("zns engine: invalid configuration")
)
