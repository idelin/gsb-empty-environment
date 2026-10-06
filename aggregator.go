// Package agg implements a reset-barrier aggregator over labeled events.
//
// Multiple producer partitions emit events tagged with a generation label.
// A reset barrier from a partition closes that partition's contribution to
// the current generation: events before the barrier count toward the old
// generation, events after it toward the new one. Partitions reach the
// barrier out of order; post-barrier events are buffered per partition.
// The final value of a generation is published only once every expected
// partition has arrived at the barrier.
//
// Correctness for duplicate barriers, partition exit and recovery rests on
// the event-label model: every event carries an immutable (partition, gen,
// seq) label and every barrier carries a (partition, gen) label, so the
// aggregator is a pure function of the delivered label stream and replays
// are idempotent.
//
// Delivery assumptions (standard for log-based producers):
//   - per-partition delivery is FIFO;
//   - a partition is never more than one barrier ahead of the aggregator,
//     i.e. it emits barrier g+1 only after generation g has closed.
package agg

import (
	"fmt"
	"sync"
)

// Event is a single labeled data point from a producer partition.
//
// Seq is a per-partition, monotonically increasing sequence number (like a
// log offset); together with Partition it forms a unique event label that
// survives replays, which is what makes recovery deduplication possible.
type Event struct {
	Partition string
	Gen       int
	Seq       int
	Value     int64
}

// Publication is the final value of a closed generation.
type Publication struct {
	Gen   int
	Total int64
}

// ProtocolError reports an event whose label is inconsistent with the
// delivering partition's barrier position, e.g. a generation+1 event
// arriving before the partition's barrier.
type ProtocolError struct {
	Partition string
	Crossed   int // generation the partition currently lives in
	EventGen  int
	Seq       int
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("agg: protocol violation: partition %q is in generation %d but event seq=%d is labeled gen=%d",
		e.Partition, e.Crossed, e.Seq, e.EventGen)
}

// label is the unique identity of an event: (partition, seq).
type label struct {
	partition string
	seq       int
}

// Aggregator folds labeled events into per-generation totals.
type Aggregator struct {
	mu       sync.Mutex
	expected map[string]struct{}
	crossed  map[string]int // partition -> generation it currently lives in
	seen     map[label]struct{}
	gen      int              // currently open generation
	oldSum   int64            // pre-barrier values for gen
	newSum   map[string]int64 // buffered post-barrier values, per partition
	pubs     []Publication
}

// NewAggregator creates an Aggregator in generation 0 expecting barriers
// from the given partitions.
func NewAggregator(partitions ...string) *Aggregator {
	a := &Aggregator{
		expected: make(map[string]struct{}, len(partitions)),
		crossed:  make(map[string]int, len(partitions)),
		seen:     make(map[label]struct{}),
		newSum:   make(map[string]int64),
	}
	for _, p := range partitions {
		a.expected[p] = struct{}{}
		a.crossed[p] = 0
	}
	return a
}

// OnEvent consumes one labeled event.
//
// Events from unknown or exited partitions are dropped. Replayed or
// redelivered events are deduplicated by label. A label inconsistent with
// the partition's barrier position yields a *ProtocolError and is not
// counted.
func (a *Aggregator) OnEvent(e Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.expected[e.Partition]; !ok {
		return nil // unknown or exited partition: drop
	}
	l := label{e.Partition, e.Seq}
	if _, dup := a.seen[l]; dup {
		return nil // replay or redelivery: deduplicated by label
	}
	if e.Gen != a.crossed[e.Partition] {
		return &ProtocolError{
			Partition: e.Partition,
			Crossed:   a.crossed[e.Partition],
			EventGen:  e.Gen,
			Seq:       e.Seq,
		}
	}
	a.seen[l] = struct{}{}
	if e.Gen == a.gen {
		a.oldSum += e.Value
	} else {
		// The partition already crossed the barrier for a.gen; buffer
		// its post-barrier values for the next generation.
		a.newSum[e.Partition] += e.Value
	}
	return nil
}

// OnBarrier records a partition's arrival at the reset barrier for gen.
//
// Only a barrier for the currently open generation from a partition that
// has not crossed yet has an effect. Duplicates (already crossed), stale
// barriers (gen < current) and barriers skipping ahead (gen > current) are
// idempotent no-ops, which is what makes barrier retransmission after
// recovery safe.
func (a *Aggregator) OnBarrier(partition string, gen int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.expected[partition]; !ok {
		return // unknown or exited partition
	}
	if gen != a.gen || a.crossed[partition] != gen {
		return
	}
	a.crossed[partition] = gen + 1
	a.maybeCloseLocked()
}

// RemovePartition makes a partition exit the expected set.
//
// Values already folded into the open generation stay counted: they were
// consumed while the partition was a member. Buffered post-barrier values
// are discarded with the partition. If the exit completes the barrier set
// of the open generation, the generation closes immediately.
func (a *Aggregator) RemovePartition(partition string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.expected[partition]; !ok {
		return
	}
	delete(a.expected, partition)
	delete(a.crossed, partition)
	delete(a.newSum, partition)
	a.maybeCloseLocked()
}

// AddPartition (re)joins a partition in the currently open generation; it
// is expected to report a barrier for that generation like everyone else.
func (a *Aggregator) AddPartition(partition string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.expected[partition]; ok {
		return
	}
	a.expected[partition] = struct{}{}
	a.crossed[partition] = a.gen
}

// Publications returns the final values published so far, in order.
func (a *Aggregator) Publications() []Publication {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Publication(nil), a.pubs...)
}

// Gen returns the currently open generation.
func (a *Aggregator) Gen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gen
}

// maybeCloseLocked publishes the open generation's final value once every
// expected partition has crossed its barrier, then rotates generations:
// buffered post-barrier values become the base of the new generation.
func (a *Aggregator) maybeCloseLocked() {
	if len(a.expected) == 0 {
		return
	}
	for p := range a.expected {
		if a.crossed[p] != a.gen+1 {
			return
		}
	}
	a.pubs = append(a.pubs, Publication{Gen: a.gen, Total: a.oldSum})
	a.gen++
	a.oldSum = 0
	for _, s := range a.newSum {
		a.oldSum += s
	}
	a.newSum = make(map[string]int64)
}
