package gossip

import (
	"math"
	"sort"
	"sync"
)

// DefaultRetransmitMult scales the log(N) dissemination-round budget.
const DefaultRetransmitMult = 3

type queuedEvent struct {
	event     Event
	transmits int // number of rounds this event has been piggybacked
}

// Disseminator manages membership change events as a propagation-count
// queue. Events that have been gossiped fewer times are preferred when
// filling a packet header, and an event is dropped once it has been
// transmitted for retransmitMult*ceil(log2(N+1)) rounds, by which time it
// is expected to have reached the whole cluster.
type Disseminator struct {
	mu             sync.Mutex
	events         map[string]*queuedEvent // keyed by node ID, newest event only
	retransmitMult int
	clusterSize    func() int
}

// NewDisseminator creates a queue. clusterSize reports the current number of
// cluster nodes and is consulted lazily so the round budget tracks N.
func NewDisseminator(retransmitMult int, clusterSize func() int) *Disseminator {
	if retransmitMult <= 0 {
		retransmitMult = DefaultRetransmitMult
	}
	return &Disseminator{
		events:         make(map[string]*queuedEvent),
		retransmitMult: retransmitMult,
		clusterSize:    clusterSize,
	}
}

// maxRounds is the dissemination budget: retransmitMult * ceil(log2(N+1)).
func (d *Disseminator) maxRounds() int {
	n := 1
	if d.clusterSize != nil {
		if v := d.clusterSize(); v > n {
			n = v
		}
	}
	return int(math.Ceil(float64(d.retransmitMult) * math.Log2(float64(n)+1)))
}

// Queue inserts a change event. A newer event for the same node replaces the
// queued one and restarts its propagation budget; stale events are ignored.
func (d *Disseminator) Queue(ev Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if old, ok := d.events[ev.NodeID]; ok && !ev.supersedes(old.event) {
		return
	}
	d.events[ev.NodeID] = &queuedEvent{event: ev}
}

// Pending reports how many events are still awaiting dissemination.
func (d *Disseminator) Pending() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.events)
}

// Piggyback selects up to limit events to attach to a Ping/Ack header,
// preferring the least-propagated ones, and accounts one transmission round
// for each selected event. Events that exhausted their round budget are
// evicted. limit <= 0 means no limit.
func (d *Disseminator) Piggyback(limit int) []Event {
	d.mu.Lock()
	defer d.mu.Unlock()

	maxRounds := d.maxRounds()
	all := make([]*queuedEvent, 0, len(d.events))
	for id, qe := range d.events {
		if qe.transmits >= maxRounds {
			delete(d.events, id)
			continue
		}
		all = append(all, qe)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].transmits != all[j].transmits {
			return all[i].transmits < all[j].transmits
		}
		return all[i].event.NodeID < all[j].event.NodeID
	})

	var out []Event
	for _, qe := range all {
		if limit > 0 && len(out) >= limit {
			break
		}
		qe.transmits++
		out = append(out, qe.event)
	}
	return out
}
