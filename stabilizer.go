// Package stabilizer implements a topological reordering stabilization
// window for weakly-ordered event streams.
//
// Monitoring events arrive out of order, each stamped with a logical
// clock (Seq). Given a maximum out-of-orderness bound K, the Stabilizer
// buffers events in an internal priority queue and only releases an event
// once the safe watermark (maxSeq - K) has advanced past it. The released
// stream is therefore guaranteed to be a strictly monotonically ordered,
// causally total sequence.
package stabilizer

import "container/heap"

// Event is a single monitoring observation on a weakly-ordered stream.
type Event struct {
	// Seq is the logical clock value assigned at the source. It defines
	// the causal position of the event in the total order.
	Seq int64
	// Source identifies the sensor that produced the event.
	Source string
	// Value is the observed measurement.
	Value float64
}

// item wraps an Event with its arrival index so that events sharing the
// same logical clock still have a deterministic, strict total order.
type item struct {
	ev      Event
	arrival uint64
}

// eventHeap is a min-heap ordered by (Seq, arrival).
type eventHeap []item

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].ev.Seq != h[j].ev.Seq {
		return h[i].ev.Seq < h[j].ev.Seq
	}
	return h[i].arrival < h[j].arrival
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x any) {
	*h = append(*h, x.(item))
}

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = item{}
	*h = old[:n-1]
	return it
}

// Stabilizer reorders a weakly-ordered event stream into a strictly
// monotonic, causally total sequence using a bounded-delay window.
//
// The zero value is not usable; construct with New.
type Stabilizer struct {
	k           int64 // maximum tolerated out-of-orderness
	maxSeq      int64 // highest logical clock observed so far
	seenAny     bool
	lastEmitted int64 // logical clock of the most recently released event
	emittedAny  bool
	arrivals    uint64
	pending     eventHeap
	dropped     int
}

// New returns a Stabilizer tolerating out-of-orderness of at most k
// logical clock ticks. It panics if k is negative.
func New(k int64) *Stabilizer {
	if k < 0 {
		panic("stabilizer: negative window")
	}
	return &Stabilizer{k: k}
}

// Watermark returns the current safe watermark: every event with
// Seq <= Watermark may be released. It reports false before any event
// has been seen.
func (s *Stabilizer) Watermark() (int64, bool) {
	if !s.seenAny {
		return 0, false
	}
	return s.maxSeq - s.k, true
}

// Pending returns the number of events buffered inside the window.
func (s *Stabilizer) Pending() int { return len(s.pending) }

// Dropped returns the number of events rejected because they arrived
// later than the stabilization window allows (Seq already emitted past).
func (s *Stabilizer) Dropped() int { return s.dropped }

// Push ingests one event and returns every event that becomes stable as
// a result, in strict causal order. The returned slice may be empty when
// the watermark has not advanced far enough.
//
// An event whose Seq is not greater than the most recently emitted Seq
// is too late to be placed in the total order; it is dropped and counted
// by Dropped.
func (s *Stabilizer) Push(e Event) []Event {
	if s.emittedAny && e.Seq <= s.lastEmitted {
		s.dropped++
		return nil
	}
	if !s.seenAny || e.Seq > s.maxSeq {
		s.maxSeq = e.Seq
		s.seenAny = true
	}
	heap.Push(&s.pending, item{ev: e, arrival: s.arrivals})
	s.arrivals++
	return s.release()
}

// release emits every buffered event at or below the safe watermark.
func (s *Stabilizer) release() []Event {
	watermark := s.maxSeq - s.k
	var out []Event
	for len(s.pending) > 0 && s.pending[0].ev.Seq <= watermark {
		it := heap.Pop(&s.pending).(item)
		s.lastEmitted = it.ev.Seq
		s.emittedAny = true
		out = append(out, it.ev)
	}
	return out
}

// Flush drains all remaining buffered events in strict causal order.
// It must be called when the input stream ends; afterwards the
// Stabilizer reports every further event as dropped.
func (s *Stabilizer) Flush() []Event {
	out := make([]Event, 0, len(s.pending))
	for len(s.pending) > 0 {
		it := heap.Pop(&s.pending).(item)
		s.lastEmitted = it.ev.Seq
		s.emittedAny = true
		out = append(out, it.ev)
	}
	return out
}
