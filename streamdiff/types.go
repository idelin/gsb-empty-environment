// Package streamdiff compares two sorted record streams that may be larger
// than memory and emits the differences (adds, removes, modifications) as a
// stream itself.
//
// Ordering contract: every Source yields records with unique keys in
// strictly ascending order. Values may be arbitrarily large and are exposed
// as chunk iterators so they never need to be materialized in memory.
//
// Trust model for value comparison: a value may advertise a strong
// (collision-resistant, SHA-256) digest of its full content. When both sides
// advertise one, equal digests are accepted as proof of equality and
// different digests as proof of inequality; this is sound under the standard
// assumption that finding SHA-256 collisions is computationally infeasible.
// Weak digests (CRC, FNV, ...) are never consulted for a final verdict. When
// a strong digest is missing on either side, values are compared byte by
// byte across chunk boundaries, which is always conclusive.
package streamdiff

import (
	"context"
	"crypto/sha256"
)

// Kind identifies the type of a Change.
type Kind int

const (
	// Added means the key exists only in the right (new) source.
	Added Kind = iota
	// Removed means the key exists only in the left (old) source.
	Removed
	// Modified means the key exists in both sources with differing values.
	Modified
)

func (k Kind) String() string {
	switch k {
	case Added:
		return "added"
	case Removed:
		return "removed"
	case Modified:
		return "modified"
	}
	return "unknown"
}

// Record is a single key/value entry. Keys are unique within a source.
type Record struct {
	Key   string
	Value Value
}

// Value is a possibly-large, chunked record payload.
type Value interface {
	// Open returns a fresh iterator over the value's chunks. It must be
	// safe to call Open multiple times and to abandon an iterator early.
	Open() ChunkIter
	// Strong returns the SHA-256 digest of the full value content if it is
	// known (precomputed or cheaply computable). ok is false when no strong
	// digest is available, in which case callers must fall back to a full
	// byte-wise comparison.
	Strong() (sum [sha256.Size]byte, ok bool)
}

// ChunkIter yields successive chunks of a value. It returns io.EOF when the
// value is exhausted. A chunk must be non-empty; the returned slice is only
// valid until the next call to Next.
type ChunkIter interface {
	Next() ([]byte, error)
}

// Source is a stream of sorted records. Next returns io.EOF at the end of
// the stream. Any other error is transient or fatal; the caller may resume
// later from the last returned Cursor.
type Source interface {
	Next(ctx context.Context) (Record, error)
	// Version identifies the exact snapshot this source reads from. A
	// Cursor is only valid for the pair of versions it was created with.
	Version() string
}

// Change is a single emitted difference.
type Change struct {
	Kind Kind
	Key  string
	// Old is the left value (set for Removed and Modified).
	Old Value
	// New is the right value (set for Added and Modified).
	New Value
}

// Cursor marks a resumable position in a diff run. It is bound to both
// source versions: resuming with sources of different versions is an error.
// LastKey is the greatest key for which a decision has been durably made
// (emitted, or proven equal); resuming sources must yield keys > LastKey,
// which guarantees no change is emitted twice.
type Cursor struct {
	LeftVersion  string
	RightVersion string
	LastKey      string
}

// Sink receives changes. Emit may block to apply backpressure; the diff
// makes no progress until Emit returns. Returning an error aborts the diff.
// A change is considered delivered (and the cursor advanced past it) only
// when Emit returns nil.
type Sink interface {
	Emit(ctx context.Context, c Change) error
}
