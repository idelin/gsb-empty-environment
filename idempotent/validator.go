// Package idempotent implements broker-side validation for the idempotent
// producer protocol of a partitioned append-only log.
//
// For every (producerId, partition) pair the validator remembers:
//   - the last seen producer epoch;
//   - the sequence interval and assigned base offset of the most recent
//     cachedBatchCount batches;
//   - the last accepted sequence number.
//
// Validation rules:
//   - a request whose epoch is lower than the stored epoch is fenced
//     (a zombie producer or a producerId thief cannot rewrite history);
//   - a request with a higher epoch resets the sequence state, which must
//     restart at sequence 0;
//   - a batch is appended only when its first sequence is lastSequence+1;
//   - a batch whose [first, last] interval is contained in a cached batch
//     is a retry and gets the original offset back without consuming new
//     log space;
//   - every other sequence is rejected as out of order;
//   - sequence numbers are int32 values: MaxInt32 is followed by 0;
//   - a transactional id may only be used by the producerId it is bound to.
package idempotent

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// cachedBatchCount is the number of recent batches retained per
// (producerId, partition) for duplicate detection, mirroring the
// broker-side producer state cache.
const cachedBatchCount = 5

// maxSequence is the largest legal sequence number before wrap-around.
const maxSequence = math.MaxInt32

// RejectReason explains why a write was rejected.
type RejectReason string

const (
	// ReasonFencedEpoch means the request carries an epoch older than the
	// one already accepted for this (producerId, partition).
	ReasonFencedEpoch RejectReason = "fenced_epoch"

	// ReasonOutOfOrder means the sequence number is neither the expected
	// successor nor a retry contained in a cached batch.
	ReasonOutOfOrder RejectReason = "out_of_order"

	// ReasonUnknownTransaction means the transactional id has never been
	// bound to a producerId.
	ReasonUnknownTransaction RejectReason = "unknown_transaction"

	// ReasonTransactionMismatch means the transactional id is bound to a
	// different producerId.
	ReasonTransactionMismatch RejectReason = "transactional_id_mismatch"

	// ReasonInvalidBatch means the request itself is malformed, for example
	// a non-positive record count or a batch crossing the wrap boundary.
	ReasonInvalidBatch RejectReason = "invalid_batch"
)

// ValidationError is returned for every rejected write. Inspect Reason with
// RejectReasonOf or errors.As instead of matching on the human-readable
// message.
type ValidationError struct {
	Reason          RejectReason
	ProducerID      int64
	Partition       int32
	Epoch           int16
	FirstSequence   int32
	LastSequence    int32
	TransactionalID string

	message string
}

func (e *ValidationError) Error() string {
	return e.message
}

// RejectReasonOf extracts the RejectReason from a validation error.
func RejectReasonOf(err error) RejectReason {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Reason
	}
	return ""
}

// WriteRequest is one idempotent append request, equivalent to a producer
// batch targeting a single partition.
type WriteRequest struct {
	ProducerID      int64
	Partition       int32
	ProducerEpoch   int16
	FirstSequence   int32
	RecordCount     int32
	TransactionalID string // empty means a non-transactional write
}

// AppendResult is the outcome of a successful validation.
type AppendResult struct {
	// BaseOffset is the log offset of the first record; for a duplicate
	// batch it is the base offset assigned to the original batch.
	BaseOffset int64
	// LastOffset is the offset of the last record of the request
	// according to its record count.
	LastOffset int64
	// Duplicate reports whether the request matched a cached batch and no
	// new log space was consumed.
	Duplicate bool
}

// Option customizes a Validator at construction time.
type Option func(*config)

type config struct {
	initialLogEndOffsets map[int32]int64
}

// WithInitialLogEndOffset seeds the log end offset of a partition, for
// example when a broker reopens an existing partition log. The offset must
// be non-negative.
func WithInitialLogEndOffset(partition int32, offset int64) Option {
	return func(c *config) {
		if offset < 0 {
			panic(fmt.Sprintf("idempotent: initial offset for partition %d is negative: %d", partition, offset))
		}
		if c.initialLogEndOffsets == nil {
			c.initialLogEndOffsets = make(map[int32]int64)
		}
		c.initialLogEndOffsets[partition] = offset
	}
}

// Validator holds the idempotent write state of all producers and
// partitions. It is safe for concurrent use.
type Validator struct {
	mu           sync.Mutex
	producers    map[producerKey]*producerState
	logEndOffset map[int32]int64
	transactions map[string]int64
}

type producerKey struct {
	producerID int64
	partition  int32
}

type producerState struct {
	known   bool
	epoch   int16
	hasLast bool
	lastSeq int32
	batches batchRing
}

type batchEntry struct {
	firstSeq   int32
	lastSeq    int32
	baseOffset int64
}

// batchRing retains the newest cachedBatchCount entries without allocation.
type batchRing struct {
	entries [cachedBatchCount]batchEntry
	head    int // next slot to overwrite
	size    int
}

func newProducerState() *producerState {
	return &producerState{}
}

func (s *producerState) reset() {
	s.hasLast = false
	s.lastSeq = 0
	s.batches = batchRing{}
}

func (r *batchRing) add(entry batchEntry) {
	r.entries[r.head] = entry
	r.head = (r.head + 1) % cachedBatchCount
	if r.size < cachedBatchCount {
		r.size++
	}
}

// find returns a cached batch fully containing the incoming [first, last]
// interval. Intervals never cross the wrap boundary, so ordinary int32
// comparison is sufficient.
func (r *batchRing) find(first, last int32) (batchEntry, bool) {
	for i := 0; i < r.size; i++ {
		idx := (r.head - 1 - i + cachedBatchCount) % cachedBatchCount
		entry := r.entries[idx]
		if first >= entry.firstSeq && last <= entry.lastSeq {
			return entry, true
		}
	}
	return batchEntry{}, false
}

// NewValidator creates an empty validator.
func NewValidator(options ...Option) *Validator {
	cfg := config{}
	for _, option := range options {
		option(&cfg)
	}
	logEndOffset := make(map[int32]int64)
	for partition, offset := range cfg.initialLogEndOffsets {
		logEndOffset[partition] = offset
	}
	return &Validator{
		producers:    make(map[producerKey]*producerState),
		logEndOffset: logEndOffset,
		transactions: make(map[string]int64),
	}
}

// BindTransactionalID binds a transactional id to a producerId. Rebinding
// the same id replaces the previous producerId, matching a coordinator
// fencing the old producer.
func (v *Validator) BindTransactionalID(transactionalID string, producerID int64) {
	if transactionalID == "" {
		panic("idempotent: transactional id must not be empty")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.transactions[transactionalID] = producerID
}

// BoundProducer reports the producerId bound to a transactional id.
func (v *Validator) BoundProducer(transactionalID string) (int64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	producerID, ok := v.transactions[transactionalID]
	return producerID, ok
}

// LogEndOffset returns the next offset to be assigned on the given
// partition. Duplicate batches do not advance it.
func (v *Validator) LogEndOffset(partition int32) int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.logEndOffset[partition]
}

// ProducerEpoch returns the epoch currently accepted for a
// (producerId, partition) pair.
func (v *Validator) ProducerEpoch(producerID int64, partition int32) (int16, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	state, ok := v.producers[producerKey{producerID, partition}]
	if !ok || !state.known {
		return 0, false
	}
	return state.epoch, true
}

// Append validates an idempotent write request and, when accepted, assigns
// the batch its base offset. Replayed batches return the original offset
// with Duplicate set to true.
func (v *Validator) Append(req WriteRequest) (AppendResult, error) {
	if req.RecordCount <= 0 {
		return AppendResult{}, rejectError(req, 0, ReasonInvalidBatch,
			"idempotent: record count must be positive, got %d", req.RecordCount)
	}
	lastSeq, ok := batchLastSequence(req.FirstSequence, req.RecordCount)
	if !ok {
		return AppendResult{}, rejectError(req, 0, ReasonInvalidBatch,
			"idempotent: batch starting at sequence %d with %d records crosses the int32 wrap boundary",
			req.FirstSequence, req.RecordCount)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if req.TransactionalID != "" {
		boundProducer, known := v.transactions[req.TransactionalID]
		if !known {
			return AppendResult{}, rejectError(req, lastSeq, ReasonUnknownTransaction,
				"idempotent: transactional id %q is not bound to any producer", req.TransactionalID)
		}
		if boundProducer != req.ProducerID {
			return AppendResult{}, rejectError(req, lastSeq, ReasonTransactionMismatch,
				"idempotent: transactional id %q is bound to producer %d, not %d",
				req.TransactionalID, boundProducer, req.ProducerID)
		}
	}

	key := producerKey{req.ProducerID, req.Partition}
	state := v.producers[key]
	if state == nil {
		state = newProducerState()
		v.producers[key] = state
	}

	if !state.known {
		if req.FirstSequence != 0 {
			return AppendResult{}, rejectError(req, lastSeq, ReasonOutOfOrder,
				"idempotent: new producer %d on partition %d must start at sequence 0, got %d",
				req.ProducerID, req.Partition, req.FirstSequence)
		}
		state.known = true
		state.epoch = req.ProducerEpoch
	} else if req.ProducerEpoch < state.epoch {
		return AppendResult{}, rejectError(req, lastSeq, ReasonFencedEpoch,
			"idempotent: producer %d on partition %d is fenced: request epoch %d < current epoch %d",
			req.ProducerID, req.Partition, req.ProducerEpoch, state.epoch)
	} else if req.ProducerEpoch > state.epoch {
		if req.FirstSequence != 0 {
			return AppendResult{}, rejectError(req, lastSeq, ReasonOutOfOrder,
				"idempotent: epoch bump of producer %d on partition %d requires restart at sequence 0, got %d",
				req.ProducerID, req.Partition, req.FirstSequence)
		}
		state.epoch = req.ProducerEpoch
		state.reset()
	}

	if state.hasLast {
		expected := nextSequence(state.lastSeq)
		if req.FirstSequence != expected {
			if entry, isDuplicate := state.batches.find(req.FirstSequence, lastSeq); isDuplicate {
				return AppendResult{
					BaseOffset: entry.baseOffset,
					LastOffset: entry.baseOffset + int64(req.RecordCount) - 1,
					Duplicate:  true,
				}, nil
			}
			return AppendResult{}, rejectError(req, lastSeq, ReasonOutOfOrder,
				"idempotent: out-of-order sequence for producer %d on partition %d: expected %d, got %d",
				req.ProducerID, req.Partition, expected, req.FirstSequence)
		}
	}

	baseOffset := v.logEndOffset[req.Partition]
	v.logEndOffset[req.Partition] = baseOffset + int64(req.RecordCount)
	state.batches.add(batchEntry{
		firstSeq:   req.FirstSequence,
		lastSeq:    lastSeq,
		baseOffset: baseOffset,
	})
	state.hasLast = true
	state.lastSeq = lastSeq

	return AppendResult{
		BaseOffset: baseOffset,
		LastOffset: baseOffset + int64(req.RecordCount) - 1,
		Duplicate:  false,
	}, nil
}

// nextSequence returns the legal successor of a sequence number, wrapping
// from MaxInt32 back to 0.
func nextSequence(seq int32) int32 {
	if seq == maxSequence {
		return 0
	}
	return seq + 1
}

// batchLastSequence computes the last sequence of a batch and reports
// whether the interval crosses the wrap boundary (illegal for one batch).
func batchLastSequence(first int32, count int32) (int32, bool) {
	end := uint32(first) + uint32(count) - 1
	if end > math.MaxInt32 {
		return 0, false
	}
	return int32(end), true
}

func rejectError(req WriteRequest, lastSeq int32, reason RejectReason, format string, args ...any) error {
	return &ValidationError{
		Reason:          reason,
		ProducerID:      req.ProducerID,
		Partition:       req.Partition,
		Epoch:           req.ProducerEpoch,
		FirstSequence:   req.FirstSequence,
		LastSequence:    lastSeq,
		TransactionalID: req.TransactionalID,
		message:         fmt.Sprintf(format, args...),
	}
}
