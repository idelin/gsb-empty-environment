// Package streamagg implements a partitioned stream aggregator whose global
// event-time watermark is driven by the minimum watermark of the active
// partitions. Partitions explicitly marked idle are excluded from the
// minimum; when every partition is idle the global watermark keeps its
// previous value. Events arriving behind the global watermark (for example
// from a resumed partition) are diverted to a late channel and never move
// the global watermark backwards.
package streamagg

import "sync"

// DefaultLateCapacity is the buffer size of the late-event channel when the
// aggregator is created with NewAggregator.
const DefaultLateCapacity = 1024

// Event is a single record carrying an event-time timestamp.
type Event struct {
	Partition string
	Timestamp int64
	Value     float64
}

type partitionState struct {
	watermark int64
	set       bool // whether watermark has been initialised by an event
	idle      bool
}

// Aggregator tracks per-partition watermarks and derives a monotonically
// non-decreasing global watermark.
type Aggregator struct {
	mu         sync.Mutex
	partitions map[string]*partitionState
	global     int64
	hasGlobal  bool
	late       chan Event
	overflow   []Event // late events that did not fit into the channel buffer
}

// NewAggregator returns an aggregator with the default late-channel capacity.
func NewAggregator() *Aggregator {
	return NewAggregatorWithLateCapacity(DefaultLateCapacity)
}

// NewAggregatorWithLateCapacity returns an aggregator whose late channel is
// buffered with the given capacity.
func NewAggregatorWithLateCapacity(lateCapacity int) *Aggregator {
	return &Aggregator{
		partitions: make(map[string]*partitionState),
		late:       make(chan Event, lateCapacity),
	}
}

// OnEvent ingests one event. Events whose timestamp is behind the current
// global watermark are diverted to the late channel and leave all watermarks
// untouched. Otherwise the owning partition's watermark advances (never
// regresses) and the global watermark is recomputed.
func (a *Aggregator) OnEvent(e Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.hasGlobal && e.Timestamp < a.global {
		a.emitLate(e)
		return
	}

	p := a.partition(e.Partition)
	if !p.set || e.Timestamp > p.watermark {
		p.watermark = e.Timestamp
		p.set = true
	}
	a.recomputeLocked()
}

// MarkIdle explicitly marks a partition idle, excluding it from the global
// minimum. Unknown partitions are registered as idle.
func (a *Aggregator) MarkIdle(partition string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.partition(partition).idle = true
	a.recomputeLocked()
}

// MarkActive resumes a previously idled partition. The global watermark is
// recomputed but can never move backwards, so resuming a lagging partition
// does not lower it; that partition's subsequent events behind the global
// watermark are diverted to the late channel.
func (a *Aggregator) MarkActive(partition string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.partition(partition).idle = false
	a.recomputeLocked()
}

// IsIdle reports whether the named partition is currently marked idle.
func (a *Aggregator) IsIdle(partition string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.partitions[partition]
	return ok && p.idle
}

// GlobalWatermark returns the current global watermark and whether it has
// been established yet.
func (a *Aggregator) GlobalWatermark() (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.global, a.hasGlobal
}

// Late returns the channel receiving diverted late events.
func (a *Aggregator) Late() <-chan Event {
	return a.late
}

// DrainLate returns the late events buffered so far (channel buffer first,
// then any overflow) without blocking.
func (a *Aggregator) DrainLate() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Event
	for {
		select {
		case e := <-a.late:
			out = append(out, e)
		default:
			out = append(out, a.overflow...)
			a.overflow = nil
			return out
		}
	}
}

func (a *Aggregator) emitLate(e Event) {
	select {
	case a.late <- e:
	default:
		a.overflow = append(a.overflow, e)
	}
}

func (a *Aggregator) partition(name string) *partitionState {
	p, ok := a.partitions[name]
	if !ok {
		p = &partitionState{}
		a.partitions[name] = p
	}
	return p
}

// recomputeLocked advances the global watermark to the minimum watermark of
// the active (non-idle) partitions. When no active partition holds a
// watermark — including the all-partitions-idle case — the current global
// value is kept unchanged. The global watermark never regresses.
func (a *Aggregator) recomputeLocked() {
	var min int64
	found := false
	for _, p := range a.partitions {
		if p.idle || !p.set {
			continue
		}
		if !found || p.watermark < min {
			min = p.watermark
			found = true
		}
	}
	if !found {
		return
	}
	if !a.hasGlobal || min > a.global {
		a.global = min
		a.hasGlobal = true
	}
}
