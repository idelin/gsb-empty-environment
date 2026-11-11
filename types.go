package netsec

import (
	"context"
	"errors"
)

// KeyGen identifies a generation of a volume key. Generation zero is the
// initial key and each rotation produces a strictly larger generation.
type KeyGen uint64

// Digest binds a block version to its plaintext content. It is the SHA-256
// digest of the plaintext carried by that version.
type Digest [32]byte

// BlockVersion is the immutable version stamp carried by every on-disk block.
// It binds the block to both the key generation that sealed it and the digest
// of the plaintext it contains. Seq is a per-block monotonic counter that
// distinguishes successive versions which happen to share the same key and
// digest (for example a rewrite of identical content after a migration).
type BlockVersion struct {
	Gen KeyGen `json:"gen"`
	Seq uint64 `json:"seq"`
	Sum Digest `json:"sum"`
}

// Record is one persisted block.
type Record struct {
	BlockID    string       `json:"block_id"`
	Version    BlockVersion `json:"version"`
	Ciphertext []byte       `json:"ciphertext"`
}

// ErrConflict is returned by a compare-and-swap commit whose expected version
// no longer matches the current on-disk version. The caller must re-read the
// block and retry; a migration must never overwrite the newer version.
var ErrConflict = errors.New("netsec: block version conflict")

// ErrNotFound is returned when no block exists for the requested id.
var ErrNotFound = errors.New("netsec: block not found")

// ErrKeyDestroyed is returned when a key generation is touched after its key
// material has been zeroized.
var ErrKeyDestroyed = errors.New("netsec: key generation destroyed")

// ErrUnknownKey is returned when no key material exists for a generation that
// has never been provisioned on this host.
var ErrUnknownKey = errors.New("netsec: unknown key generation")

// Store is the durable, per-block transactional storage backend.
//
// CompareAndSwap atomically replaces the record for id when its current version
// equals expected:
//   - On success it stores next (Version and Ciphertext) and returns nil.
//   - On mismatch it returns ErrConflict together with the record currently
//     stored so callers can re-read without an extra round trip.
//
// A missing block is represented by the zero BlockVersion: the first write of
// an id must therefore pass the zero BlockVersion as expected.
type Store interface {
	Get(ctx context.Context, id string) (Record, error)
	List(ctx context.Context) ([]Record, error)
	CompareAndSwap(ctx context.Context, id string, expected BlockVersion, next Record) (Record, error)
}
