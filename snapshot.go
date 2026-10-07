package msgqueue

import (
	"encoding/json"
	"time"
)

// snapshot is the durable form of the queue. In-flight leases (token,
// expiry) and real delivery counts are preserved so recovery can honor
// still-valid leases and keep dead-letter accounting exact.
type snapshot struct {
	Pending  []snapshotMessage `json:"pending"`
	Inflight []snapshotLease   `json:"inflight"`
	Dead     []snapshotMessage `json:"dead"`
	TokenSeq uint64            `json:"tokenSeq"`
}

type snapshotMessage struct {
	ID         string `json:"id"`
	Body       []byte `json:"body"`
	Deliveries int    `json:"deliveries"`
}

type snapshotLease struct {
	Token     string          `json:"token"`
	ExpiresAt time.Time       `json:"expiresAt"`
	Message   snapshotMessage `json:"message"`
}

func toSnapshotMessage(m *Message) snapshotMessage {
	body := make([]byte, len(m.Body))
	copy(body, m.Body)
	return snapshotMessage{ID: m.ID, Body: body, Deliveries: m.Deliveries}
}

func (s snapshotMessage) toMessage() *Message {
	body := make([]byte, len(s.Body))
	copy(body, s.Body)
	return &Message{ID: s.ID, Body: body, Deliveries: s.Deliveries}
}

// Snapshot serializes the full queue state, including in-flight leases and
// per-message delivery counts.
func (q *Queue) Snapshot() ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reapExpiredLocked(q.clock.Now())

	snap := snapshot{TokenSeq: q.tokenSeq}
	for _, m := range q.pending {
		snap.Pending = append(snap.Pending, toSnapshotMessage(m))
	}
	for _, l := range q.inflight {
		snap.Inflight = append(snap.Inflight, snapshotLease{
			Token:     l.token,
			ExpiresAt: l.expiresAt,
			Message:   toSnapshotMessage(l.msg),
		})
	}
	for _, m := range q.dead {
		snap.Dead = append(snap.Dead, toSnapshotMessage(m))
	}
	return json.Marshal(snap)
}

// Restore rebuilds a queue from a snapshot. Leases already expired at
// restore time are reaped: their messages return to pending (or dead) and
// their old ack tokens are gone, so stale consumers cannot ack them.
func Restore(data []byte, clock Clock, leaseDuration time.Duration, maxDeliveries int) (*Queue, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	q := New(clock, leaseDuration, maxDeliveries)
	q.tokenSeq = snap.TokenSeq
	for _, sm := range snap.Pending {
		q.pending = append(q.pending, sm.toMessage())
	}
	for _, sl := range snap.Inflight {
		q.inflight[sl.Token] = &lease{
			msg:       sl.Message.toMessage(),
			token:     sl.Token,
			expiresAt: sl.ExpiresAt,
		}
	}
	for _, sm := range snap.Dead {
		q.dead = append(q.dead, sm.toMessage())
	}
	// Expired leases go back to pending (or dead); their tokens are dropped.
	q.reapExpiredLocked(clock.Now())
	return q, nil
}
