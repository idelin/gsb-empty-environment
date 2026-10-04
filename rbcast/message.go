package rbcast

// Message is a single broadcast unit. It is uniquely identified by the
// (Source, Seq) pair: every source numbers its messages with a strictly
// increasing sequence counter starting at 1.
type Message struct {
	Source string
	Seq    uint64
	Body   string
}

// Key returns the dedup identity of the message.
func (m Message) Key() MessageKey { return MessageKey{Source: m.Source, Seq: m.Seq} }

// MessageKey is the dedup identity of a message.
type MessageKey struct {
	Source string
	Seq    uint64
}

// Ack reports a subscriber's contiguous delivery watermark for a source.
// Generation identifies the member's current membership epoch: it is
// incremented every time the member rejoins, so acks produced before a
// leave/rejoin cycle carry a stale generation and must be rejected.
type Ack struct {
	MemberID   string
	Generation uint64
	Source     string
	Watermark  uint64
}
