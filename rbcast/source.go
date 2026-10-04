package rbcast

// Source broadcasts numbered messages and retains a bounded retransmission
// log so offline subscribers can catch up later. The log may only be
// compacted up to the lowest ack watermark across ALL currently valid
// subscribers: a single lagging (or offline-then-rejoined) member pins the
// history it still needs.
type Source struct {
	ID        string
	nextSeq   uint64
	log       []Message         // retained messages, all with Seq > compacted
	compacted uint64            // highest seq safely dropped from the log
	acks      map[string]uint64 // memberID -> last accepted ack watermark
}

func NewSource(id string) *Source {
	return &Source{ID: id, acks: make(map[string]uint64)}
}

// Broadcast assigns the next sequence number and retains the message.
func (s *Source) Broadcast(body string) Message {
	s.nextSeq++
	m := Message{Source: s.ID, Seq: s.nextSeq, Body: body}
	s.log = append(s.log, m)
	return m
}

// RecordAck stores an the ack watermark for a member. Caller must have
// validated the ack's generation via Membership.AcceptAck.
func (s *Source) RecordAck(memberID string, watermark uint64) {
	if watermark > s.acks[memberID] {
		s.acks[memberID] = watermark
	}
}

// ForgetMember drops ack state for a member that left, so a stale high
// watermark cannot survive into the member's next epoch.
func (s *Source) ForgetMember(memberID string) {
	delete(s.acks, memberID)
}

// CompactableSeq returns the highest seq that may be dropped: the minimum
// ack watermark over all currently valid subscribers. Members that have
// never acked in their current epoch count as 0 and pin the whole log.
func (s *Source) CompactableSeq(validMembers []string) uint64 {
	if len(validMembers) == 0 {
		return 0
	}
	min := uint64(0)
	first := true
	for _, id := range validMembers {
		w := s.acks[id]
		if first || w < min {
			min = w
			first = false
		}
	}
	return min
}

// Compact drops retained messages up to the compactable point. It never
// drops anything a valid subscriber has not confirmed.
func (s *Source) Compact(validMembers []string) uint64 {
	target := s.CompactableSeq(validMembers)
	if target <= s.compacted {
		return s.compacted
	}
	keep := s.log[:0]
	for _, m := range s.log {
		if m.Seq > target {
			keep = append(keep, m)
		}
	}
	s.log = keep
	s.compacted = target
	return s.compacted
}

// Retained returns the number of messages still in the retransmission log.
func (s *Source) Retained() int { return len(s.log) }

// Compacted returns the highest seq dropped from the log.
func (s *Source) Compacted() uint64 { return s.compacted }

// RetransmitFrom returns retained messages with Seq > watermark, oldest
// first, for catching up a rejoined subscriber. Because compaction is gated
// on every valid subscriber's ack, anything the subscriber still needs is
// guaranteed to be present here.
func (s *Source) RetransmitFrom(watermark uint64) []Message {
	out := make([]Message, 0, len(s.log))
	for _, m := range s.log {
		if m.Seq > watermark {
			out = append(out, m)
		}
	}
	return out
}

// LatestSeq returns the highest sequence number broadcast so far.
func (s *Source) LatestSeq() uint64 { return s.nextSeq }
