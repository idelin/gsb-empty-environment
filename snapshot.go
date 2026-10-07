package queue

import "time"

// Snapshot is a serializable copy of the full queue state. It preserves
// in-flight leases (token + deadline) and per-message delivery counts so
// a restored queue behaves exactly like the original.
type Snapshot struct {
	Pending  []Message     `json:"pending"`
	InFlight []LeaseRecord `json:"inflight"`
	DLQ      []Message     `json:"dlq"`
	Seq      uint64        `json:"seq"`
}

// LeaseRecord is the persisted form of an in-flight lease.
type LeaseRecord struct {
	Message  Message   `json:"message"`
	Token    string    `json:"token"`
	Deadline time.Time `json:"deadline"`
}

// Snapshot captures pending, in-flight and dead-letter state.
func (q *Queue) Snapshot() Snapshot {
	q.requeueExpired()
	snap := Snapshot{
		Pending: make([]Message, len(q.pending)),
		DLQ:     make([]Message, len(q.dlq)),
		Seq:     q.seq,
	}
	copy(snap.Pending, q.pending)
	copy(snap.DLQ, q.dlq)
	for _, l := range q.inflight {
		snap.InFlight = append(snap.InFlight, LeaseRecord{
			Message:  l.msg,
			Token:    l.token,
			Deadline: l.deadline,
		})
	}
	return snap
}

// Restore rebuilds a queue from a snapshot. Leases still valid at restore
// time keep their token, so an ack issued before the crash still lands.
// Expired leases fall back to pending and their old tokens are dropped:
// any ack carrying them is rejected. Delivery counts are preserved, so
// the dead-letter threshold reflects real deliveries across restarts.
func (q *Queue) Restore(snap Snapshot) {
	q.pending = nil
	q.inflight = make(map[string]lease)
	q.dlq = nil
	q.dlqSet = make(map[string]bool)

	for _, msg := range snap.Pending {
		if msg.Deliveries >= q.maxDeliveries {
			q.deadLetter(msg)
		} else {
			q.pending = append(q.pending, msg)
		}
	}
	for _, msg := range snap.DLQ {
		q.deadLetter(msg)
	}
	if q.seq < snap.Seq {
		q.seq = snap.Seq
	}

	now := q.clock.Now()
	for _, rec := range snap.InFlight {
		if rec.Deadline.After(now) {
			q.inflight[rec.Message.ID] = lease{
				msg:      rec.Message,
				token:    rec.Token,
				deadline: rec.Deadline,
			}
			continue
		}
		// Expired lease: back to pending, old token discarded.
		if rec.Message.Deliveries >= q.maxDeliveries {
			q.deadLetter(rec.Message)
		} else {
			q.pending = append(q.pending, rec.Message)
		}
	}
}
