// Package sim implements a single-process event simulator for a replicated
// quota/entitlement system.
//
// Model:
//   - A global quota Total is partitioned into per-replica local entitlements.
//     The partitions are disjoint, so offline (uncoordinated) deduction on any
//     replica can never make the global consumed amount exceed Total.
//   - Replicas may move entitlement between each other with transfers. Every
//     transfer carries a unique ID; the sender debits its local entitlement at
//     send time and the receiver credits at most once, deduplicated by ID.
//   - Communication (delay/drop/duplicate), crashes/restarts and virtual time
//     are all controlled by the Simulator's event queue.
//
// Evidence:
//   - Safety: after every processed event the simulator checks the
//     conservation invariant sum(local) + sum(inflight) + consumed == Total,
//     local[r] >= 0 for every replica, and credits[id] <= 1 for every
//     transfer ID. Any violation is recorded in Violations.
//   - Liveness: once the network is healed (Healed=true guarantees delivery),
//     the sender retransmits until it receives an ack, so every transfer
//     eventually settles: Settled() becomes true in bounded virtual time.
package sim

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sort"
)

// Event is a scheduled occurrence in virtual time.
type Event interface {
	Handle(s *Simulator)
}

type eventItem struct {
	time int64
	seq  int
	ev   Event
}

type eventQueue []eventItem

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].time != q[j].time {
		return q[i].time < q[j].time
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x interface{}) { *q = append(*q, x.(eventItem)) }
func (q *eventQueue) Pop() interface{} {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

// Simulator is the single-process event-driven world: virtual clock, event
// queue, network, replicas and global accounting used for the safety proof.
type Simulator struct {
	Now   int64
	Total int64 // global quota, partitioned across replicas

	Replicas []*Replica
	Net      *Network

	// Global accounting (the "proof observer"): in a real deployment these
	// are not a central store, they are the invariant we check.
	Consumed int64            // quota consumed by offline deductions
	Inflight map[string]int64 // transfer ID -> amount debited but not yet credited
	Credits  map[string]int   // transfer ID -> how many times a receiver credited it

	RetryInterval int64

	// CheckInvariants enables the safety check after every event.
	CheckInvariants bool
	Violations      []string

	LogLines []string

	rng   *rand.Rand
	queue eventQueue
	seq   int
}

// New creates a simulator. total is split into len(alloc) replica shards;
// alloc must sum to total.
func New(total int64, alloc []int64, seed int64) *Simulator {
	var sum int64
	for _, a := range alloc {
		sum += a
	}
	if sum != total {
		panic(fmt.Sprintf("alloc sum %d != total %d", sum, total))
	}
	s := &Simulator{
		Total:         total,
		Inflight:      map[string]int64{},
		Credits:       map[string]int{},
		RetryInterval: 5,
		rng:           rand.New(rand.NewSource(seed)),
	}
	s.Net = &Network{sim: s, MaxDelay: 3}
	for i, a := range alloc {
		s.Replicas = append(s.Replicas, &Replica{
			ID:        i,
			Local:     a,
			Pending:   map[string]*Outgoing{},
			Processed: map[string]bool{},
			Sent:      map[string]bool{},
		})
	}
	s.logf("t=%d INIT total=%d alloc=%v", s.Now, total, alloc)
	return s
}

func (s *Simulator) logf(format string, args ...interface{}) {
	s.LogLines = append(s.LogLines, fmt.Sprintf(format, args...))
}

// Schedule inserts an event at virtual time t.
func (s *Simulator) Schedule(t int64, ev Event) {
	s.seq++
	heap.Push(&s.queue, eventItem{time: t, seq: s.seq, ev: ev})
}

// Run processes all events with time <= until.
func (s *Simulator) Run(until int64) {
	for len(s.queue) > 0 {
		item := heap.Pop(&s.queue).(eventItem)
		if item.time > until {
			heap.Push(&s.queue, item)
			break
		}
		s.Now = item.time
		item.ev.Handle(s)
		if s.CheckInvariants {
			s.checkInvariants()
		}
	}
	if s.Now < until {
		s.Now = until
	}
}

// RunUntilEmpty processes events until the queue drains or maxTime is hit.
func (s *Simulator) RunUntilEmpty(maxTime int64) {
	for len(s.queue) > 0 && s.Now < maxTime {
		item := heap.Pop(&s.queue).(eventItem)
		if item.time > maxTime {
			heap.Push(&s.queue, item)
			break
		}
		s.Now = item.time
		item.ev.Handle(s)
		if s.CheckInvariants {
			s.checkInvariants()
		}
	}
}

// PendingEvents reports whether the queue still holds events.
func (s *Simulator) PendingEvents() int { return len(s.queue) }

// Available is the total currently usable entitlement across all replicas.
// In-flight transfer amounts are debited but not yet credited, so they are
// NOT available: a lost message reduces availability, it never inflates it.
func (s *Simulator) Available() int64 {
	var sum int64
	for _, r := range s.Replicas {
		sum += r.Local
	}
	return sum
}

// Settled is the liveness target: no transfer waiting for an ack and no
// amount in flight.
func (s *Simulator) Settled() bool {
	if len(s.Inflight) != 0 {
		return false
	}
	for _, r := range s.Replicas {
		if len(r.Pending) != 0 {
			return false
		}
	}
	return true
}

// --- Safety evidence -------------------------------------------------------

func (s *Simulator) violate(format string, args ...interface{}) {
	s.Violations = append(s.Violations, fmt.Sprintf("t=%d SAFETY VIOLATION: "+format, append([]interface{}{s.Now}, args...)...))
}

func (s *Simulator) checkInvariants() {
	// (1) No replica may overdraw its local entitlement.
	for _, r := range s.Replicas {
		if r.Local < 0 {
			s.violate("R%d local=%d < 0 (overdraw)", r.ID, r.Local)
		}
	}
	// (2) Conservation: local + in-flight + consumed == Total, always.
	var inflight int64
	for _, a := range s.Inflight {
		inflight += a
	}
	if got := s.Available() + inflight + s.Consumed; got != s.Total {
		s.violate("conservation broken: local=%d inflight=%d consumed=%d sum=%d total=%d",
			s.Available(), inflight, s.Consumed, got, s.Total)
	}
	// (3) No transfer ID may be credited more than once.
	ids := make([]string, 0, len(s.Credits))
	for id := range s.Credits {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if s.Credits[id] > 1 {
			s.violate("transfer %s credited %d times", id, s.Credits[id])
		}
	}
}

// Report renders the final accounting, used as printed safety evidence.
func (s *Simulator) Report() string {
	out := fmt.Sprintf("t=%d REPORT total=%d available=%d inflight=%d consumed=%d settled=%v violations=%d",
		s.Now, s.Total, s.Available(), sumMap(s.Inflight), s.Consumed, s.Settled(), len(s.Violations))
	for _, r := range s.Replicas {
		out += fmt.Sprintf("\n  R%d local=%d pending=%d processed=%d crashed=%v",
			r.ID, r.Local, len(r.Pending), len(r.Processed), r.Crashed)
	}
	return out
}

func sumMap(m map[string]int64) int64 {
	var sum int64
	for _, v := range m {
		sum += v
	}
	return sum
}
