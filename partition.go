package replayhandoff

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// NodeID identifies a receiver/replica.
type NodeID string

// ContextID identifies an OSCORE security context.
type ContextID string

// SenderID identifies the sending endpoint inside a security context.
type SenderID string

// Partition is the replay-state key. Sequence numbers are never compared or
// shared across different Partitions.
type Partition struct {
	Context ContextID
	Sender  SenderID
}

// NewPartition constructs a partition key.
func NewPartition(context, sender string) Partition {
	return Partition{Context: ContextID(context), Sender: SenderID(sender)}
}

// Validate checks that both identity components are present.
func (p Partition) Validate() error {
	if p.Context == "" {
		return errors.New("replayhandoff: security context must not be empty")
	}
	if p.Sender == "" {
		return errors.New("replayhandoff: sender must not be empty")
	}
	return nil
}

// String returns a stable, unambiguous textual partition key.
func (p Partition) String() string {
	return fmt.Sprintf("%q/%q", p.Context, p.Sender)
}

// ContextDigestFor binds the security-context identifier into batch headers
// and range proofs.
func ContextDigestFor(ctx ContextID) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("replayhandoff/context/v1\x00"))
	_, _ = h.Write([]byte(ctx))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Digest returns the authenticated security-context digest for p.
func (p Partition) Digest() [32]byte {
	return ContextDigestFor(p.Context)
}
