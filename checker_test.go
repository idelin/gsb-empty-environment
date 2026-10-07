package boundedq

import "testing"

func enq(v int, err error, call, ret int64) Op {
	return Op{Kind: EnqueueOp, Value: v, Err: err, Call: call, Ret: ret}
}

func deq(v int, err error, call, ret int64) Op {
	return Op{Kind: DequeueOp, Value: v, Err: err, Call: call, Ret: ret}
}

func deqClosed(call, ret int64) Op {
	return Op{Kind: DequeueOp, Err: ErrClosed, Call: call, Ret: ret}
}

func cls(call, ret int64) Op {
	return Op{Kind: CloseOp, Call: call, Ret: ret}
}

func TestCheckerValidSequential(t *testing.T) {
	ops := []Op{
		enq(1, nil, 1, 2),
		enq(2, nil, 3, 4),
		deq(1, nil, 5, 6),
		cls(7, 8),
		deq(2, nil, 9, 10),
		deqClosed(11, 12),
	}
	order, ok := CheckLinearizable(2, ops)
	if !ok {
		t.Fatal("valid history rejected")
	}
	// The witness itself must be a legal sequential execution.
	if len(order) != len(ops) {
		t.Fatalf("witness length = %d, want %d", len(order), len(ops))
	}
}

func TestCheckerEnqueueRacingClose(t *testing.T) {
	// Enqueue is invoked before Close but returns ErrClosed after Close
	// returns: legal, since the enqueue can serialize after the close.
	ops := []Op{
		enq(1, ErrClosed, 1, 6),
		cls(3, 4),
	}
	if _, ok := CheckLinearizable(1, ops); !ok {
		t.Fatal("enqueue racing close with ErrClosed should be linearizable")
	}
}

func TestCheckerEnqueueCommittedBeforeClose(t *testing.T) {
	// Enqueue commits before Close; the element stays drainable.
	ops := []Op{
		enq(1, nil, 1, 2),
		cls(3, 4),
		deq(1, nil, 5, 6),
		deqClosed(7, 8),
	}
	if _, ok := CheckLinearizable(1, ops); !ok {
		t.Fatal("committed enqueue drained after close should be linearizable")
	}
}

func TestCheckerRejectsEnqueueAfterClose(t *testing.T) {
	// Close completes before the enqueue is even invoked, yet the enqueue
	// succeeds: no legal serialization.
	ops := []Op{
		cls(1, 2),
		enq(1, nil, 3, 4),
	}
	if _, ok := CheckLinearizable(1, ops); ok {
		t.Fatal("successful enqueue after completed close accepted")
	}
}

func TestCheckerRejectsPrematureTermination(t *testing.T) {
	// Dequeue reports closed-and-drained while a committed element is
	// still in the queue.
	ops := []Op{
		enq(1, nil, 1, 2),
		cls(3, 4),
		deqClosed(5, 6),
	}
	if _, ok := CheckLinearizable(1, ops); ok {
		t.Fatal("termination before drain accepted")
	}
}

func TestCheckerRejectsPhantomElement(t *testing.T) {
	ops := []Op{
		enq(1, nil, 1, 2),
		deq(2, nil, 3, 4),
	}
	if _, ok := CheckLinearizable(1, ops); ok {
		t.Fatal("dequeue of never-enqueued value accepted")
	}
}

func TestCheckerRejectsFIFOViolation(t *testing.T) {
	ops := []Op{
		enq(1, nil, 1, 2),
		enq(2, nil, 3, 4),
		deq(2, nil, 5, 6),
	}
	if _, ok := CheckLinearizable(2, ops); ok {
		t.Fatal("out-of-order dequeue accepted")
	}
}

func TestCheckerRejectsCapacityOverflow(t *testing.T) {
	// Capacity 1, two sequential successful enqueues with no dequeue.
	ops := []Op{
		enq(1, nil, 1, 2),
		enq(2, nil, 3, 4),
	}
	if _, ok := CheckLinearizable(1, ops); ok {
		t.Fatal("enqueue beyond capacity accepted")
	}
}

func TestCheckerCapacityFreedByConcurrentDequeue(t *testing.T) {
	// Capacity 1: the second enqueue overlaps a dequeue, so it can
	// legally succeed after the dequeue frees the slot.
	ops := []Op{
		enq(1, nil, 1, 2),
		deq(1, nil, 3, 6),
		enq(2, nil, 4, 8),
		deq(2, nil, 9, 10),
	}
	if _, ok := CheckLinearizable(1, ops); !ok {
		t.Fatal("enqueue overlapping dequeue should be linearizable")
	}
}

func TestCheckerIdempotentClose(t *testing.T) {
	ops := []Op{
		cls(1, 2),
		cls(3, 4),
		deqClosed(5, 6),
	}
	if _, ok := CheckLinearizable(1, ops); !ok {
		t.Fatal("idempotent close should be linearizable")
	}
}

func TestCheckerEmptyHistory(t *testing.T) {
	if _, ok := CheckLinearizable(1, nil); !ok {
		t.Fatal("empty history should be linearizable")
	}
}
