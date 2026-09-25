package sim

// Kind is the message type.
type Kind int

const (
	KindTransfer Kind = iota
	KindAck
)

// Message is the only thing replicas exchange. Transfers and their acks are
// both idempotent at the receiver, keyed by the unique transfer ID.
type Message struct {
	Kind   Kind
	ID     string // unique transfer ID
	From   int
	To     int
	Amount int64
}

// Outgoing is a transfer the sender has debited but not yet seen acked.
type Outgoing struct {
	ID     string
	To     int
	Amount int64
}

// Replica holds one shard of the global quota. Local, Processed and Sent are
// durable state (they survive Crash/Restart); Pending is rebuilt from durable
// state on restart in a real system and simply retained here.
type Replica struct {
	ID        int
	Local     int64 // local entitlement shard; never negative
	Crashed   bool
	Pending   map[string]*Outgoing // outgoing transfers awaiting ack
	Processed map[string]bool      // credited transfer IDs (receiver-side dedup)
	Sent      map[string]bool      // accepted transfer request IDs (sender-side dedup)
}

// Deduct consumes quota locally, offline, with zero coordination. It is safe
// because shards are disjoint: sum of all deductions can never exceed Total.
func (s *Simulator) Deduct(rid int, amount int64) bool {
	r := s.Replicas[rid]
	if r.Crashed {
		s.logf("t=%d R%d DEDUCT %d IGNORED (crashed)", s.Now, rid, amount)
		return false
	}
	if amount <= 0 || r.Local < amount {
		s.logf("t=%d R%d DEDUCT %d REJECTED (local=%d, no overdraw)", s.Now, rid, amount, r.Local)
		return false
	}
	r.Local -= amount
	s.Consumed += amount
	s.logf("t=%d R%d DEDUCT %d ok (local=%d, consumed=%d)", s.Now, rid, amount, r.Local, s.Consumed)
	return true
}

// Transfer moves amount of entitlement from replica `from` to replica `to`
// under the unique ID id. The sender debits immediately (the amount becomes
// in-flight and unavailable to anyone) and retransmits until acked.
//
// Re-issuing a transfer with an already-used ID is a no-op: request-level
// retries cannot mint quota either.
func (s *Simulator) Transfer(from, to int, amount int64, id string) bool {
	r := s.Replicas[from]
	if r.Crashed {
		s.logf("t=%d R%d TRANSFER %s IGNORED (crashed)", s.Now, from, id)
		return false
	}
	if r.Sent[id] {
		s.logf("t=%d R%d TRANSFER %s DEDUP (request retry, no double debit)", s.Now, from, id)
		return false
	}
	if amount <= 0 || r.Local < amount {
		s.logf("t=%d R%d TRANSFER %s amount=%d REJECTED (local=%d, no overdraw)", s.Now, from, id, amount, r.Local)
		return false
	}
	r.Sent[id] = true
	r.Local -= amount
	r.Pending[id] = &Outgoing{ID: id, To: to, Amount: amount}
	s.Inflight[id] = amount
	s.logf("t=%d R%d SEND %s amount=%d -> R%d (local=%d, inflight=%d)",
		s.Now, from, id, amount, to, r.Local, sumMap(s.Inflight))
	s.Net.Send(Message{Kind: KindTransfer, ID: id, From: from, To: to, Amount: amount})
	s.Schedule(s.Now+s.RetryInterval, RetryEvent{Replica: from, ID: id})
	return true
}

// Crash stops the replica; incoming messages are lost while it is down.
func (s *Simulator) Crash(rid int) {
	r := s.Replicas[rid]
	if r.Crashed {
		return
	}
	r.Crashed = true
	s.logf("t=%d R%d CRASH", s.Now, rid)
}

// Restart brings the replica back with its durable state and resumes
// retransmission of every unacked transfer.
func (s *Simulator) Restart(rid int) {
	r := s.Replicas[rid]
	if !r.Crashed {
		return
	}
	r.Crashed = false
	s.logf("t=%d R%d RESTART (local=%d durable, pending=%d)", s.Now, rid, r.Local, len(r.Pending))
	for id := range r.Pending {
		s.Schedule(s.Now+s.RetryInterval, RetryEvent{Replica: rid, ID: id})
	}
}

// Deliver injects a message directly, bypassing the network model. Tests use
// it to force exact duplicate/loss scenarios.
func (s *Simulator) Deliver(m Message) {
	(DeliverEvent{Msg: m}).Handle(s)
}

// --- events ----------------------------------------------------------------

// DeliverEvent is a message arrival at its destination replica.
type DeliverEvent struct{ Msg Message }

func (e DeliverEvent) Handle(s *Simulator) {
	m := e.Msg
	r := s.Replicas[m.To]
	if r.Crashed {
		s.logf("t=%d R%d RECV %s LOST (replica down)", s.Now, m.To, m.ID)
		return
	}
	switch m.Kind {
	case KindTransfer:
		if r.Processed[m.ID] {
			// Duplicate delivery (or retry after a lost ack): do NOT credit
			// again, just re-ack. This is what makes retries safe.
			s.logf("t=%d R%d RECV %s DUPLICATE -> re-ack, no credit (local=%d)",
				s.Now, m.To, m.ID, r.Local)
		} else {
			r.Processed[m.ID] = true
			r.Local += m.Amount
			delete(s.Inflight, m.ID)
			s.Credits[m.ID]++
			s.logf("t=%d R%d RECV %s CREDIT +%d (local=%d)", s.Now, m.To, m.ID, m.Amount, r.Local)
		}
		s.Net.Send(Message{Kind: KindAck, ID: m.ID, From: m.To, To: m.From})
	case KindAck:
		if _, ok := r.Pending[m.ID]; ok {
			delete(r.Pending, m.ID)
			s.logf("t=%d R%d RECV ACK %s (transfer complete)", s.Now, m.To, m.ID)
		}
	}
}

// RetryEvent retransmits an unacked transfer and reschedules itself.
type RetryEvent struct {
	Replica int
	ID      string
}

func (e RetryEvent) Handle(s *Simulator) {
	r := s.Replicas[e.Replica]
	if r.Crashed {
		return // Restart() re-arms retries for all pending transfers
	}
	out, ok := r.Pending[e.ID]
	if !ok {
		return // acked already
	}
	s.logf("t=%d R%d RETRY %s amount=%d -> R%d", s.Now, e.Replica, out.ID, out.Amount, out.To)
	s.Net.Send(Message{Kind: KindTransfer, ID: out.ID, From: e.Replica, To: out.To, Amount: out.Amount})
	s.Schedule(s.Now+s.RetryInterval, RetryEvent{Replica: e.Replica, ID: e.ID})
}

// ActionEvent schedules an arbitrary scripted action at a virtual time.
type ActionEvent struct{ Do func(s *Simulator) }

func (e ActionEvent) Handle(s *Simulator) { e.Do(s) }
