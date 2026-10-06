// Package seq implements a persistent segment-based sequence number
// generator. It reduces flush frequency by reserving number segments
// with a single atomic compare-and-swap against a pluggable store.
//
// Guarantees:
//   - A returned sequence number is never reused, even across restarts.
//   - Restarting may skip numbers (gaps are allowed).
//   - If persisting a reservation fails, no number is issued.
package seq

import (
	"context"
	"errors"
)

// ErrCASConflict is returned when a compare-and-swap reservation loses
// a race against another process.
var ErrCASConflict = errors.New("seq: reservation conflict")

// ErrExhausted is returned when the sequence space is exhausted and no
// further segment can be reserved without overflowing uint64.
var ErrExhausted = errors.New("seq: sequence space exhausted")

// Store persists reservation high-water marks. Implementations must
// provide atomic compare-and-swap semantics so that multiple processes
// can coordinate through the same store.
type Store interface {
	// MaxReserved returns the persisted maximum reserved value for key.
	// It returns 0 when nothing has ever been reserved for key.
	MaxReserved(ctx context.Context, key string) (uint64, error)

	// CompareAndSwapMaxReserved atomically persists next as the new
	// maximum reserved value for key, but only if the currently
	// persisted value equals expected. It returns ErrCASConflict when
	// the persisted value no longer equals expected. On any other
	// error the caller cannot tell whether the write was applied and
	// must reload before issuing numbers.
	CompareAndSwapMaxReserved(ctx context.Context, key string, expected, next uint64) error
}
