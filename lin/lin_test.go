package lin_test

import (
	"errors"
	"sort"
	"testing"

	"bq/lin"
)

func op(kind lin.Kind, arg, val int, st lin.Status, call, ret int64) lin.Op {
	return lin.Op{Kind: kind, Arg: arg, Val: val, Status: st, Call: call, Ret: ret}
}

func enq(v int, st lin.Status, call, ret int64) lin.Op { return op(lin.Enqueue, v, 0, st, call, ret) }
func deq(v int, st lin.Status, call, ret int64) lin.Op { return op(lin.Dequeue, 0, v, st, call, ret) }
func cls(call, ret int64) lin.Op                       { return op(lin.Close, 0, 0, lin.OK, call, ret) }

func mustLinearizable(t *testing.T, capacity int, ops []lin.Op) []int {
	t.Helper()
	order, ok, err := lin.Check(capacity, ops)
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("history should be linearizable, got no serial order")
	}
	return order
}

func mustNotLinearizable(t *testing.T, capacity int, ops []lin.Op) {
	t.Helper()
	if order, ok, err := lin.Check(capacity, ops); err != nil || ok {
		t.Fatalf("history should not be linearizable, got ok=%v order=%v err=%v", ok, order, err)
	}
}

func TestSequentialHistory(t *testing.T) {
	ops := []lin.Op{
		enq(1, lin.OK, 1, 2),
		enq(2, lin.OK, 3, 4),
		deq(1, lin.OK, 5, 6),
		cls(7, 8),
		deq(2, lin.OK, 9, 10),      // committed before close: drainable
		deq(0, lin.Closed, 11, 12), // drained: termination
		enq(3, lin.Closed, 13, 14), // uncommitted at close: rejected
	}
	mustLinearizable(t, 2, ops)
}

func TestRejectsPhantomDequeue(t *testing.T) {
	mustNotLinearizable(t, 2, []lin.Op{deq(7, lin.OK, 1, 2)})
}

func TestRejectsFIFOViolation(t *testing.T) {
	ops := []lin.Op{
		enq(1, lin.OK, 1, 2),
		enq(2, lin.OK, 3, 4),
		deq(2, lin.OK, 5, 6), // 1 was enqueued first
	}
	mustNotLinearizable(t, 2, ops)
}

func TestConcurrentOpsMayReorder(t *testing.T) {
	// Capacity 1: A is invoked first but its interval overlaps everyone.
	// A cannot commit before B (the slot would still be occupied during
	// B's whole interval), so the only legal serial order is B, C, A, D.
	ops := []lin.Op{
		enq(1, lin.OK, 1, 8), // A
		enq(2, lin.OK, 2, 3), // B
		deq(2, lin.OK, 4, 5), // C
		deq(1, lin.OK, 6, 7), // D
	}
	order := mustLinearizable(t, 1, ops)
	if len(order) != 4 {
		t.Fatalf("order length = %d, want 4", len(order))
	}
	// Real-time order B<C<D must be preserved in the witness.
	pos := make(map[int]int)
	for i, idx := range order {
		pos[idx] = i
	}
	if !(pos[1] < pos[2] && pos[2] < pos[3]) {
		t.Fatalf("witness %v violates real-time order", order)
	}
	if pos[0] < pos[2] {
		t.Fatalf("witness %v: A must linearize after C (dequeue of 2)", order)
	}
}

func TestEnqueueCommittedBeforeCloseDrains(t *testing.T) {
	ops := []lin.Op{
		enq(1, lin.OK, 1, 2),
		cls(3, 4),
		deq(1, lin.OK, 5, 6),
		deq(0, lin.Closed, 7, 8),
	}
	mustLinearizable(t, 1, ops)
}

func TestEnqueueAfterCloseMustFail(t *testing.T) {
	// An Enqueue that reports OK entirely after Close returned is illegal.
	mustNotLinearizable(t, 1, []lin.Op{
		cls(1, 2),
		enq(1, lin.OK, 3, 4),
	})
	// Reporting ErrClosed instead is the legal outcome.
	mustLinearizable(t, 1, []lin.Op{
		cls(1, 2),
		enq(1, lin.Closed, 3, 4),
	})
}

func TestTerminationRequiresDrained(t *testing.T) {
	// Dequeue reporting ErrClosed while a committed item remains is illegal.
	mustNotLinearizable(t, 1, []lin.Op{
		enq(1, lin.OK, 1, 2),
		cls(3, 4),
		deq(0, lin.Closed, 5, 6),
	})
}

func TestFullQueueBlocksEnqueue(t *testing.T) {
	// Capacity 1, no dequeue: a second Enqueue cannot report OK.
	mustNotLinearizable(t, 1, []lin.Op{
		enq(1, lin.OK, 1, 2),
		enq(2, lin.OK, 3, 4),
	})
	// With a dequeue in between it is legal.
	mustLinearizable(t, 1, []lin.Op{
		enq(1, lin.OK, 1, 2),
		deq(1, lin.OK, 3, 4),
		enq(2, lin.OK, 5, 6),
	})
}

func TestCanceledOpsHaveNoEffect(t *testing.T) {
	// Cancelled enqueue must not commit: dequeuing its value is illegal.
	mustNotLinearizable(t, 1, []lin.Op{
		enq(1, lin.OK, 1, 2),
		enq(2, lin.Canceled, 3, 4),
		deq(2, lin.OK, 5, 6),
	})
	// But cancelling while full is fine, and the slot is not leaked.
	mustLinearizable(t, 1, []lin.Op{
		enq(1, lin.OK, 1, 2),
		enq(2, lin.Canceled, 3, 4),
		deq(1, lin.OK, 5, 6),
		enq(3, lin.OK, 7, 8),
		cls(9, 10),
		deq(3, lin.OK, 11, 12),
		deq(0, lin.Closed, 13, 14),
	})
	// Cancelled dequeue must not consume an item.
	mustNotLinearizable(t, 1, []lin.Op{
		enq(1, lin.OK, 1, 2),
		deq(0, lin.Canceled, 3, 4),
		cls(5, 6),
		deq(0, lin.Closed, 7, 8), // item 1 was never dequeued
	})
}

func TestDuplicateValuesTrackedSeparately(t *testing.T) {
	ops := []lin.Op{
		enq(5, lin.OK, 1, 2),
		enq(5, lin.OK, 3, 4),
		deq(5, lin.OK, 5, 6),
		deq(5, lin.OK, 7, 8),
	}
	mustLinearizable(t, 2, ops)
	// A third dequeue of 5 has no matching enqueue.
	mustNotLinearizable(t, 2, append(append([]lin.Op(nil), ops...), deq(5, lin.OK, 9, 10)))
}

func TestCheckRejectsOversizedHistory(t *testing.T) {
	ops := make([]lin.Op, lin.MaxOps+1)
	for i := range ops {
		ops[i] = cls(int64(2*i+1), int64(2*i+2))
	}
	if _, _, err := lin.Check(1, ops); !errors.Is(err, lin.ErrTooManyOps) {
		t.Fatalf("got %v, want ErrTooManyOps", err)
	}
}

func TestWitnessIsPermutation(t *testing.T) {
	ops := []lin.Op{
		enq(1, lin.OK, 1, 10),
		enq(2, lin.OK, 2, 3),
		deq(2, lin.OK, 4, 5),
		deq(1, lin.OK, 6, 7),
		cls(8, 9),
		deq(0, lin.Closed, 11, 12),
	}
	order := mustLinearizable(t, 2, ops)
	sorted := append([]int(nil), order...)
	sort.Ints(sorted)
	for i, v := range sorted {
		if v != i {
			t.Fatalf("order %v is not a permutation of 0..%d", order, len(ops)-1)
		}
	}
	// Real-time: every op with Ret < another's Call must precede it.
	pos := make(map[int]int)
	for i, idx := range order {
		pos[idx] = i
	}
	for i, a := range ops {
		for j, b := range ops {
			if a.Ret < b.Call && pos[i] > pos[j] {
				t.Fatalf("op %d must precede op %d in %v", i, j, order)
			}
		}
	}
}
