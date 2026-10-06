package chunkedtx

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	// ErrBudgetExceeded is returned when staging a chunk would exceed
	// the global staging budget.
	ErrBudgetExceeded = errors.New("chunkedtx: staging budget exceeded")
	// ErrTxFailed is returned when a chunk targets a transaction that
	// has already permanently failed.
	ErrTxFailed = errors.New("chunkedtx: transaction permanently failed")
	// ErrTxCommitted is returned when a chunk targets a transaction
	// that has already committed.
	ErrTxCommitted = errors.New("chunkedtx: transaction already committed")
	// ErrChunkExpired is returned when a chunk arrives after its own
	// expiry time.
	ErrChunkExpired = errors.New("chunkedtx: chunk already expired")
	// ErrTxExpired is returned when a chunk targets a transaction whose
	// deadline has passed.
	ErrTxExpired = errors.New("chunkedtx: transaction expired")
)

// Config tunes an Assembler.
type Config struct {
	// MaxStagedBytes bounds the total payload bytes staged across all
	// in-flight transactions. Must be positive.
	MaxStagedBytes int64
	// Now supplies the current time for expiry checks. Defaults to
	// time.Now. Inject a fake clock in tests.
	Now func() time.Time
}

type stagedChunk struct {
	payload []byte
	final   bool
}

type txState struct {
	chunks      map[uint32]stagedChunk
	finalSeq    int64 // -1 until the final chunk is staged
	finalDigest [sha256.Size]byte
	bytes       int64
	deadline    time.Time // zero = no expiry
}

func newTxState() *txState {
	return &txState{chunks: make(map[uint32]stagedChunk), finalSeq: -1}
}

// expired reports whether the transaction passed its deadline.
func (t *txState) expired(now time.Time) bool {
	return !t.deadline.IsZero() && !now.Before(t.deadline)
}

// complete reports whether chunks 0..finalSeq are all staged.
func (t *txState) complete() bool {
	if t.finalSeq < 0 {
		return false
	}
	return int64(len(t.chunks)) == t.finalSeq+1
}

// assemble concatenates payloads in sequence order.
func (t *txState) assemble() []byte {
	var total int
	for _, c := range t.chunks {
		total += len(c.payload)
	}
	out := make([]byte, 0, total)
	for seq := int64(0); seq <= t.finalSeq; seq++ {
		out = append(out, t.chunks[uint32(seq)].payload...)
	}
	return out
}

// Assembler stages chunks for in-flight transactions and publishes
// commits. It is safe for concurrent use.
//
// Terminal states (committed or permanently failed) are remembered so
// that late or replayed events are rejected instead of resurrecting a
// transaction; this record grows with the number of finished
// transactions and is not counted against the staging budget.
type Assembler struct {
	cfg Config

	mu       sync.Mutex
	txs      map[string]*txState
	terminal map[string]error
	staged   int64
	onCommit func(Commit)
}

// New creates an Assembler. onCommit, if non-nil, is invoked with each
// published commit.
func New(cfg Config, onCommit func(Commit)) (*Assembler, error) {
	if cfg.MaxStagedBytes <= 0 {
		return nil, fmt.Errorf("chunkedtx: MaxStagedBytes must be positive, got %d", cfg.MaxStagedBytes)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Assembler{
		cfg:      cfg,
		txs:      make(map[string]*txState),
		terminal: make(map[string]error),
		onCommit: onCommit,
	}, nil
}

// Ingest stages one chunk. It never publishes a transaction before the
// full contiguous sequence is staged and the total digest matches.
func (a *Assembler) Ingest(c Chunk) (Outcome, error) {
	if c.TxID == "" {
		return Outcome{}, errors.New("chunkedtx: empty transaction id")
	}
	if !c.Final && c.TotalDigest != ([sha256.Size]byte{}) {
		return Outcome{}, errors.New("chunkedtx: total digest set on non-final chunk")
	}
	if c.Final && c.TotalDigest == ([sha256.Size]byte{}) {
		return Outcome{}, errors.New("chunkedtx: final chunk missing total digest")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.cfg.Now()

	// Late events for finished transactions are rejected, never staged.
	if term, done := a.terminal[c.TxID]; done {
		return Outcome{}, term
	}

	tx, ok := a.txs[c.TxID]
	if ok {
		// Lazily expire the transaction before accepting new work.
		if tx.expired(now) {
			a.failLocked(c.TxID, tx)
			return Outcome{}, ErrTxExpired
		}

		// Duplicate handling: an identical chunk is a no-op, a
		// conflicting chunk permanently fails the transaction.
		if prev, dup := tx.chunks[c.Seq]; dup {
			if prev.final == c.Final && string(prev.payload) == string(c.Payload) {
				return Outcome{Status: StatusDuplicate, TxID: c.TxID}, nil
			}
			a.failLocked(c.TxID, tx)
			return Outcome{}, fmt.Errorf("chunkedtx: conflicting chunk for tx %q seq %d: %v", c.TxID, c.Seq, ErrTxFailed)
		}

		// A second, distinct final chunk is a conflict.
		if c.Final && tx.finalSeq >= 0 && tx.finalSeq != int64(c.Seq) {
			a.failLocked(c.TxID, tx)
			return Outcome{}, fmt.Errorf("chunkedtx: conflicting final chunk for tx %q: %v", c.TxID, ErrTxFailed)
		}
	}

	// An event that is already expired on arrival is never staged.
	if !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt) {
		return Outcome{}, ErrChunkExpired
	}

	// Enforce the global staging budget.
	if a.staged+int64(len(c.Payload)) > a.cfg.MaxStagedBytes {
		return Outcome{}, ErrBudgetExceeded
	}

	if !ok {
		tx = newTxState()
		a.txs[c.TxID] = tx
	}

	payload := make([]byte, len(c.Payload))
	copy(payload, c.Payload)
	tx.chunks[c.Seq] = stagedChunk{payload: payload, final: c.Final}
	tx.bytes += int64(len(payload))
	a.staged += int64(len(payload))
	if c.Final {
		tx.finalSeq = int64(c.Seq)
		tx.finalDigest = c.TotalDigest
	}
	if !c.ExpiresAt.IsZero() && (tx.deadline.IsZero() || c.ExpiresAt.Before(tx.deadline)) {
		tx.deadline = c.ExpiresAt
	}

	if !tx.complete() {
		return Outcome{Status: StatusStaged, TxID: c.TxID}, nil
	}

	assembled := tx.assemble()
	if sha256.Sum256(assembled) != tx.finalDigest {
		a.failLocked(c.TxID, tx)
		return Outcome{}, fmt.Errorf("chunkedtx: total digest mismatch for tx %q: %v", c.TxID, ErrTxFailed)
	}

	commit := Commit{TxID: c.TxID, Payload: assembled, Chunks: len(tx.chunks)}
	a.releaseLocked(c.TxID, tx)
	a.terminal[c.TxID] = ErrTxCommitted
	if a.onCommit != nil {
		a.onCommit(commit)
	}
	return Outcome{Status: StatusCommitted, TxID: c.TxID, Commit: &commit}, nil
}

// Reap permanently fails every transaction whose deadline has passed
// and returns the ids of the failed transactions, sorted.
func (a *Assembler) Reap() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.cfg.Now()
	var reaped []string
	for id, tx := range a.txs {
		if tx.expired(now) {
			a.failLocked(id, tx)
			reaped = append(reaped, id)
		}
	}
	sort.Strings(reaped)
	return reaped
}

// StagedBytes reports the currently staged payload bytes.
func (a *Assembler) StagedBytes() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.staged
}

// InFlight reports the number of transactions currently staged.
func (a *Assembler) InFlight() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.txs)
}

// failLocked permanently fails a transaction and frees its budget.
func (a *Assembler) failLocked(id string, tx *txState) {
	a.releaseLocked(id, tx)
	a.terminal[id] = ErrTxFailed
}

// releaseLocked removes a transaction and returns its budget.
func (a *Assembler) releaseLocked(id string, tx *txState) {
	a.staged -= tx.bytes
	delete(a.txs, id)
}
