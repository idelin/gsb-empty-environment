// Package chunkedtx assembles transactions that arrive split across
// multiple message chunks. A transaction commits only when every chunk
// in the contiguous sequence is staged and the declared total digest
// matches the assembled payload. Conflicting duplicates permanently
// fail a transaction, and the global staging budget is bounded.
package chunkedtx

import (
	"crypto/sha256"
	"time"
)

// Chunk is a single message block belonging to a transaction.
type Chunk struct {
	// TxID identifies the transaction this chunk belongs to.
	TxID string
	// Seq is the zero-based position of the chunk within the transaction.
	Seq uint32
	// Final marks the last chunk of the transaction. Exactly one chunk
	// per transaction may carry this flag.
	Final bool
	// Payload is the chunk's data fragment.
	Payload []byte
	// TotalDigest is the SHA-256 of the fully assembled transaction
	// payload. It must be set on the final chunk and must be zero on
	// all other chunks.
	TotalDigest [sha256.Size]byte
	// ExpiresAt optionally bounds how long the transaction may remain
	// staged. The transaction deadline is the earliest ExpiresAt seen
	// across its chunks. A zero value means no expiry.
	ExpiresAt time.Time
}

// Commit is the published result of a fully assembled transaction.
type Commit struct {
	TxID    string
	Payload []byte
	Chunks  int
}

// Status describes the outcome of ingesting a chunk.
type Status int

const (
	// StatusStaged means the chunk was accepted and the transaction is
	// still waiting for more chunks.
	StatusStaged Status = iota
	// StatusDuplicate means an identical chunk was already staged; the
	// ingest was a no-op.
	StatusDuplicate
	// StatusCommitted means the chunk completed the transaction and it
	// was published.
	StatusCommitted
)

// Outcome is the result of a single Ingest call.
type Outcome struct {
	Status Status
	TxID   string
	// Commit is non-nil only when Status is StatusCommitted.
	Commit *Commit
}
