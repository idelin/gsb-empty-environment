package causal

import "fmt"

// MsgID uniquely identifies a message: sender index + per-sender sequence.
type MsgID struct {
	Sender int
	Seq    int
}

func (id MsgID) String() string { return fmt.Sprintf("n%d#%d", id.Sender, id.Seq) }

// Message carries a vector clock snapshot taken at send time.
type Message struct {
	ID      MsgID
	From    int
	VC      VectorClock
	Payload string
}

func (m Message) String() string {
	return fmt.Sprintf("%s vc=%v %q", m.ID, []int(m.VC), m.Payload)
}
