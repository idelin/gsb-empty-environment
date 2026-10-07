package viewbuilder

import "sync"

// Op is the kind of a change event.
type Op int

const (
	OpPut Op = iota
	OpDelete
)

// Event is a single committed change on the source, ordered by Seq.
type Event struct {
	Seq   uint64
	Key   string
	Value string
	Op    Op
}

// Ack acknowledges that a built view generation was switched in at Seq.
type Ack struct {
	Generation uint64
	Seq        uint64
}

// Source is the upstream store: a KV map plus an immutable change log.
// Delete events are retained in the log forever (never compacted away),
// so any replay starting from an older snapshot observes them.
type Source struct {
	mu   sync.RWMutex
	data map[string]string
	log  []Event // log[i].Seq == uint64(i+1)

	// shuffle, when set, reorders a polled batch before delivery to
	// simulate out-of-order change delivery.
	shuffle func([]Event)

	// dropAck, when set, reports whether an ack is lost in transit.
	dropAck func(Ack) bool

	ackedGen map[uint64]uint64 // generation -> acked seq (idempotent record)
	ackLog   []Ack             // every ack that actually arrived
}

// NewSource returns an empty source.
func NewSource() *Source {
	return &Source{
		data:     make(map[string]string),
		ackedGen: make(map[uint64]uint64),
	}
}

// SetShuffle installs the out-of-order delivery hook (test use).
func (s *Source) SetShuffle(f func([]Event)) { s.shuffle = f }

// SetDropAck installs the lost-ack hook (test use).
func (s *Source) SetDropAck(f func(Ack) bool) { s.dropAck = f }

func (s *Source) append(ev Op, k, v string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := uint64(len(s.log)) + 1
	s.log = append(s.log, Event{Seq: seq, Key: k, Value: v, Op: ev})
	if ev == OpPut {
		s.data[k] = v
	} else {
		delete(s.data, k)
	}
	return seq
}

// Put commits a put and returns its sequence number.
func (s *Source) Put(k, v string) uint64 { return s.append(OpPut, k, v) }

// Delete commits a delete and returns its sequence number.
func (s *Source) Delete(k string) uint64 { return s.append(OpDelete, k, "") }

// LatestSeq returns the newest committed sequence number.
func (s *Source) LatestSeq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return uint64(len(s.log))
}

// Snapshot returns a fixed point-in-time copy of the state and the
// sequence number it was taken at.
func (s *Source) Snapshot() (uint64, map[string]string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]string, len(s.data))
	for k, v := range s.data {
		cp[k] = v
	}
	return uint64(len(s.log)), cp
}

// Poll delivers a batch of events with Seq > after. Delivery may be
// out of order when a shuffle hook is installed; the log itself is
// complete, so re-polling eventually yields every missing event.
func (s *Source) Poll(after uint64, limit int) []Event {
	s.mu.RLock()
	var batch []Event
	for i := after; i < uint64(len(s.log)); i++ {
		batch = append(batch, s.log[i])
		if limit > 0 && len(batch) >= limit {
			break
		}
	}
	s.mu.RUnlock()
	if s.shuffle != nil && len(batch) > 1 {
		s.shuffle(batch)
	}
	return batch
}

// RecomputeAt is the test oracle: a full recomputation of the state at
// seq by folding the retained log from scratch.
func (s *Source) RecomputeAt(seq uint64) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string)
	for i := uint64(0); i < seq && i < uint64(len(s.log)); i++ {
		ev := s.log[i]
		if ev.Op == OpPut {
			out[ev.Key] = ev.Value
		} else {
			delete(out, ev.Key)
		}
	}
	return out
}

// SendAck delivers a switch acknowledgment. It reports false when the
// ack is lost in transit (injected). Recording is idempotent per
// generation: a retried ack after an ambiguous loss is a no-op.
func (s *Source) SendAck(a Ack) bool {
	if s.dropAck != nil && s.dropAck(a) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ackLog = append(s.ackLog, a)
	if _, ok := s.ackedGen[a.Generation]; !ok {
		s.ackedGen[a.Generation] = a.Seq
	}
	return true
}

// AckedSeq returns the recorded acked sequence for a generation.
func (s *Source) AckedSeq(gen uint64) (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seq, ok := s.ackedGen[gen]
	return seq, ok
}

// AckCount returns how many acks physically arrived (test use).
func (s *Source) AckCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ackLog)
}
