// Package forkwatch implements a fork monitor for signed, hash-chained
// session messages.
//
// A replicated client snapshot can emit two distinct signed messages at the
// same session sequence number. Each message carries the digest of the
// previous message in its session (the parent digest), so two messages that
// share a parent but have different content digests are conclusive evidence
// of a fork. When the parent record has not been observed yet the record is
// merely marked pending instead of raising a conflict.
//
// Signature verification is delegated to an injected [Verifier], which keeps
// this package independent of any particular cryptographic stack. State can
// be exported per shard as a [Snapshot], exported, transported and merged
// back in any order; merges are idempotent and order-independent because
// every record is keyed by its content digest and every status is derived
// rather than stored.
package forkwatch

import (
	"encoding/hex"
	"errors"
)

// Digest is the content-addressed identity of a message. It is typically the
// hash of the canonical message bytes. A nil or zero-length digest is
// invalid.
type Digest []byte

// String renders the digest in lower-case hexadecimal.
func (d Digest) String() string {
	return hex.EncodeToString(d)
}

// Equal reports whether two digests contain the same bytes.
func (d Digest) Equal(other Digest) bool {
	if len(d) != len(other) {
		return false
	}
	for i := range d {
		if d[i] != other[i] {
			return false
		}
	}
	return true
}

// SignedMessage is a message observed from a client snapshot.
//
// Generation and Sequence identify the position in the session: a replicated
// client that forks emits different ContentDigest values with the same
// generation and sequence number. ParentDigest is the content digest of the
// preceding message in the same session; the first message (the root) uses a
// nil or zero-length parent digest.
type SignedMessage struct {
	// Generation is the session generation (epoch) of the message.
	Generation uint64

	// Sequence is the monotonically increasing sequence number within the
	// generation.
	Sequence uint64

	// ParentDigest references the previous message in the session. It is
	// empty for the root message.
	ParentDigest Digest

	// ContentDigest identifies the message content. Two messages with the
	// same content digest are the same message.
	ContentDigest Digest

	// Payload holds the signed message bytes that the Verifier checks. It is
	// not interpreted by this package.
	Payload []byte

	// Signature over Payload, handed to the Verifier together with Payload.
	Signature []byte
}

// Verifier validates message signatures. Production code injects a
// cryptographic implementation; tests can inject a stub. The payload and
// signature bytes are the values carried by [SignedMessage].
type Verifier interface {
	Verify(payload, signature []byte) error
}

// VerifierFunc adapts an ordinary function to the [Verifier] interface.
type VerifierFunc func(payload, signature []byte) error

// Verify calls the wrapped function.
func (f VerifierFunc) Verify(payload, signature []byte) error {
	return f(payload, signature)
}

// Status is the derived state of a record within the observed graph.
type Status int

const (
	// StatusUnknown is the zero value and is never returned for stored
	// records.
	StatusUnknown Status = iota

	// StatusLinked means the record either is a root or its parent has been
	// observed, and it is not part of a fork.
	StatusLinked

	// StatusPending means the parent record has not been observed yet. The
	// record stays pending until the parent arrives; a fork verdict takes
	// precedence over pending.
	StatusPending

	// StatusConflict means the record shares its parent with at least one
	// other record having a different content digest, which is conclusive
	// fork evidence.
	StatusConflict
)

// String returns a human-readable status name.
func (s Status) String() string {
	switch s {
	case StatusLinked:
		return "linked"
	case StatusPending:
		return "pending"
	case StatusConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// Record is the stored evidence for one distinct signed message.
type Record struct {
	Generation    uint64 `json:"generation"`
	Sequence      uint64 `json:"sequence"`
	ParentDigest  Digest `json:"parent_digest"`
	ContentDigest Digest `json:"content_digest"`
}

// Conflict is one piece of fork evidence: a parent digest with two or more
// distinct child content digests.
type Conflict struct {
	// Generation and Sequence identify the forked session position shared by
	// all children.
	Generation uint64 `json:"generation"`
	Sequence   uint64 `json:"sequence"`

	// ParentDigest is the shared predecessor; it is empty for conflicting
	// root messages.
	ParentDigest Digest `json:"parent_digest"`

	// ContentDigests are the distinct competing messages, sorted by digest
	// bytes for deterministic output.
	ContentDigests []Digest `json:"content_digests"`
}

// Snapshot is the serializable state of one shard. It contains only verified
// records; statuses are derived after loading and therefore need not be
// persisted. The byte-slice digests marshal as JSON base64 strings.
type Snapshot struct {
	// Shard identifies the originating shard. It is informational; merges
	// never reject a snapshot because of its shard identifier.
	Shard string `json:"shard"`

	// Records are the verified messages known to the shard, sorted by
	// generation, sequence and content digest.
	Records []Record `json:"records"`
}

// Observation errors. Callers should treat these as stable sentinels wrapped
// with errors.Is.
var (
	// ErrInvalidSignature is returned when the injected Verifier rejects a
	// message.
	ErrInvalidSignature = errors.New("forkwatch: invalid message signature")

	// ErrInvalidDigest is returned when a message has no content digest.
	ErrInvalidDigest = errors.New("forkwatch: missing content digest")
)
