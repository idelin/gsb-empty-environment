// Package chunkedtx stages multi-chunk transactions and commits each one
// atomically, only after every chunk has arrived contiguously and the
// digest of the fully assembled payload matches the expected total digest.
//
// A transaction is described by a sequence of Chunk values sharing a TxID.
// Chunks may arrive in any order. The chunk marked Final carries the
// expected digest of the whole assembled payload. A transaction commits at
// most once; a duplicate chunk whose content conflicts with the previously
// staged copy fails the transaction permanently.
//
// Staged payloads are accounted against a fixed global byte budget shared
// by all in-flight transactions. Transactions that do not complete within
// their TTL are expired: their staged bytes are released and late chunks
// for them are rejected, so nothing is ever published early or late.
package chunkedtx

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by Add. Go 1.11 has no errors.Is, so callers
// should compare with ==.
var (
	// ErrInvalidChunk indicates a malformed chunk (empty TxID, or a Final
	// chunk without a total digest).
	ErrInvalidChunk = errors.New("chunkedtx: invalid chunk")
	// ErrTxFailed indicates the transaction has permanently failed, either
	// by this chunk (conflict) or earlier. Failed transactions never commit.
	ErrTxFailed = errors.New("chunkedtx: transaction permanently failed")
	// ErrDigestMismatch indicates the transaction became complete but the
	// assembled payload's digest does not match the expected total digest.
	// The transaction is failed permanently.
	ErrDigestMismatch = errors.New("chunkedtx: total digest mismatch")
	// ErrExpired indicates the transaction expired before completing, or
	// the chunk belongs to a previously expired transaction.
	ErrExpired = errors.New("chunkedtx: transaction expired")
	// ErrBudgetExceeded indicates the chunk does not fit in the remaining
	// global staging budget. It is not staged and may be retried later.
	ErrBudgetExceeded = errors.New("chunkedtx: staging budget exceeded")
	// ErrAlreadyCommitted indicates the chunk belongs to a transaction
	// that has already committed. It is ignored to guarantee at-most-once
	// publication.
	ErrAlreadyCommitted = errors.New("chunkedtx: transaction already committed")
)

// Chunk is one message block of a transaction.
type Chunk struct {
	TxID    string            // transaction identifier
	Seq     uint32            // zero-based sequence number within the transaction
	Final   bool              // true on the last chunk of the transaction
	Digest  [sha256.Size]byte // expected digest of the assembled payload; required on Final
	Payload []byte            // chunk payload bytes
}

// DigestOf returns the digest that a Final chunk must carry for payload.
func DigestOf(payload []byte) [sha256.Size]byte {
	return sha256.Sum256(payload)
}

// Option configures a Stager.
type Option func(*Stager)

// WithClock overrides the clock, primarily for testing expiry.
func WithClock(now func() time.Time) Option {
	return func(s *Stager) { s.now = now }
}

// WithTombstoneTTL sets how long expired/committed transaction IDs are
// remembered so that late or redelivered chunks are rejected instead of
// starting a fresh transaction. Defaults to the transaction TTL.
func WithTombstoneTTL(d time.Duration) Option {
	return func(s *Stager) { s.tombstoneTTL = d }
}

// Stager stages chunks for many concurrent transactions under a shared
// byte budget. It is safe for concurrent use.
type Stager struct {
	mu           sync.Mutex
	budget       int64
	used         int64
	ttl          time.Duration
	tombstoneTTL time.Duration
	now          func() time.Time

	txs       map[string]*txState
	failed    map[string]struct{}
	committed map[string]time.Time // txID -> tombstone expiry
	expired   map[string]time.Time // txID -> tombstone expiry
}

type txState struct {
	chunks    map[uint32]Chunk
	bytes     int64
	finalSeq  int64 // -1 until a Final chunk is staged
	digest    [sha256.Size]byte
	hasDigest bool
	deadline  time.Time
}

// New returns a Stager with a global staging budget of budget bytes and a
// per-transaction TTL measured from its first staged chunk.
func New(budget int64, ttl time.Duration, opts ...Option) *Stager {
	s := &Stager{
		budget:    budget,
		ttl:       ttl,
		now:       time.Now,
		txs:       make(map[string]*txState),
		failed:    make(map[string]struct{}),
		committed: make(map[string]time.Time),
		expired:   make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.tombstoneTTL == 0 {
		s.tombstoneTTL = ttl
	}
	return s
}

// Add ingests one chunk. When the chunk completes its transaction and the
// assembled payload matches the expected total digest, Add commits the
// transaction and returns the assembled payload with committed == true.
// In every other case it returns committed == false; nothing is published
// before the transaction is contiguous, complete and digest-verified.
func (s *Stager) Add(c Chunk) (payload []byte, committed bool, err error) {
	var zero [sha256.Size]byte
	if c.TxID == "" || (c.Final && c.Digest == zero) {
		return nil, false, ErrInvalidChunk
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	if _, ok := s.failed[c.TxID]; ok {
		return nil, false, ErrTxFailed
	}
	if exp, ok := s.committed[c.TxID]; ok {
		if now.Before(exp) {
			return nil, false, ErrAlreadyCommitted
		}
		delete(s.committed, c.TxID)
	}
	if exp, ok := s.expired[c.TxID]; ok {
		if now.Before(exp) {
			return nil, false, ErrExpired
		}
		delete(s.expired, c.TxID)
	}

	tx, ok := s.txs[c.TxID]
	if ok && !now.Before(tx.deadline) {
		s.expireLocked(c.TxID, tx, now)
		return nil, false, ErrExpired
	}
	if !ok {
		tx = &txState{
			chunks:   make(map[uint32]Chunk),
			finalSeq: -1,
			deadline: now.Add(s.ttl),
		}
		s.txs[c.TxID] = tx
	}

	// Duplicate sequence number: identical content is an idempotent
	// redelivery; any difference is a conflict and fails the transaction
	// permanently.
	if prev, dup := tx.chunks[c.Seq]; dup {
		if prev.Final == c.Final && prev.Digest == c.Digest &&
			bytes.Equal(prev.Payload, c.Payload) {
			return nil, false, nil
		}
		s.failLocked(c.TxID, tx)
		return nil, false, ErrTxFailed
	}

	// Finality and digest must be consistent across the transaction.
	if c.Final {
		if tx.finalSeq >= 0 && tx.finalSeq != int64(c.Seq) {
			s.failLocked(c.TxID, tx)
			return nil, false, ErrTxFailed
		}
	} else if tx.finalSeq >= 0 && int64(c.Seq) > tx.finalSeq {
		s.failLocked(c.TxID, tx)
		return nil, false, ErrTxFailed
	}
	if c.Digest != zero && tx.hasDigest && tx.digest != c.Digest {
		s.failLocked(c.TxID, tx)
		return nil, false, ErrTxFailed
	}

	size := int64(len(c.Payload))
	if s.used+size > s.budget {
		return nil, false, ErrBudgetExceeded
	}

	staged := c
	staged.Payload = append([]byte(nil), c.Payload...)
	tx.chunks[c.Seq] = staged
	tx.bytes += size
	s.used += size
	if c.Final {
		tx.finalSeq = int64(c.Seq)
		tx.digest = c.Digest
		tx.hasDigest = true
	} else if c.Digest != zero && !tx.hasDigest {
		tx.digest = c.Digest
		tx.hasDigest = true
	}

	// Commit only when the Final chunk is known and chunks 0..finalSeq are
	// all staged. Unique sequence keys plus the finality checks above make
	// the count test equivalent to full contiguity.
	if tx.finalSeq >= 0 && int64(len(tx.chunks)) == tx.finalSeq+1 {
		assembled := assemble(tx)
		if DigestOf(assembled) != tx.digest {
			s.failLocked(c.TxID, tx)
			return nil, false, ErrDigestMismatch
		}
		s.commitLocked(c.TxID, tx, now)
		return assembled, true, nil
	}
	return nil, false, nil
}

// Sweep expires all transactions whose deadline has passed and drops
// tombstones older than the tombstone TTL, releasing their budget.
func (s *Stager) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, tx := range s.txs {
		if !now.Before(tx.deadline) {
			s.expireLocked(id, tx, now)
		}
	}
	for id, exp := range s.committed {
		if !now.Before(exp) {
			delete(s.committed, id)
		}
	}
	for id, exp := range s.expired {
		if !now.Before(exp) {
			delete(s.expired, id)
		}
	}
}

// Stats reports the currently staged byte count and the number of
// in-flight transactions.
func (s *Stager) Stats() (usedBytes int64, pending int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used, len(s.txs)
}

func assemble(tx *txState) []byte {
	var buf bytes.Buffer
	for seq := int64(0); seq <= tx.finalSeq; seq++ {
		buf.Write(tx.chunks[uint32(seq)].Payload)
	}
	return buf.Bytes()
}

// failLocked permanently fails a transaction and releases its budget.
func (s *Stager) failLocked(id string, tx *txState) {
	s.used -= tx.bytes
	delete(s.txs, id)
	s.failed[id] = struct{}{}
}

// commitLocked removes a committed transaction and records a tombstone so
// redelivered chunks cannot trigger a second publication.
func (s *Stager) commitLocked(id string, tx *txState, now time.Time) {
	s.used -= tx.bytes
	delete(s.txs, id)
	s.committed[id] = now.Add(s.tombstoneTTL)
}

// expireLocked removes an expired transaction and records a tombstone so
// late chunks are rejected instead of starting a new transaction.
func (s *Stager) expireLocked(id string, tx *txState, now time.Time) {
	s.used -= tx.bytes
	delete(s.txs, id)
	s.expired[id] = now.Add(s.tombstoneTTL)
}
