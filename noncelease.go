// Package noncelease hands out disjoint counter segments (leases) to several
// encryption writers that share a symmetric key.
//
// # Correctness model
//
// Every successful allocation is first made durable in a write-ahead log and
// only afterwards returned to the caller. The durable state is therefore the
// monotonic high-water mark of each key version: once a segment has been
// handed out, the allocator can never hand out an overlapping segment again,
// even after a crash with a torn (partially persisted) write or a full loss of
// the last write.
//
// Leases are deliberately kept out of durable state. A lease is valid only for
// the allocator instance that issued it, so every restart invalidates all
// outstanding leases. Expired leases release their segment back into the pool
// after their TTL without ever allowing duplicate nonces: the high-water mark
// never moves backwards, only the bookkeeping of which tail segment is still
// reserved is forgotten.
//
// Key rotation starts a new (keyID, version) counter space that begins again at
// zero. Nonces are unique per key version, so counters may safely be reused
// after rotation; the old version is sealed and receives no new leases.
package noncelease

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// KeyID identifies a logical key; versions distinguish rotations of that key.
type KeyID string

// Sentinel errors returned by the allocator.
var (
	// ErrKeyNotFound is returned when an operation references an unknown key.
	ErrKeyNotFound = errors.New("noncelease: key not found")
	// ErrKeySealed is returned when allocating from a superseded key version.
	ErrKeySealed = errors.New("noncelease: key version is sealed")
	// ErrCounterExhausted is returned when not enough counters remain to
	// satisfy the full requested segment. Allocation is all-or-nothing.
	ErrCounterExhausted = errors.New("noncelease: counter space exhausted")
	// ErrLeaseInvalid is returned by Lease.Valid for unknown, expired or
	// restart-invalidated leases.
	ErrLeaseInvalid = errors.New("noncelease: invalid or expired lease")
	// ErrInvalidArgument is returned for zero lengths, empty key ids and
	// other malformed requests.
	ErrInvalidArgument = errors.New("noncelease: invalid argument")
)

// Interval is a half-open [Start, End) counter segment. Every counter in the
// segment belongs to exactly one lease of one key version.
type Interval struct {
	Start uint64
	End   uint64
	Key   KeyID
	// Version of the key the counters were allocated for.
	Version uint32
}

// Contains reports whether counter c is inside the half-open interval.
func (i Interval) Contains(c uint64) bool {
	return i.Start <= c && c < i.End
}

// Len returns the number of counters in the interval.
func (i Interval) Len() uint64 {
	return i.End - i.Start
}

// Lease is a counter segment granted to one writer. It must be presented to
// Valid before each use; leases do not survive an allocator restart and expire
// after their TTL.
type Lease struct {
	Interval Interval
	// Token is the unguessable fencing token identifying this lease.
	Token uint64
	// ExpiresAt is the TTL deadline in the allocator's clock; zero means the
	// lease never expires.
	ExpiresAt int64
}

// EncodeCounter turns a counter value into an 8-byte big-endian nonce. Use
// counters inside an allocated interval in any order; they remain unique
// within the key version because the intervals themselves are disjoint.
func EncodeCounter(counter uint64) [8]byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], counter)
	return n
}

// CounterFromNonce decodes a nonce produced by EncodeCounter.
func CounterFromNonce(nonce [8]byte) uint64 {
	return binary.BigEndian.Uint64(nonce[:])
}

// randomToken draws a nonzero 64-bit token from a CSPRNG so a stale writer
// cannot guess a reissued lease's token.
func randomToken() (uint64, error) {
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if t := binary.LittleEndian.Uint64(b[:]); t != 0 {
			return t, nil
		}
	}
}
