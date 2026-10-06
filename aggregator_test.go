package agg

import (
	"reflect"
	"testing"
)

func ev(p string, gen, seq int, v int64) Event {
	return Event{Partition: p, Gen: gen, Seq: seq, Value: v}
}

func mustEvent(t *testing.T, a *Aggregator, e Event) {
	t.Helper()
	if err := a.OnEvent(e); err != nil {
		t.Fatalf("OnEvent(%+v): %v", e, err)
	}
}

func checkPubs(t *testing.T, a *Aggregator, want []Publication) {
	t.Helper()
	if got := a.Publications(); !reflect.DeepEqual(got, want) {
		t.Fatalf("publications = %+v, want %+v", got, want)
	}
}

// Partitions reach the barrier out of order; post-barrier events are
// buffered per partition and only become visible after rotation. The old
// generation's final value is published only when all partitions arrived.
func TestOutOfOrderBarrierAndBuffering(t *testing.T) {
	a := NewAggregator("A", "B", "C")

	// A finishes generation 0 early and keeps producing into gen 1.
	mustEvent(t, a, ev("A", 0, 1, 10))
	mustEvent(t, a, ev("A", 0, 2, 20))
	a.OnBarrier("A", 0)
	mustEvent(t, a, ev("A", 1, 3, 5)) // buffered, must not leak into gen 0

	mustEvent(t, a, ev("B", 0, 1, 1))
	a.OnBarrier("B", 0)
	checkPubs(t, a, nil) // C still missing: nothing published

	mustEvent(t, a, ev("C", 0, 1, 100))
	mustEvent(t, a, ev("C", 0, 2, 200))
	a.OnBarrier("C", 0) // completes the set: 10+20+1+100+200
	checkPubs(t, a, []Publication{{Gen: 0, Total: 331}})

	// Generation 1 starts from A's buffered 5.
	mustEvent(t, a, ev("A", 1, 4, 7))
	a.OnBarrier("A", 1)
	mustEvent(t, a, ev("A", 2, 5, 9)) // buffered for gen 2
	mustEvent(t, a, ev("B", 1, 2, 3))
	a.OnBarrier("B", 1)
	mustEvent(t, a, ev("C", 1, 3, 4))
	a.OnBarrier("C", 1) // 5 (buffered) + 7 + 3 + 4
	checkPubs(t, a, []Publication{{Gen: 0, Total: 331}, {Gen: 1, Total: 19}})

	a.OnBarrier("A", 2)
	a.OnBarrier("B", 2)
	a.OnBarrier("C", 2) // only A's buffered 9
	checkPubs(t, a, []Publication{{Gen: 0, Total: 331}, {Gen: 1, Total: 19}, {Gen: 2, Total: 9}})
}

// Duplicate and stale barriers are idempotent: a generation is published
// exactly once.
func TestDuplicateAndStaleBarriers(t *testing.T) {
	a := NewAggregator("A", "B")

	mustEvent(t, a, ev("A", 0, 1, 10))
	mustEvent(t, a, ev("A", 0, 1, 10)) // duplicate event label: deduplicated
	a.OnBarrier("A", 0)
	a.OnBarrier("A", 0) // duplicate barrier
	a.OnBarrier("A", 0) // duplicate barrier
	checkPubs(t, a, nil)

	mustEvent(t, a, ev("B", 0, 1, 20))
	a.OnBarrier("B", 0)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 30}})

	a.OnBarrier("A", 0) // stale: gen 0 already closed
	a.OnBarrier("B", 0) // stale
	a.OnBarrier("A", 2) // skips ahead of the open generation: ignored
	checkPubs(t, a, []Publication{{Gen: 0, Total: 30}})

	a.OnBarrier("A", 1)
	a.OnBarrier("B", 1)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 30}, {Gen: 1, Total: 0}})
}

// A partition exiting before its barrier stops blocking the close; values
// it contributed while a member stay counted, and its late messages are
// ignored.
func TestPartitionExitBeforeBarrier(t *testing.T) {
	a := NewAggregator("A", "B", "C")

	mustEvent(t, a, ev("A", 0, 1, 10))
	a.OnBarrier("A", 0)
	mustEvent(t, a, ev("C", 0, 1, 5))
	mustEvent(t, a, ev("C", 0, 2, 7))
	a.RemovePartition("C") // C exits before its barrier
	checkPubs(t, a, nil)   // B still pending

	mustEvent(t, a, ev("B", 0, 1, 20))
	a.OnBarrier("B", 0) // 10 + 5 + 7 + 20
	checkPubs(t, a, []Publication{{Gen: 0, Total: 42}})

	// Late messages from the exited partition are ignored.
	mustEvent(t, a, ev("C", 1, 3, 100))
	a.OnBarrier("C", 1)

	mustEvent(t, a, ev("A", 1, 2, 1))
	a.OnBarrier("A", 1)
	mustEvent(t, a, ev("B", 1, 2, 2))
	a.OnBarrier("B", 1)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 42}, {Gen: 1, Total: 3}})
}

// A partition exiting after it crossed keeps its pre-barrier contribution
// in the closing generation but loses its buffered post-barrier values.
func TestPartitionExitAfterCrossing(t *testing.T) {
	a := NewAggregator("A", "B")

	mustEvent(t, a, ev("A", 0, 1, 10))
	a.OnBarrier("A", 0)
	mustEvent(t, a, ev("A", 1, 2, 5)) // buffered for gen 1
	a.RemovePartition("A")

	mustEvent(t, a, ev("B", 0, 1, 20))
	a.OnBarrier("B", 0) // A's pre-barrier 10 stays counted
	checkPubs(t, a, []Publication{{Gen: 0, Total: 30}})

	mustEvent(t, a, ev("B", 1, 2, 7))
	a.OnBarrier("B", 1) // A's buffered 5 was discarded with the exit
	checkPubs(t, a, []Publication{{Gen: 0, Total: 30}, {Gen: 1, Total: 7}})
}

// Removing the last laggard completes the barrier set and closes the
// generation immediately.
func TestPartitionExitCompletesBarrier(t *testing.T) {
	a := NewAggregator("A", "B", "C")

	a.OnBarrier("A", 0)
	a.OnBarrier("B", 0)
	checkPubs(t, a, nil)

	a.RemovePartition("C")
	checkPubs(t, a, []Publication{{Gen: 0, Total: 0}})
}

// Recovery: a crashed partition replays events from its checkpoint and
// resends its barrier. Labels make every replay idempotent, even across a
// generation rotation.
func TestRecoveryReplay(t *testing.T) {
	a := NewAggregator("A", "B")

	mustEvent(t, a, ev("A", 0, 1, 10))
	mustEvent(t, a, ev("A", 0, 2, 20))

	// A crashes and recovers: replays its log, then continues.
	mustEvent(t, a, ev("A", 0, 1, 10)) // replay: deduplicated
	mustEvent(t, a, ev("A", 0, 2, 20)) // replay: deduplicated
	mustEvent(t, a, ev("A", 0, 3, 30))
	a.OnBarrier("A", 0)

	// A crashes again after crossing: resends barrier and last events.
	a.OnBarrier("A", 0)
	mustEvent(t, a, ev("A", 0, 3, 30)) // replay: deduplicated

	mustEvent(t, a, ev("B", 0, 1, 1))
	a.OnBarrier("B", 0) // 10 + 20 + 30 + 1, each exactly once
	checkPubs(t, a, []Publication{{Gen: 0, Total: 61}})

	// A very late replay arriving after the rotation is still deduplicated.
	mustEvent(t, a, ev("A", 0, 1, 10))
	a.OnBarrier("A", 1)
	a.OnBarrier("B", 1)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 61}, {Gen: 1, Total: 0}})
}

// An event labeled with a generation the partition has not reached (or has
// left) is a protocol violation: rejected and not counted.
func TestProtocolViolation(t *testing.T) {
	a := NewAggregator("A")

	if err := a.OnEvent(ev("A", 1, 1, 5)); err == nil {
		t.Fatal("gen+1 event before barrier: want ProtocolError, got nil")
	} else if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("want *ProtocolError, got %T", err)
	}
	if err := a.OnEvent(ev("A", 5, 2, 5)); err == nil {
		t.Fatal("far-future event: want ProtocolError, got nil")
	}
	checkPubs(t, a, nil)

	// After the barrier, the same labels become valid.
	a.OnBarrier("A", 0)
	mustEvent(t, a, ev("A", 1, 1, 5))
	a.OnBarrier("A", 1)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 0}, {Gen: 1, Total: 5}})
}

// A partition joining mid-generation is expected at the current barrier.
func TestJoinMidGeneration(t *testing.T) {
	a := NewAggregator("A")

	mustEvent(t, a, ev("A", 0, 1, 10))
	a.OnBarrier("A", 0)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 10}})

	a.AddPartition("B")
	mustEvent(t, a, ev("A", 1, 2, 1))
	a.OnBarrier("A", 1)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 10}}) // B pending

	mustEvent(t, a, ev("B", 1, 1, 100))
	a.OnBarrier("B", 1)
	checkPubs(t, a, []Publication{{Gen: 0, Total: 10}, {Gen: 1, Total: 101}})
}
