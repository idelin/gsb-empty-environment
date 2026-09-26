package sim

import (
	"container/heap"
	"fmt"
)

// event is one item on the discrete-event queue: either a message delivery
// or a timer callback. The simulation is single-threaded, so exactly one
// event runs at any (virtual) instant; ordering among equal timestamps is
// fixed by the tie-breaker sequence, which makes runs fully deterministic.
type event struct {
	when int64
	seq  int64
	msg  *Message // nil for timer events
	fn   func(t int64)
}

type eventHeap []*event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].when != h[j].when {
		return h[i].when < h[j].when
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(*event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Stats counts network-level and application-level events.
type Stats struct {
	Sent         int
	Delivered    int
	Dropped      int
	Duplicates   int // extra copies injected by the network
	TransferDups int // duplicate transfer copies rejected by a receiver
	AckDups      int // duplicate acks rejected by a sender
}

// Simulator is the single-process event loop: virtual clock, event queue,
// network model and the set of replicas.
type Simulator struct {
	now      int64
	events   eventHeap
	nextSeq  int64
	faults   Faults
	replicas map[int64]*Replica
	quota    int64 // global total quota Q, assigned at replica creation
	stats    Stats
	trace    func(line string)
}

// NewSimulator builds a simulator starting at virtual time 0.
func NewSimulator(f Faults) *Simulator {
	s := &Simulator{
		faults:   f,
		replicas: make(map[int64]*Replica),
	}
	heap.Init(&s.events)
	return s
}

// SetTrace installs a sink for human-readable trace lines.
func (s *Simulator) SetTrace(f func(line string)) { s.trace = f }

func (s *Simulator) logf(format string, args ...interface{}) {
	if s.trace != nil {
		s.trace(fmt.Sprintf("t=%-4d %s", s.now, fmt.Sprintf(format, args...)))
	}
}

// Stats returns a copy of the running counters, including application-level
// duplicate rejections aggregated from all replicas (crashed or not).
func (s *Simulator) Stats() Stats {
	st := s.stats
	for _, r := range s.replicas {
		st.TransferDups += r.transferDups
		st.AckDups += r.ackDups
	}
	return st
}

// Now is the current virtual time.
func (s *Simulator) Now() int64 { return s.now }

// AddReplica creates a replica with id and assigns it credit units out of
// the single global quota. The assignment is recorded durably in its WAL as
// the genesis record, so it survives a crash.
func (s *Simulator) AddReplica(id, credit int64) *Replica {
	if _, ok := s.replicas[id]; ok {
		panic(fmt.Sprintf("simulator: duplicate replica id %d", id))
	}
	r := newReplica(id, s)
	r.bootstrap(credit)
	s.replicas[id] = r
	s.quota += credit
	return r
}

// schedule enqueues a one-shot timer.
func (s *Simulator) schedule(when int64, fn func(t int64)) {
	s.nextSeq++
	heap.Push(&s.events, &event{when: when, seq: s.nextSeq, fn: fn})
}

// At enqueues a callback at absolute virtual time t.
func (s *Simulator) At(t int64, fn func(t int64)) {
	if t < s.now {
		t = s.now
	}
	s.schedule(t, fn)
}

// After enqueues a callback d units in the future.
func (s *Simulator) After(d int64, fn func(t int64)) {
	if d < 0 {
		d = 0
	}
	s.schedule(s.now+d, fn)
}

// Send puts one message into the network. Faults decide delivery delay,
// loss and duplication. The network never identifies retries: every call is
// a fresh independent send, which is what forces the application to dedup
// by TransferID.
func (s *Simulator) Send(m Message) {
	m.SentAt = s.now
	s.stats.Sent++
	s.dispatch(m)
}

func (s *Simulator) dispatch(m Message) {
	if s.faults.Drop(m) {
		s.stats.Dropped++
		return
	}
	delay := s.faults.Delay(m)
	if delay < 0 {
		delay = 0
	}
	s.deliver(m, delay)
	if s.faults.Duplicate(m) {
		s.stats.Duplicates++
		// The duplicate takes an independently chosen delay, as a real
		// replayed packet would.
		s.deliver(m, s.faults.Delay(m))
	}
}

func (s *Simulator) deliver(m Message, delay int64) {
	mm := m
	s.nextSeq++
	heap.Push(&s.events, &event{
		when: s.now + delay,
		seq:  s.nextSeq,
		msg:  &mm,
	})
}

// Crash stops a replica immediately. Anything already queued for it is
// discarded at delivery time; durable state lives only on its disk.
func (s *Simulator) Crash(id int64) {
	r, ok := s.replicas[id]
	if !ok {
		panic(fmt.Sprintf("simulator: unknown replica %d", id))
	}
	if r.down {
		return
	}
	r.crash()
	s.logf("replica %d CRASHED (disk survives)", id)
}

// Restart reloads durable state from disk and resumes timeout-driven retries.
func (s *Simulator) Restart(id int64) {
	r, ok := s.replicas[id]
	if !ok {
		panic(fmt.Sprintf("simulator: unknown replica %d", id))
	}
	if !r.down {
		return
	}
	r.restart()
	s.logf("replica %d RESTARTED, state recovered from disk", id)
}

// Run advances the event loop until the queue is empty.
func (s *Simulator) Run() {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(*event)
		s.now = e.when
		if e.msg != nil {
			s.stats.Delivered++
			r := s.replicas[e.msg.Target]
			if r != nil && !r.down {
				r.handle(*e.msg)
			}
			// Messages arriving for a crashed replica are lost; the
			// sender's retransmission timer recovers the transfer.
			continue
		}
		e.fn(s.now)
	}
}

// PendingEvents reports the number of queued events.
func (s *Simulator) PendingEvents() int { return len(s.events) }
