package gossip

import (
	"math"
	"sort"
	"sync"
)

// Lambda is the dissemination multiplier: an event is gossiped at most
// Lambda*log2(N) times, which is enough for epidemic-style full coverage
// while bounding bandwidth.
const Lambda = 3

// MaxRounds returns the dissemination cap for a cluster of size n:
// Lambda * ceil(log2(n)), with a floor of 1.
func MaxRounds(n int) int {
	if n < 2 {
		return 1
	}
	r := int(math.Ceil(float64(Lambda) * math.Log2(float64(n))))
	if r < 1 {
		return 1
	}
	return r
}

type eventState struct {
	event     Event
	transmits int // number of times this event has been piggybacked
}

// Disseminator manages pending membership events as a propagation-count
// queue. Events with fewer transmits are preferred; an event is dropped
// once it has been gossiped maxRounds times.
type Disseminator struct {
	mu        sync.Mutex
	events    map[Event]*eventState
	maxRounds int
}

func NewDisseminator(clusterSize int) *Disseminator {
	return &Disseminator{
		events:    make(map[Event]*eventState),
		maxRounds: MaxRounds(clusterSize),
	}
}

// MaxRoundsLimit exposes the configured dissemination cap.
func (d *Disseminator) MaxRoundsLimit() int { return d.maxRounds }

// Queue registers a membership event for piggyback dissemination.
// Re-queueing an already pending event resets nothing; fresh transmits win.
func (d *Disseminator) Queue(e Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.events[e]; !ok {
		d.events[e] = &eventState{event: e}
	}
}

// Piggyback returns up to k events to attach to a Ping/Ack packet header,
// preferring the least-transmitted ones, and counts this emission against
// each returned event. Events that reach maxRounds are evicted.
func (d *Disseminator) Piggyback(k int) []Event {
	d.mu.Lock()
	defer d.mu.Unlock()

	ordered := make([]*eventState, 0, len(d.events))
	for _, es := range d.events {
		ordered = append(ordered, es)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].transmits != ordered[j].transmits {
			return ordered[i].transmits < ordered[j].transmits
		}
		if ordered[i].event.Node != ordered[j].event.Node {
			return ordered[i].event.Node < ordered[j].event.Node
		}
		return ordered[i].event.Type < ordered[j].event.Type
	})

	out := make([]Event, 0, k)
	for _, es := range ordered {
		if len(out) == k {
			break
		}
		out = append(out, es.event)
		es.transmits++
		if es.transmits >= d.maxRounds {
			delete(d.events, es.event)
		}
	}
	return out
}

// Pending reports how many events are still queued for dissemination.
func (d *Disseminator) Pending() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.events)
}

// Transmits reports how many times an event has been piggybacked (-1 if absent).
func (d *Disseminator) Transmits(e Event) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if es, ok := d.events[e]; ok {
		return es.transmits
	}
	return -1
}
