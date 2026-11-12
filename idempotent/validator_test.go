package idempotent

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func writeReq(producerID int64, partition int32, epoch int16, firstSeq int32, count int32) WriteRequest {
	return WriteRequest{
		ProducerID:    producerID,
		Partition:     partition,
		ProducerEpoch: epoch,
		FirstSequence: firstSeq,
		RecordCount:   count,
	}
}

func assertReject(t *testing.T, err error, want RejectReason, req WriteRequest, wantLastSeq int32) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection with reason %q, got success", want)
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if got := RejectReasonOf(err); got != want {
		t.Fatalf("reject reason = %q, want %q (%v)", got, want, err)
	}
	if validationErr.ProducerID != req.ProducerID ||
		validationErr.Partition != req.Partition ||
		validationErr.Epoch != req.ProducerEpoch ||
		validationErr.FirstSequence != req.FirstSequence {
		t.Fatalf("error fields do not echo request: %+v vs %+v", validationErr, req)
	}
	if validationErr.LastSequence != wantLastSeq {
		t.Fatalf("error LastSequence = %d, want %d", validationErr.LastSequence, wantLastSeq)
	}
}

func assertAppend(t *testing.T, v *Validator, req WriteRequest, wantBase, wantLast int64, wantDuplicate bool) AppendResult {
	t.Helper()
	result, err := v.Append(req)
	if err != nil {
		t.Fatalf("Append(%+v) unexpected error: %v", req, err)
	}
	if result.BaseOffset != wantBase || result.LastOffset != wantLast || result.Duplicate != wantDuplicate {
		t.Fatalf("Append(%+v) = %+v, want base=%d last=%d duplicate=%v",
			req, result, wantBase, wantLast, wantDuplicate)
	}
	return result
}

func TestAppendHappyPathAndOffsets(t *testing.T) {
	v := NewValidator()

	assertAppend(t, v, writeReq(1, 7, 0, 0, 3), 0, 2, false)
	assertAppend(t, v, writeReq(1, 7, 0, 3, 2), 3, 4, false)

	// A different producer on the same partition gets its own offset range
	// within the partition log.
	assertAppend(t, v, writeReq(2, 7, 0, 0, 1), 5, 5, false)

	// The same producer on another partition starts both sequence and
	// offset independently.
	assertAppend(t, v, writeReq(1, 8, 0, 0, 1), 0, 0, false)

	if got := v.LogEndOffset(7); got != 6 {
		t.Fatalf("partition 7 log end offset = %d, want 6", got)
	}
	if got := v.LogEndOffset(8); got != 1 {
		t.Fatalf("partition 8 log end offset = %d, want 1", got)
	}
}

func TestNewProducerMustStartAtZero(t *testing.T) {
	v := NewValidator()

	_, err := v.Append(writeReq(1, 0, 0, 1, 1))
	assertReject(t, err, ReasonOutOfOrder, writeReq(1, 0, 0, 1, 1), 1)

	if _, ok := v.ProducerEpoch(1, 0); ok {
		t.Fatal("rejected first write must not register the producer")
	}

	assertAppend(t, v, writeReq(1, 0, 0, 0, 1), 0, 0, false)
	if epoch, ok := v.ProducerEpoch(1, 0); !ok || epoch != 0 {
		t.Fatalf("ProducerEpoch = (%d, %v), want (0, true)", epoch, ok)
	}
}

func TestZombieProducerLowerEpochFenced(t *testing.T) {
	v := NewValidator()

	// Live producer appends with epoch 5.
	assertAppend(t, v, writeReq(42, 0, 5, 0, 2), 0, 1, false)
	assertAppend(t, v, writeReq(42, 0, 5, 2, 1), 2, 2, false)

	// Zombie reusing the producerId with an older epoch cannot append,
	// cannot skip sequences, and cannot rewrite confirmed messages.
	zombieReq := writeReq(42, 0, 4, 3, 1)
	_, err := v.Append(zombieReq)
	assertReject(t, err, ReasonFencedEpoch, zombieReq, 3)

	// Even replaying an old sequence with a stale epoch is fenced:
	// epoch is checked before the duplicate lookup.
	staleRetry := writeReq(42, 0, 3, 0, 2)
	_, err = v.Append(staleRetry)
	assertReject(t, err, ReasonFencedEpoch, staleRetry, 1)

	// Rejected writes must not consume any offset.
	if got := v.LogEndOffset(0); got != 3 {
		t.Fatalf("log end offset = %d, want 3", got)
	}

	// The live producer continues unaffected.
	assertAppend(t, v, writeReq(42, 0, 5, 3, 1), 3, 3, false)
}

func TestHigherEpochRestartsSequenceAtZero(t *testing.T) {
	v := NewValidator()

	assertAppend(t, v, writeReq(7, 0, 1, 0, 2), 0, 1, false)

	// Epoch bump that does not restart at zero is out of order.
	badBump := writeReq(7, 0, 2, 2, 1)
	_, err := v.Append(badBump)
	assertReject(t, err, ReasonOutOfOrder, badBump, 2)

	if epoch, _ := v.ProducerEpoch(7, 0); epoch != 1 {
		t.Fatalf("epoch advanced despite rejection: got %d", epoch)
	}

	// Valid bump resets sequence space and starts again at zero.
	assertAppend(t, v, writeReq(7, 0, 2, 0, 3), 2, 4, false)

	if epoch, _ := v.ProducerEpoch(7, 0); epoch != 2 {
		t.Fatalf("epoch = %d, want 2", epoch)
	}

	// Old-epoch producer is now a zombie.
	oldEpochReq := writeReq(7, 0, 1, 0, 1)
	_, err = v.Append(oldEpochReq)
	assertReject(t, err, ReasonFencedEpoch, oldEpochReq, 0)
}

func TestDuplicateBatchesReturnOriginalOffset(t *testing.T) {
	v := NewValidator(WithInitialLogEndOffset(0, 10))

	assertAppend(t, v, writeReq(9, 0, 0, 0, 4), 10, 13, false)
	assertAppend(t, v, writeReq(9, 0, 0, 4, 2), 14, 15, false)

	// Exact retry of the first batch after later batches were accepted.
	assertAppend(t, v, writeReq(9, 0, 0, 0, 4), 10, 13, true)
	// Exact retry of the latest batch.
	assertAppend(t, v, writeReq(9, 0, 0, 4, 2), 14, 15, true)
	// A sub-range fully contained in a cached batch is also a retry and
	// reports the original batch base offset.
	assertAppend(t, v, writeReq(9, 0, 0, 1, 2), 10, 11, true)

	// Duplicates must not move the log end offset.
	if got := v.LogEndOffset(0); got != 16 {
		t.Fatalf("log end offset = %d, want 16", got)
	}

	// Normal flow resumes at the expected successor.
	assertAppend(t, v, writeReq(9, 0, 0, 6, 1), 16, 16, false)
}

func TestOnlyLastFiveBatchesAreCached(t *testing.T) {
	v := NewValidator()

	// Six single-record batches: sequences 0..5, offsets 0..5.
	for seq := int32(0); seq < 6; seq++ {
		assertAppend(t, v, writeReq(1, 0, 0, seq, 1), int64(seq), int64(seq), false)
	}

	// The five most recent batches (sequences 1..5) still dedupe.
	assertAppend(t, v, writeReq(1, 0, 0, 1, 1), 1, 1, true)
	assertAppend(t, v, writeReq(1, 0, 0, 5, 1), 5, 5, true)

	// The oldest batch (sequence 0) has been evicted: it is now seen as
	// an out-of-order write rather than a retry.
	evicted := writeReq(1, 0, 0, 0, 1)
	_, err := v.Append(evicted)
	assertReject(t, err, ReasonOutOfOrder, evicted, 0)
}

func TestGapIsOutOfOrder(t *testing.T) {
	v := NewValidator()

	assertAppend(t, v, writeReq(1, 0, 0, 0, 2), 0, 1, false)

	// Jump from sequence 1 straight to 3.
	gap := writeReq(1, 0, 0, 3, 1)
	_, err := v.Append(gap)
	assertReject(t, err, ReasonOutOfOrder, gap, 3)

	// A rewrite attempt targeting an already used sequence with a batch
	// that is not contained in a cached interval is also out of order.
	rewrite := writeReq(1, 0, 0, 0, 3)
	_, err = v.Append(rewrite)
	assertReject(t, err, ReasonOutOfOrder, rewrite, 2)

	if got := v.LogEndOffset(0); got != 2 {
		t.Fatalf("log end offset = %d, want 2", got)
	}

	// Filling the real next sequence recovers the stream.
	assertAppend(t, v, writeReq(1, 0, 0, 2, 1), 2, 2, false)
}

func TestSequenceWrapsFromMaxInt32ToZero(t *testing.T) {
	v := NewValidator()

	// Inject a state whose last accepted sequence is MaxInt32 so the test
	// does not need to append 2 billion batches.
	key := producerKey{producerID: 3, partition: 0}
	v.mu.Lock()
	v.producers[key] = &producerState{
		known:   true,
		epoch:   0,
		hasLast: true,
		lastSeq: math.MaxInt32,
	}
	v.logEndOffset[0] = 100
	v.mu.Unlock()

	// The legal successor of MaxInt32 is 0, and offsets keep advancing.
	assertAppend(t, v, writeReq(3, 0, 0, 0, 2), 100, 101, false)

	// Retry of the wrap-around batch returns the same offsets.
	assertAppend(t, v, writeReq(3, 0, 0, 0, 2), 100, 101, true)

	// After wrap, ordinary progression continues from 0.
	assertAppend(t, v, writeReq(3, 0, 0, 2, 1), 102, 102, false)

	if got := nextSequence(math.MaxInt32); got != 0 {
		t.Fatalf("nextSequence(MaxInt32) = %d, want 0", got)
	}
	if got := nextSequence(0); got != 1 {
		t.Fatalf("nextSequence(0) = %d, want 1", got)
	}
}

func TestBatchCrossingWrapBoundaryIsInvalid(t *testing.T) {
	v := NewValidator()

	crossing := writeReq(1, 0, 0, math.MaxInt32, 2)
	_, err := v.Append(crossing)
	assertReject(t, err, ReasonInvalidBatch, crossing, 0)

	// Register the producer at sequence 0 first.
	assertAppend(t, v, writeReq(1, 0, 0, 0, 1), 0, 0, false)

	// A single record exactly at MaxInt32 is legal; the next batch wraps.
	maxReq := writeReq(1, 0, 0, math.MaxInt32, 1)
	_, err = v.Append(maxReq)
	assertReject(t, err, ReasonOutOfOrder, maxReq, math.MaxInt32)

	// Reaching MaxInt32 through the normal progression makes it legal,
	// and the following batch wraps back to sequence 0.
	v.mu.Lock()
	v.producers[producerKey{producerID: 1, partition: 0}].lastSeq = math.MaxInt32 - 1
	v.mu.Unlock()
	assertAppend(t, v, writeReq(1, 0, 0, math.MaxInt32, 1), 1, 1, false)
	assertAppend(t, v, writeReq(1, 0, 0, 0, 1), 2, 2, false)
}

func TestTransactionalIDBinding(t *testing.T) {
	v := NewValidator()
	v.BindTransactionalID("txn-A", 100)

	if producerID, ok := v.BoundProducer("txn-A"); !ok || producerID != 100 {
		t.Fatalf("BoundProducer = (%d, %v), want (100, true)", producerID, ok)
	}

	// Bound producer can write transactionally.
	req := writeReq(100, 0, 0, 0, 1)
	req.TransactionalID = "txn-A"
	assertAppend(t, v, req, 0, 0, false)

	// A thief with a different producerId cannot use the transactional id.
	thief := writeReq(200, 0, 0, 0, 1)
	thief.TransactionalID = "txn-A"
	_, err := v.Append(thief)
	assertReject(t, err, ReasonTransactionMismatch, thief, 0)

	// An unbound transactional id is rejected outright.
	unknown := writeReq(100, 0, 0, 1, 1)
	unknown.TransactionalID = "txn-ghost"
	_, err = v.Append(unknown)
	assertReject(t, err, ReasonUnknownTransaction, unknown, 1)

	// Non-transactional writes from the same producer are unaffected.
	assertAppend(t, v, writeReq(100, 0, 0, 1, 1), 1, 1, false)

	// Rebinding fences the old producer, coordinator style.
	v.BindTransactionalID("txn-A", 300)
	old := writeReq(100, 0, 0, 2, 1)
	old.TransactionalID = "txn-A"
	_, err = v.Append(old)
	assertReject(t, err, ReasonTransactionMismatch, old, 2)

	newOwner := writeReq(300, 0, 0, 0, 1)
	newOwner.TransactionalID = "txn-A"
	assertAppend(t, v, newOwner, 2, 2, false)
}

func TestInvalidRequestsAndSeedOption(t *testing.T) {
	v := NewValidator(WithInitialLogEndOffset(2, 50))

	if got := v.LogEndOffset(2); got != 50 {
		t.Fatalf("seeded log end offset = %d, want 50", got)
	}

	assertAppend(t, v, writeReq(1, 2, 0, 0, 1), 50, 50, false)

	zeroCount := writeReq(1, 2, 0, 1, 0)
	_, err := v.Append(zeroCount)
	assertReject(t, err, ReasonInvalidBatch, zeroCount, 0)

	negativeCount := writeReq(1, 2, 0, 1, -1)
	_, err = v.Append(negativeCount)
	assertReject(t, err, ReasonInvalidBatch, negativeCount, 0)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic for negative seeded offset")
			}
		}()
		NewValidator(WithInitialLogEndOffset(1, -1))
	}()
}

func TestConcurrentAppendsAreSerialized(t *testing.T) {
	v := NewValidator()

	const goroutines = 16
	const perGoroutine = 25
	var wg sync.WaitGroup

	// Each goroutine owns a distinct (producer, partition) pair so every
	// append is valid; the test exercises locking and offset accounting.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(producerID int64) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := v.Append(writeReq(producerID, 0, 0, int32(i), 1)); err != nil {
					t.Errorf("producer %d append %d failed: %v", producerID, i, err)
					return
				}
			}
		}(int64(g + 1))
	}
	wg.Wait()

	if got := v.LogEndOffset(0); got != goroutines*perGoroutine {
		t.Fatalf("log end offset = %d, want %d", got, goroutines*perGoroutine)
	}
}

func TestBatchLastSequence(t *testing.T) {
	tests := []struct {
		name   string
		first  int32
		count  int32
		want   int32
		wantOK bool
	}{
		{name: "single", first: 0, count: 1, want: 0, wantOK: true},
		{name: "range", first: 10, count: 5, want: 14, wantOK: true},
		{name: "ends at max", first: math.MaxInt32 - 1, count: 2, want: math.MaxInt32, wantOK: true},
		{name: "wraps", first: math.MaxInt32, count: 2, want: 0, wantOK: false},
		{name: "overflows", first: math.MaxInt32, count: math.MaxInt32, want: 0, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := batchLastSequence(tc.first, tc.count)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Fatalf("batchLastSequence(%d, %d) = (%d, %v), want (%d, %v)",
					tc.first, tc.count, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
