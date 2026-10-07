// Package streamagg implements a per-partition watermark aggregator.
//
// The global watermark advances to the minimum watermark across active
// partitions only. A partition is excluded from the minimum solely when it
// has been explicitly marked idle. Events arriving below the current global
// watermark (for example from a resumed partition) are routed to a late
// channel and never move the global watermark backwards.
package streamagg

import (
	"errors"
	"fmt"
	"sync"
)

// Event is a timestamped record flowing through a partition.
type Event struct {
	Partition string
	Timestamp int64
	Value     interface{}
}

// ErrUnknownPartition is returned when an event references a partition that
// has not been registered with the aggregator.
var ErrUnknownPartition = errors.New("streamagg: unknown partition")

type partitionState struct {
	watermark    int64
	hasWatermark bool
	idle         bool
}

// Aggregator tracks per-partition watermarks and derives a global watermark.
type Aggregator struct {
	mu         sync.Mutex
	partitions map[string]*partitionState
	global     int64
	hasGlobal  bool
	late       chan Event
	dropped    int
}

// New creates an Aggregator. lateBuf is the buffer size of the late-event
// channel exposed via Late.
func New(lateBuf int) *Aggregator {
	return &Aggregator{
		partitions: make(map[string]*partitionState),
		late:       make(chan Event, lateBuf),
	}
}

// AddPartition registers a partition. It starts active and without a
// watermark, so it blocks global advancement until it reports one or is
// marked idle. Re-adding an existing partition is a no-op.
func (a *Aggregator) AddPartition(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.partitions[id]; !ok {
		a.partitions[id] = &partitionState{}
	}
}

// MarkIdle excludes a partition from the global watermark computation.
func (a *Aggregator) MarkIdle(id string) error {
	return a.setIdle(id, true)
}

// MarkActive resumes a previously idled partition. Its last reported
// watermark is retained, so events below the current global watermark are
// classified as late.
func (a *Aggregator) MarkActive(id string) error {
	return a.setIdle(id, false)
}

func (a *Aggregator) setIdle(id string, idle bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.partitions[id]
	if !ok {
		return ErrUnknownPartition
	}
	p.idle = idle
	a.advanceLocked()
	return nil
}

// OnEvent ingests an event and returns the global watermark after the event
// is applied. Events timestamped below the current global watermark are
// forwarded to the late channel and never regress it. Per-partition
// watermarks are monotonic: an event older than the partition's own
// watermark (but not globally late) leaves the partition watermark unchanged.
func (a *Aggregator) OnEvent(ev Event) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.partitions[ev.Partition]
	if !ok {
		return a.global, ErrUnknownPartition
	}
	if a.hasGlobal && ev.Timestamp < a.global {
		a.emitLateLocked(ev)
		return a.global, nil
	}
	if !p.hasWatermark || ev.Timestamp > p.watermark {
		p.watermark = ev.Timestamp
		p.hasWatermark = true
	}
	a.advanceLocked()
	return a.global, nil
}

func (a *Aggregator) emitLateLocked(ev Event) {
	select {
	case a.late <- ev:
	default:
		a.dropped++
	}
}

// advanceLocked recomputes the global watermark as the minimum over active
// partitions. An active partition that has not reported a watermark yet
// blocks advancement entirely. The global watermark is monotonic and holds
// its previous value when every partition is idle.
func (a *Aggregator) advanceLocked() {
	var min int64
	found := false
	for _, p := range a.partitions {
		if p.idle {
			continue
		}
		if !p.hasWatermark {
			return
		}
		if !found || p.watermark < min {
			min = p.watermark
			found = true
		}
	}
	if found && (!a.hasGlobal || min > a.global) {
		a.global = min
		a.hasGlobal = true
	}
}

// Global returns the current global watermark and whether any watermark has
// been established yet.
func (a *Aggregator) Global() (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.global, a.hasGlobal
}

// Late returns the channel receiving late events.
func (a *Aggregator) Late() <-chan Event {
	return a.late
}

// DroppedLate returns how many late events were discarded because the late
// channel buffer was full.
func (a *Aggregator) DroppedLate() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dropped
}

// IsIdle reports whether a partition is currently marked idle.
func (a *Aggregator) IsIdle(id string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.partitions[id]
	if !ok {
		return false, ErrUnknownPartition
	}
	return p.idle, nil
}

// PartitionWatermark returns a partition's current watermark.
func (a *Aggregator) PartitionWatermark(id string) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.partitions[id]
	if !ok {
		return 0, ErrUnknownPartition
	}
	if !p.hasWatermark {
		return 0, fmt.Errorf("streamagg: partition %q has no watermark", id)
	}
	return p.watermark, nil
}
