package causal

import "fmt"

// MsgID uniquely identifies a message: its sender plus the sender's
// per-node sequence number.
type MsgID struct {
	Sender NodeID
	Seq    uint64
}

func (id MsgID) String() string { return fmt.Sprintf("%s#%d", id.Sender, id.Seq) }

// Message is a broadcast unit carrying its causal history as a vector clock.
type Message struct {
	ID      MsgID
	Clock   VectorClock
	Payload string
}

func (m Message) String() string {
	return fmt.Sprintf("%s(%q)", m.ID, m.Payload)
}
