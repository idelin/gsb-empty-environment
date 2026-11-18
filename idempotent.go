// Package netsec provides idempotent producer validation for partitioned logs.
package netsec

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

const (
	// sequenceSpace is the number of sequence values in the int32 range
	// [0, math.MaxInt32]. Sequence numbers wrap back to zero after MaxInt32.
	sequenceSpace = int64(math.MaxInt32) + 1

	// recentBatchLimit is the number of most recent batches retained for
	// duplicate detection.
	recentBatchLimit = 5
)

// Status describes the outcome of applying a batch.
type Status uint8

const (
	// StatusAppended means the batch was new and received a new offset range.
	StatusAppended Status = iota
	// StatusDuplicate means the batch was already applied. Result contains
	// the offset range assigned to the original batch.
	StatusDuplicate
	// StatusFenced means the batch used an epoch older than the producer state.
	StatusFenced
	// StatusOutOfOrder means the batch's sequence was neither the next expected
	// sequence nor part of one of the cached recent batches.
	StatusOutOfOrder
	// StatusInvalidBatch means the batch itself was malformed.
	StatusInvalidBatch
	// StatusInvalidTransaction means the transactional ID was unbound or bound
	// to a different producer.
	StatusInvalidTransaction
)

// String returns a human-readable status name.
func (s Status) String() string {
	switch s {
	case StatusAppended:
		return "appended"
	case StatusDuplicate:
		return "duplicate"
	case StatusFenced:
		return "fenced"
	case StatusOutOfOrder:
		return "out-of-order"
	case StatusInvalidBatch:
		return "invalid-batch"
	case StatusInvalidTransaction:
		return "invalid-transaction"
	default:
		return fmt.Sprintf("status(%d)", uint8(s))
	}
}

var (
	// ErrFenced is returned when a producer presents an epoch older than the
	// epoch recorded for its (producerID, partition) pair.
	ErrFenced = errors.New("netsec: producer fenced")

	// ErrOutOfOrder is returned for a sequence gap or a sequence that is older
	// than the next expected sequence but not covered by a cached batch.
	ErrOutOfOrder = errors.New("netsec: sequence out of order")

	// ErrInvalidBatch is returned for malformed batches.
	ErrInvalidBatch = errors.New("netsec: invalid batch")

	// ErrTransaction is returned when a transactional ID is not bound or is
	// used by a producer other than its bound producer.
	ErrTransaction = errors.New("netsec: transactional ID rejected")

	// ErrTransactionalIDAlreadyBound is returned when binding a transactional
	// ID that already belongs to a different producer.
	ErrTransactionalIDAlreadyBound = errors.New("netsec: transactional ID already bound")
)

// Batch is one producer write to a partition.
//
// BaseSequence is the sequence number of the first record in the batch.
// RecordCount must be positive. Sequence numbers occupy the inclusive range
// [0, math.MaxInt32] and wrap from math.MaxInt32 to zero.
type Batch struct {
	ProducerID      int64
	Partition       int32
	Epoch           int32
	BaseSequence    int32
	RecordCount     int32
	TransactionalID string
}

// Result reports the outcome of applying a batch. BaseOffset and LastOffset
// are meaningful for StatusAppended and StatusDuplicate.
type Result struct {
	Status     Status
	BaseOffset int64
	LastOffset int64
}

// Duplicate reports whether the batch had already been applied.
func (r Result) Duplicate() bool {
	return r.Status == StatusDuplicate
}

// BatchRange describes one cached batch.
type BatchRange struct {
	BaseSequence int32
	RecordCount  int32
	BaseOffset   int64
	LastOffset   int64
}

// ProducerState is a copy of the validator state for one producer and
// partition. Batches are ordered from oldest to newest.
type ProducerState struct {
	Epoch        int32
	LastSequence int32
	Batches      []BatchRange
}

type producerKey struct {
	producerID int64
	partition  int32
}

type cachedBatch struct {
	baseSequence int32
	recordCount  int32
	baseOffset   int64
	lastOffset   int64
}

type producerState struct {
	epoch        int32
	lastSequence int32
	batches      []cachedBatch
}

// Validator validates idempotent writes and assigns log offsets.
//
// Validator is safe for concurrent use.
type Validator struct {
	mu          sync.Mutex
	producers   map[producerKey]*producerState
	nextOffsets map[int32]int64
	txBindings  map[string]int64
}

// NewValidator creates an empty validator.
func NewValidator() *Validator {
	return &Validator{
		producers:   make(map[producerKey]*producerState),
		nextOffsets: make(map[int32]int64),
		txBindings:  make(map[string]int64),
	}
}

// New is an alias for NewValidator.
func New() *Validator {
	return NewValidator()
}

// BindTransactionalID binds transactionalID to producerID.
//
// A transactional ID may be bound to only one producer. Repeating the same
// binding is allowed; binding the ID to a different producer returns
// ErrTransactionalIDAlreadyBound.
func (v *Validator) BindTransactionalID(transactionalID string, producerID int64) error {
	if transactionalID == "" {
		return fmt.Errorf("%w: transactional ID must not be empty", ErrTransaction)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if bound, ok := v.txBindings[transactionalID]; ok && bound != producerID {
		return fmt.Errorf("%w: %q belongs to producer %d", ErrTransactionalIDAlreadyBound, transactionalID, bound)
	}
	v.txBindings[transactionalID] = producerID
	return nil
}

// TransactionalIDBinding returns the producer bound to transactionalID.
func (v *Validator) TransactionalIDBinding(transactionalID string) (int64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	producerID, ok := v.txBindings[transactionalID]
	return producerID, ok
}

// Append validates a batch and, when it is new, assigns it an offset range.
//
// The batch is appended only when its base sequence equals the sequence after
// the producer's last accepted sequence. If the base sequence is contained in
// one of the five most recent accepted batches, Append reports a duplicate and
// returns the original offset range without changing state.
//
// An epoch lower than the cached epoch always returns StatusFenced, including
// when the sequence would otherwise look like a duplicate. A higher epoch
// clears the cached sequence history and requires the first batch to start at
// sequence zero.
func (v *Validator) Append(batch Batch) (Result, error) {
	if err := validateBatch(batch); err != nil {
		return Result{Status: StatusInvalidBatch}, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	key := producerKey{producerID: batch.ProducerID, partition: batch.Partition}
	current := v.producers[key]

	if current != nil && batch.Epoch < current.epoch {
		return Result{Status: StatusFenced}, fmt.Errorf(
			"%w: producer %d partition %d epoch %d is older than %d",
			ErrFenced, batch.ProducerID, batch.Partition, batch.Epoch, current.epoch,
		)
	}

	if batch.TransactionalID != "" {
		boundProducer, ok := v.txBindings[batch.TransactionalID]
		if !ok {
			return Result{Status: StatusInvalidTransaction}, fmt.Errorf(
				"%w: transactional ID %q is not bound",
				ErrTransaction, batch.TransactionalID,
			)
		}
		if boundProducer != batch.ProducerID {
			return Result{Status: StatusInvalidTransaction}, fmt.Errorf(
				"%w: transactional ID %q belongs to producer %d, not %d",
				ErrTransaction, batch.TransactionalID, boundProducer, batch.ProducerID,
			)
		}
	}

	if current == nil || batch.Epoch > current.epoch {
		if batch.BaseSequence != 0 {
			return Result{Status: StatusOutOfOrder}, fmt.Errorf(
				"%w: epoch %d must start at sequence 0, got %d",
				ErrOutOfOrder, batch.Epoch, batch.BaseSequence,
			)
		}
		return v.appendLocked(key, batch, true), nil
	}

	if duplicate, ok := findDuplicate(current.batches, batch.BaseSequence); ok {
		return Result{
			Status:     StatusDuplicate,
			BaseOffset: duplicate.baseOffset,
			LastOffset: duplicate.lastOffset,
		}, nil
	}

	expected := nextSequence(current.lastSequence)
	if batch.BaseSequence != expected {
		return Result{Status: StatusOutOfOrder}, fmt.Errorf(
			"%w: producer %d partition %d expected sequence %d, got %d",
			ErrOutOfOrder, batch.ProducerID, batch.Partition, expected, batch.BaseSequence,
		)
	}

	return v.appendLocked(key, batch, false), nil
}

// Apply is an alias for Append.
func (v *Validator) Apply(batch Batch) (Result, error) {
	return v.Append(batch)
}

func (v *Validator) appendLocked(key producerKey, batch Batch, resetHistory bool) Result {
	baseOffset := v.nextOffsets[key.partition]
	lastOffset := baseOffset + int64(batch.RecordCount) - 1
	v.nextOffsets[key.partition] = lastOffset + 1

	state := v.producers[key]
	if state == nil || resetHistory {
		state = &producerState{epoch: batch.Epoch}
		v.producers[key] = state
	}

	state.epoch = batch.Epoch
	state.lastSequence = addSequence(batch.BaseSequence, int64(batch.RecordCount)-1)
	state.batches = append(state.batches, cachedBatch{
		baseSequence: batch.BaseSequence,
		recordCount:  batch.RecordCount,
		baseOffset:   baseOffset,
		lastOffset:   lastOffset,
	})
	if len(state.batches) > recentBatchLimit {
		kept := make([]cachedBatch, recentBatchLimit)
		copy(kept, state.batches[len(state.batches)-recentBatchLimit:])
		state.batches = kept
	}

	return Result{
		Status:     StatusAppended,
		BaseOffset: baseOffset,
		LastOffset: lastOffset,
	}
}

// Restore seeds a producer's epoch and last accepted sequence after log
// recovery. The cached duplicate window is empty until new batches are
// appended. nextOffset is the next log offset for the partition; the larger of
// nextOffset and the current partition offset is retained.
func (v *Validator) Restore(producerID int64, partition int32, epoch, lastSequence int32, nextOffset int64) error {
	if epoch < 0 {
		return fmt.Errorf("%w: epoch must not be negative", ErrInvalidBatch)
	}
	if lastSequence < 0 {
		return fmt.Errorf("%w: last sequence must not be negative", ErrInvalidBatch)
	}
	if nextOffset < 0 {
		return fmt.Errorf("%w: next offset must not be negative", ErrInvalidBatch)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	key := producerKey{producerID: producerID, partition: partition}
	v.producers[key] = &producerState{
		epoch:        epoch,
		lastSequence: lastSequence,
	}
	if nextOffset > v.nextOffsets[partition] {
		v.nextOffsets[partition] = nextOffset
	}
	return nil
}

// NextOffset returns the next log offset that will be assigned in partition.
func (v *Validator) NextOffset(partition int32) int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.nextOffsets[partition]
}

// Snapshot returns a copy of the producer state for a (producerID, partition)
// pair.
func (v *Validator) Snapshot(producerID int64, partition int32) (ProducerState, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	state, ok := v.producers[producerKey{producerID: producerID, partition: partition}]
	if !ok {
		return ProducerState{}, false
	}

	snapshot := ProducerState{
		Epoch:        state.epoch,
		LastSequence: state.lastSequence,
		Batches:      make([]BatchRange, len(state.batches)),
	}
	for i, batch := range state.batches {
		snapshot.Batches[i] = BatchRange{
			BaseSequence: batch.baseSequence,
			RecordCount:  batch.recordCount,
			BaseOffset:   batch.baseOffset,
			LastOffset:   batch.lastOffset,
		}
	}
	return snapshot, true
}

func validateBatch(batch Batch) error {
	if batch.Epoch < 0 {
		return fmt.Errorf("%w: epoch must not be negative", ErrInvalidBatch)
	}
	if batch.BaseSequence < 0 {
		return fmt.Errorf("%w: base sequence must not be negative", ErrInvalidBatch)
	}
	if batch.RecordCount <= 0 {
		return fmt.Errorf("%w: record count must be positive", ErrInvalidBatch)
	}
	return nil
}

func findDuplicate(batches []cachedBatch, sequence int32) (cachedBatch, bool) {
	for i := len(batches) - 1; i >= 0; i-- {
		if sequenceInRange(sequence, batches[i].baseSequence, batches[i].recordCount) {
			return batches[i], true
		}
	}
	return cachedBatch{}, false
}

// sequenceInRange reports whether sequence is in the circular range starting
// at base and containing count values.
func sequenceInRange(sequence, base, count int32) bool {
	distance := (int64(sequence) - int64(base) + sequenceSpace) % sequenceSpace
	return distance < int64(count)
}

// addSequence adds delta modulo the sequence space, wrapping MaxInt32 to zero.
func addSequence(sequence int32, delta int64) int32 {
	return int32((int64(sequence) + delta) % sequenceSpace)
}

func nextSequence(sequence int32) int32 {
	return addSequence(sequence, 1)
}
