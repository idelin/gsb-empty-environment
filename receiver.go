// Package recvq implements a bounded receive queue with priority lanes,
// per-tenant fair scheduling, explicit drop policies, and conservation
// statistics (arrived == processed + dropped + in-queue).
package recvq

import (
	"fmt"
	"sync"
)

// Priority of a record. High-priority records are scheduled more often,
// but can never fully starve Normal records (weighted 3:1 scheduling).
type Priority int

const (
	Normal Priority = iota
	High
)

func (p Priority) String() string {
	if p == High {
		return "high"
	}
	return "normal"
}

// Record is a unit of received data.
type Record struct {
	ID       string
	Tenant   string
	Priority Priority
	Payload  string
}

// DropPolicy decides what happens when the queue is full.
type DropPolicy int

const (
	// DropIncoming rejects the newly arriving record (reason "queue_full").
	DropIncoming DropPolicy = iota
	// DropOldest evicts the oldest queued record (normal lane first) to
	// make room for the new one (reason "evicted").
	DropOldest
)

// Mode selects the shutdown behavior.
type Mode int

const (
	// Drain stops accepting new records and processes everything queued.
	Drain Mode = iota
	// StopNow stops accepting new records and discards everything still
	// queued (reason "shutdown").
	StopNow
)

// Drop reasons used in Stats.DropsByReason and telemetry events.
const (
	ReasonQueueFull = "queue_full"
	ReasonEvicted   = "evicted"
	ReasonClosed    = "closed"
	ReasonShutdown  = "shutdown"
)

// laneWeight gives High 3 picks for every 1 Normal pick while both lanes
// are non-empty, so a sustained high-priority stream gets 75% of capacity
// and the normal stream is guaranteed at least 25%.
const (
	highWeight   = 3
	normalWeight = 1
)

// Stats are the conservation counters. At quiescence (queue empty, no
// in-flight records) the invariant holds:
//
//	Arrived == Processed + Dropped
//
// Rejected records count as Dropped, never as Processed.
type Stats struct {
	Arrived       int            `json:"arrived"`
	Enqueued      int            `json:"enqueued"`
	Processed     int            `json:"processed"`
	Dropped       int            `json:"dropped"`
	DropsByReason map[string]int `json:"drops_by_reason"`
}

func newStats() Stats {
	return Stats{DropsByReason: map[string]int{}}
}

func (s Stats) clone() Stats {
	out := s
	out.DropsByReason = map[string]int{}
	for k, v := range s.DropsByReason {
		out.DropsByReason[k] = v
	}
	return out
}

// Conserved reports whether every arrived record is accounted for as
// processed, dropped, or still sitting in the queue.
func (s Stats) Conserved(inQueue int) bool {
	return s.Arrived == s.Processed+s.Dropped+inQueue
}

func (s Stats) String() string {
	return fmt.Sprintf("arrived=%d enqueued=%d processed=%d dropped=%d by_reason=%v",
		s.Arrived, s.Enqueued, s.Processed, s.Dropped, s.DropsByReason)
}

// lane is a per-priority set of per-tenant FIFO queues served round-robin.
type lane struct {
	order   []string            // tenants in round-robin order
	queues  map[string][]Record // tenant -> pending records
	next    int                 // round-robin cursor into order
	pending int
}

func newLane() *lane {
	return &lane{queues: map[string][]Record{}}
}

func (l *lane) push(rec Record) {
	if _, ok := l.queues[rec.Tenant]; !ok {
		l.order = append(l.order, rec.Tenant)
	}
	l.queues[rec.Tenant] = append(l.queues[rec.Tenant], rec)
	l.pending++
}

// pop returns the next record, fairly rotating across tenants.
func (l *lane) pop() (Record, bool) {
	if l.pending == 0 {
		return Record{}, false
	}
	for i := 0; i < len(l.order); i++ {
		idx := (l.next + i) % len(l.order)
		tenant := l.order[idx]
		q := l.queues[tenant]
		if len(q) == 0 {
			continue
		}
		rec := q[0]
		l.queues[tenant] = q[1:]
		l.pending--
		l.next = (idx + 1) % len(l.order)
		return rec, true
	}
	return Record{}, false
}

// popOldest removes the record that has been queued longest (front of the
// longest-waiting tenant queue, scanning from the round-robin cursor).
func (l *lane) popOldest() (Record, bool) {
	return l.pop()
}

func (l *lane) drain() []Record {
	var out []Record
	for {
		rec, ok := l.pop()
		if !ok {
			return out
		}
		out = append(out, rec)
	}
}

// Receiver is a bounded queue with two priority lanes, per-tenant fair
// scheduling, an explicit drop policy, and conservation statistics.
type Receiver struct {
	mu       sync.Mutex
	cond     *sync.Cond
	capacity int
	size     int
	lanes    [2]*lane // [Normal], [High]
	streak   int      // consecutive high-lane picks for weighted scheduling
	policy   DropPolicy
	closed   bool
	stats    Stats
	sink     func(Event)
	seq      int
}

// NewReceiver creates a bounded queue. sink may be nil; when set it
// receives a telemetry Event for every enqueue/process/drop. The sink is
// called with the receiver lock held and must not call back into the
// Receiver.
func NewReceiver(capacity int, policy DropPolicy, sink func(Event)) *Receiver {
	if capacity <= 0 {
		panic("recvq: capacity must be positive")
	}
	r := &Receiver{
		capacity: capacity,
		policy:   policy,
		stats:    newStats(),
		sink:     sink,
	}
	r.cond = sync.NewCond(&r.mu)
	r.lanes[Normal] = newLane()
	r.lanes[High] = newLane()
	return r
}

func (r *Receiver) emit(typ string, rec Record, reason string) {
	if r.sink == nil {
		return
	}
	r.seq++
	r.sink(Event{
		Seq:      r.seq,
		Type:     typ,
		Record:   rec.ID,
		Tenant:   rec.Tenant,
		Priority: rec.Priority.String(),
		Reason:   reason,
	})
}

// dropLocked accounts a dropped record and emits its telemetry event.
func (r *Receiver) dropLocked(rec Record, reason string) {
	r.stats.Dropped++
	r.stats.DropsByReason[reason]++
	r.emit(EventDrop, rec, reason)
}

// Enqueue offers a record to the queue. It reports whether the record was
// accepted; rejected records are counted as dropped, never as processed.
func (r *Receiver) Enqueue(rec Record) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.Arrived++
	if r.closed {
		r.dropLocked(rec, ReasonClosed)
		return false
	}
	if r.size >= r.capacity {
		switch r.policy {
		case DropIncoming:
			r.dropLocked(rec, ReasonQueueFull)
			return false
		case DropOldest:
			victim, ok := r.lanes[Normal].popOldest()
			if !ok {
				victim, ok = r.lanes[High].popOldest()
			}
			if ok {
				r.size--
				r.dropLocked(victim, ReasonEvicted)
			}
		}
	}
	r.lanes[rec.Priority].push(rec)
	r.size++
	r.stats.Enqueued++
	r.emit(EventEnqueue, rec, "")
	r.cond.Signal()
	return true
}

// popLocked picks the next record: weighted 3:1 between the high and
// normal lanes, round-robin across tenants within a lane.
func (r *Receiver) popLocked() (Record, bool) {
	high, normal := r.lanes[High], r.lanes[Normal]
	if high.pending > 0 && (normal.pending == 0 || r.streak < highWeight) {
		rec, ok := high.pop()
		if ok {
			r.streak++
			return rec, true
		}
	}
	if normal.pending > 0 {
		rec, ok := normal.pop()
		if ok {
			r.streak = 0
			return rec, true
		}
	}
	if high.pending > 0 {
		r.streak = 0
		return high.pop()
	}
	return Record{}, false
}

// dequeue blocks until a record is available or the queue is closed and
// drained.
func (r *Receiver) dequeue() (Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if rec, ok := r.popLocked(); ok {
			r.size--
			return rec, true
		}
		if r.closed {
			return Record{}, false
		}
		r.cond.Wait()
	}
}

// markProcessed records successful handling of a dequeued record.
func (r *Receiver) markProcessed(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.Processed++
	r.emit(EventProcess, rec, "")
}

// Run starts workers that dequeue records and pass them to handle until
// the queue is closed and empty. Run blocks until all workers exit.
func (r *Receiver) Run(workers int, handle func(Record)) {
	if workers <= 0 {
		workers = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				rec, ok := r.dequeue()
				if !ok {
					return
				}
				handle(rec)
				r.markProcessed(rec)
			}
		}()
	}
	wg.Wait()
}

// Shutdown stops the queue. In Drain mode, queued records remain available
// for workers to finish. In StopNow mode, everything still queued is
// discarded and counted as dropped with reason "shutdown".
func (r *Receiver) Shutdown(mode Mode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if mode == StopNow {
		for _, l := range r.lanes {
			for _, rec := range l.drain() {
				r.size--
				r.dropLocked(rec, ReasonShutdown)
			}
		}
	}
	r.cond.Broadcast()
}

// Stats returns a snapshot of the conservation counters.
func (r *Receiver) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats.clone()
}

// InQueue returns the number of records currently queued.
func (r *Receiver) InQueue() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}
