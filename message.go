package causalrouter

import "fmt"

// Message is a discrete event routed through the system. Clock is the
// sender's version vector at send time and encodes the message's causal
// predecessors.
type Message struct {
	ID      string
	Sender  string
	Clock   VectorClock
	Payload string
}

// NewMessage builds a message stamped with the given clock (copied).
func NewMessage(sender string, clock VectorClock, payload string) *Message {
	return &Message{
		ID:      fmt.Sprintf("%s:%d", sender, clock[sender]),
		Sender:  sender,
		Clock:   clock.Copy(),
		Payload: payload,
	}
}

func (m *Message) String() string {
	return fmt.Sprintf("%s clock=%v payload=%q", m.ID, m.Clock, m.Payload)
}
