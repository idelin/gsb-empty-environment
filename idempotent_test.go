package netsec

import (
	"errors"
	"math"
	"testing"
)

func mustAppend(t *testing.T, validator *Validator, batch Batch) Result {
	t.Helper()

	result, err := validator.Append(batch)
	if err != nil {
		t.Fatalf("Append(%+v) returned error: %v", batch, err)
	}
	if result.Status != StatusAppended {
		t.Fatalf("Append(%+v) status = %s, want appended", batch, result.Status)
	}
	return result
}

func mustDuplicate(t *testing.T, validator *Validator, batch Batch, baseOffset, lastOffset int64) {
	t.Helper()

	result, err := validator.Append(batch)
	if err != nil {
		t.Fatalf("duplicate Append(%+v) returned error: %v", batch, err)
	}
	if result.Status != StatusDuplicate {
		t.Fatalf("duplicate Append(%+v) status = %s, want duplicate", batch, result.Status)
	}
	if result.BaseOffset != baseOffset || result.LastOffset != lastOffset {
		t.Fatalf(
			"duplicate Append(%+v) offsets = [%d,%d], want [%d,%d]",
			batch, result.BaseOffset, result.LastOffset, baseOffset, lastOffset,
		)
	}
}

func TestAppendAndDuplicate(t *testing.T) {
	validator := NewValidator()
	first := Batch{
		ProducerID:   10,
		Partition:    1,
		Epoch:        3,
		BaseSequence: 0,
		RecordCount:  3,
	}

	result := mustAppend(t, validator, first)
	if result.BaseOffset != 0 || result.LastOffset != 2 {
		t.Fatalf("first offsets = [%d,%d], want [0,2]", result.BaseOffset, result.LastOffset)
	}

	mustDuplicate(t, validator, first, 0, 2)

	second := Batch{
		ProducerID:   10,
		Partition:    1,
		Epoch:        3,
		BaseSequence: 3,
		RecordCount:  2,
	}
	result = mustAppend(t, validator, second)
	if result.BaseOffset != 3 || result.LastOffset != 4 {
		t.Fatalf("second offsets = [%d,%d], want [3,4]", result.BaseOffset, result.LastOffset)
	}

	mustDuplicate(t, validator, first, 0, 2)
	mustDuplicate(t, validator, second, 3, 4)

	state, ok := validator.Snapshot(10, 1)
	if !ok {
		t.Fatal("expected producer state")
	}
	if state.LastSequence != 4 || len(state.Batches) != 2 {
		t.Fatalf("state = %+v, want last sequence 4 and 2 cached batches", state)
	}
	if got := validator.NextOffset(1); got != 5 {
		t.Fatalf("next offset = %d, want 5", got)
	}
}

func TestZombieProducerIsFenced(t *testing.T) {
	validator := NewValidator()
	live := Batch{
		ProducerID:   42,
		Partition:    7,
		Epoch:        5,
		BaseSequence: 0,
		RecordCount:  2,
	}
	mustAppend(t, validator, live)

	zombie := Batch{
		ProducerID:   42,
		Partition:    7,
		Epoch:        4,
		BaseSequence: 2,
		RecordCount:  1,
	}
	result, err := validator.Append(zombie)
	if !errors.Is(err, ErrFenced) {
		t.Fatalf("zombie Append error = %v, want ErrFenced", err)
	}
	if result.Status != StatusFenced {
		t.Fatalf("zombie status = %s, want fenced", result.Status)
	}

	zombie.BaseSequence = 0
	result, err = validator.Append(zombie)
	if !errors.Is(err, ErrFenced) || result.Status != StatusFenced {
		t.Fatalf("stale duplicate was not fenced: result=%+v err=%v", result, err)
	}

	state, _ := validator.Snapshot(42, 7)
	if state.Epoch != 5 || state.LastSequence != 1 {
		t.Fatalf("fenced write mutated state: %+v", state)
	}
	if got := validator.NextOffset(7); got != 2 {
		t.Fatalf("fenced write consumed offsets, next = %d, want 2", got)
	}

	next := Batch{
		ProducerID:   42,
		Partition:    7,
		Epoch:        5,
		BaseSequence: 2,
		RecordCount:  1,
	}
	result = mustAppend(t, validator, next)
	if result.BaseOffset != 2 {
		t.Fatalf("live producer offset = %d, want 2", result.BaseOffset)
	}
}

func TestHigherEpochRestartsSequence(t *testing.T) {
	validator := NewValidator()
	mustAppend(t, validator, Batch{
		ProducerID:   11,
		Partition:    2,
		Epoch:        1,
		BaseSequence: 0,
		RecordCount:  2,
	})

	fresh := Batch{
		ProducerID:   11,
		Partition:    2,
		Epoch:        2,
		BaseSequence: 0,
		RecordCount:  1,
	}
	result := mustAppend(t, validator, fresh)
	if result.BaseOffset != 2 {
		t.Fatalf("offset after epoch bump = %d, want 2", result.BaseOffset)
	}

	state, _ := validator.Snapshot(11, 2)
	if state.Epoch != 2 || state.LastSequence != 0 || len(state.Batches) != 1 {
		t.Fatalf("epoch bump did not reset history: %+v", state)
	}

	// The old epoch is now a zombie even though sequence 0 was a duplicate.
	if _, err := validator.Append(Batch{
		ProducerID:   11,
		Partition:    2,
		Epoch:        1,
		BaseSequence: 0,
		RecordCount:  2,
	}); !errors.Is(err, ErrFenced) {
		t.Fatalf("old epoch error = %v, want ErrFenced", err)
	}

	mustAppend(t, validator, Batch{
		ProducerID:   11,
		Partition:    2,
		Epoch:        2,
		BaseSequence: 1,
		RecordCount:  1,
	})
}

func TestHigherEpochMustStartAtZero(t *testing.T) {
	validator := NewValidator()
	mustAppend(t, validator, Batch{
		ProducerID:   12,
		Partition:    0,
		Epoch:        1,
		BaseSequence: 0,
		RecordCount:  2,
	})

	result, err := validator.Append(Batch{
		ProducerID:   12,
		Partition:    0,
		Epoch:        2,
		BaseSequence: 3,
		RecordCount:  1,
	})
	if !errors.Is(err, ErrOutOfOrder) || result.Status != StatusOutOfOrder {
		t.Fatalf("higher epoch gap: result=%+v err=%v, want out-of-order", result, err)
	}

	state, _ := validator.Snapshot(12, 0)
	if state.Epoch != 1 || state.LastSequence != 1 {
		t.Fatalf("rejected epoch bump mutated state: %+v", state)
	}
}

func TestSequenceGapRejected(t *testing.T) {
	validator := NewValidator()

	if _, err := validator.Append(Batch{
		ProducerID:   1,
		Partition:    0,
		Epoch:        0,
		BaseSequence: 1,
		RecordCount:  1,
	}); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("initial gap error = %v, want ErrOutOfOrder", err)
	}

	mustAppend(t, validator, Batch{
		ProducerID:   1,
		Partition:    0,
		Epoch:        0,
		BaseSequence: 0,
		RecordCount:  2,
	})

	if _, err := validator.Append(Batch{
		ProducerID:   1,
		Partition:    0,
		Epoch:        0,
		BaseSequence: 3,
		RecordCount:  1,
	}); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("gap error = %v, want ErrOutOfOrder", err)
	}

	mustAppend(t, validator, Batch{
		ProducerID:   1,
		Partition:    0,
		Epoch:        0,
		BaseSequence: 2,
		RecordCount:  1,
	})
}

func TestSequenceWrapsAfterMaxInt32(t *testing.T) {
	validator := NewValidator()
	if err := validator.Restore(20, 3, 4, math.MaxInt32-1, 100); err != nil {
		t.Fatal(err)
	}

	result := mustAppend(t, validator, Batch{
		ProducerID:   20,
		Partition:    3,
		Epoch:        4,
		BaseSequence: math.MaxInt32,
		RecordCount:  1,
	})
	if result.BaseOffset != 100 {
		t.Fatalf("wrapped batch offset = %d, want 100", result.BaseOffset)
	}

	state, _ := validator.Snapshot(20, 3)
	if state.LastSequence != math.MaxInt32 {
		t.Fatalf("last sequence = %d, want MaxInt32", state.LastSequence)
	}

	// MaxInt32 + 1 wraps to zero.
	result = mustAppend(t, validator, Batch{
		ProducerID:   20,
		Partition:    3,
		Epoch:        4,
		BaseSequence: 0,
		RecordCount:  2,
	})
	if result.BaseOffset != 101 || result.LastOffset != 102 {
		t.Fatalf("post-wrap offsets = [%d,%d], want [101,102]", result.BaseOffset, result.LastOffset)
	}
	state, _ = validator.Snapshot(20, 3)
	if state.LastSequence != 1 {
		t.Fatalf("post-wrap last sequence = %d, want 1", state.LastSequence)
	}
}

func TestBatchMayCrossWrapBoundary(t *testing.T) {
	validator := NewValidator()
	if err := validator.Restore(21, 0, 1, math.MaxInt32-2, 0); err != nil {
		t.Fatal(err)
	}

	result := mustAppend(t, validator, Batch{
		ProducerID:   21,
		Partition:    0,
		Epoch:        1,
		BaseSequence: math.MaxInt32 - 1,
		RecordCount:  3,
	})
	if result.LastOffset != 2 {
		t.Fatalf("last offset = %d, want 2", result.LastOffset)
	}
	state, _ := validator.Snapshot(21, 0)
	if state.LastSequence != 0 {
		t.Fatalf("last sequence = %d, want 0 after crossing wrap", state.LastSequence)
	}

	// A base sequence inside the wrapped batch is a duplicate.
	mustDuplicate(t, validator, Batch{
		ProducerID:   21,
		Partition:    0,
		Epoch:        1,
		BaseSequence: 0,
		RecordCount:  1,
	}, 0, 2)

	mustAppend(t, validator, Batch{
		ProducerID:   21,
		Partition:    0,
		Epoch:        1,
		BaseSequence: 1,
		RecordCount:  1,
	})
}

func TestGapAfterWrapRejected(t *testing.T) {
	validator := NewValidator()
	if err := validator.Restore(22, 0, 1, math.MaxInt32, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := validator.Append(Batch{
		ProducerID:   22,
		Partition:    0,
		Epoch:        1,
		BaseSequence: 2,
		RecordCount:  1,
	}); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("gap after wrap error = %v, want ErrOutOfOrder", err)
	}

	mustAppend(t, validator, Batch{
		ProducerID:   22,
		Partition:    0,
		Epoch:        1,
		BaseSequence: 0,
		RecordCount:  1,
	})
}

func TestOnlyRecentFiveBatchesAreDeduplicated(t *testing.T) {
	validator := NewValidator()
	batches := make([]Batch, 6)
	offsets := make([]int64, len(batches))

	for i := range batches {
		batches[i] = Batch{
			ProducerID:   30,
			Partition:    0,
			Epoch:        1,
			BaseSequence: int32(i),
			RecordCount:  1,
		}
		result := mustAppend(t, validator, batches[i])
		offsets[i] = result.BaseOffset
	}

	state, _ := validator.Snapshot(30, 0)
	if len(state.Batches) != recentBatchLimit {
		t.Fatalf("cached batches = %d, want %d", len(state.Batches), recentBatchLimit)
	}

	result, err := validator.Append(batches[0])
	if !errors.Is(err, ErrOutOfOrder) || result.Status != StatusOutOfOrder {
		t.Fatalf("evicted duplicate: result=%+v err=%v, want out-of-order", result, err)
	}

	for i := 1; i < len(batches); i++ {
		mustDuplicate(t, validator, batches[i], offsets[i], offsets[i])
	}
}

func TestTransactionalIDBinding(t *testing.T) {
	validator := NewValidator()
	if err := validator.BindTransactionalID("orders-tx", 50); err != nil {
		t.Fatal(err)
	}
	if err := validator.BindTransactionalID("orders-tx", 50); err != nil {
		t.Fatalf("idempotent binding failed: %v", err)
	}
	if err := validator.BindTransactionalID("orders-tx", 51); !errors.Is(err, ErrTransactionalIDAlreadyBound) {
		t.Fatalf("conflicting binding error = %v, want ErrTransactionalIDAlreadyBound", err)
	}

	mustAppend(t, validator, Batch{
		ProducerID:      50,
		Partition:       0,
		Epoch:           0,
		BaseSequence:    0,
		RecordCount:     1,
		TransactionalID: "orders-tx",
	})

	result, err := validator.Append(Batch{
		ProducerID:      51,
		Partition:       0,
		Epoch:           0,
		BaseSequence:    0,
		RecordCount:     1,
		TransactionalID: "orders-tx",
	})
	if !errors.Is(err, ErrTransaction) || result.Status != StatusInvalidTransaction {
		t.Fatalf("stolen transactional ID: result=%+v err=%v", result, err)
	}

	result, err = validator.Append(Batch{
		ProducerID:      50,
		Partition:       0,
		Epoch:           0,
		BaseSequence:    1,
		RecordCount:     1,
		TransactionalID: "unbound-tx",
	})
	if !errors.Is(err, ErrTransaction) || result.Status != StatusInvalidTransaction {
		t.Fatalf("unbound transactional ID: result=%+v err=%v", result, err)
	}

	if got := validator.NextOffset(0); got != 1 {
		t.Fatalf("rejected transactional writes consumed offsets, next = %d", got)
	}
}

func TestInvalidBatch(t *testing.T) {
	validator := NewValidator()
	tests := []Batch{
		{ProducerID: 1, Partition: 0, Epoch: -1, BaseSequence: 0, RecordCount: 1},
		{ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: -1, RecordCount: 1},
		{ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: 0, RecordCount: 0},
	}
	for _, batch := range tests {
		result, err := validator.Append(batch)
		if !errors.Is(err, ErrInvalidBatch) || result.Status != StatusInvalidBatch {
			t.Fatalf("Append(%+v): result=%+v err=%v, want invalid batch", batch, result, err)
		}
	}
}

func TestPartitionsHaveIndependentOffsets(t *testing.T) {
	validator := NewValidator()

	first := mustAppend(t, validator, Batch{
		ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: 0, RecordCount: 2,
	})
	second := mustAppend(t, validator, Batch{
		ProducerID: 1, Partition: 1, Epoch: 0, BaseSequence: 0, RecordCount: 2,
	})
	third := mustAppend(t, validator, Batch{
		ProducerID: 2, Partition: 0, Epoch: 0, BaseSequence: 0, RecordCount: 2,
	})

	if first.BaseOffset != 0 || second.BaseOffset != 0 || third.BaseOffset != 2 {
		t.Fatalf("partition offsets = %d, %d, %d; want 0, 0, 2",
			first.BaseOffset, second.BaseOffset, third.BaseOffset)
	}
}

func TestOutOfOrderDoesNotConsumeOffsets(t *testing.T) {
	validator := NewValidator()
	mustAppend(t, validator, Batch{
		ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: 0, RecordCount: 2,
	})

	if _, err := validator.Append(Batch{
		ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: 9, RecordCount: 4,
	}); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("gap error = %v, want ErrOutOfOrder", err)
	}
	if got := validator.NextOffset(0); got != 2 {
		t.Fatalf("out-of-order write consumed offsets, next = %d, want 2", got)
	}
}

func TestDuplicateSubRangeReturnsOriginalBatchOffsets(t *testing.T) {
	validator := NewValidator()
	original := Batch{
		ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: 0, RecordCount: 4,
	}
	mustAppend(t, validator, original)

	// Any sequence inside the cached batch identifies the original batch.
	mustDuplicate(t, validator, Batch{
		ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: 2, RecordCount: 1,
	}, 0, 3)
}

func TestApplyAlias(t *testing.T) {
	validator := NewValidator()
	result, err := validator.Apply(Batch{
		ProducerID: 1, Partition: 0, Epoch: 0, BaseSequence: 0, RecordCount: 1,
	})
	if err != nil || result.Status != StatusAppended {
		t.Fatalf("Apply: result=%+v err=%v", result, err)
	}
}

func TestRestoreAndLookupHelpers(t *testing.T) {
	validator := NewValidator()
	if err := validator.Restore(7, 0, 1, 4, 10); err != nil {
		t.Fatal(err)
	}
	if got := validator.NextOffset(0); got != 10 {
		t.Fatalf("next offset = %d, want 10", got)
	}

	// Restore never moves the partition offset backwards.
	if err := validator.Restore(8, 0, 0, 0, 3); err != nil {
		t.Fatal(err)
	}
	if got := validator.NextOffset(0); got != 10 {
		t.Fatalf("next offset moved backwards to %d", got)
	}

	if err := validator.Restore(9, 0, 1, 2, -1); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("negative restore offset error = %v", err)
	}
	if err := validator.Restore(9, 0, -1, 0, 0); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("negative epoch error = %v", err)
	}
	if err := validator.Restore(9, 0, 0, -1, 0); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("negative sequence error = %v", err)
	}

	if _, ok := validator.Snapshot(404, 0); ok {
		t.Fatal("unexpected snapshot for unknown producer")
	}
}

func TestTransactionalBindingHelpers(t *testing.T) {
	validator := NewValidator()
	if err := validator.BindTransactionalID("", 1); !errors.Is(err, ErrTransaction) {
		t.Fatalf("empty transactional ID error = %v", err)
	}
	if err := validator.BindTransactionalID("tx", 1); err != nil {
		t.Fatal(err)
	}
	producerID, ok := validator.TransactionalIDBinding("tx")
	if !ok || producerID != 1 {
		t.Fatalf("binding = (%d, %v), want (1, true)", producerID, ok)
	}
	if _, ok := validator.TransactionalIDBinding("missing"); ok {
		t.Fatal("unexpected binding for missing transactional ID")
	}
}

func TestStatusString(t *testing.T) {
	tests := map[Status]string{
		StatusAppended:           "appended",
		StatusDuplicate:          "duplicate",
		StatusFenced:             "fenced",
		StatusOutOfOrder:         "out-of-order",
		StatusInvalidBatch:       "invalid-batch",
		StatusInvalidTransaction: "invalid-transaction",
		Status(99):               "status(99)",
	}
	for status, want := range tests {
		if got := status.String(); got != want {
			t.Fatalf("Status(%d).String() = %q, want %q", uint8(status), got, want)
		}
	}
}
