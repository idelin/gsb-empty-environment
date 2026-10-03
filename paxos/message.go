package paxos

// MsgKind enumerates the protocol message types.
type MsgKind int

const (
	MsgPrepare MsgKind = iota
	MsgPromise
	MsgAccept
	MsgAccepted
	MsgHeartbeat
	MsgHeartbeatAck
)

func (k MsgKind) String() string {
	switch k {
	case MsgPrepare:
		return "Prepare"
	case MsgPromise:
		return "Promise"
	case MsgAccept:
		return "Accept"
	case MsgAccepted:
		return "Accepted"
	case MsgHeartbeat:
		return "Heartbeat"
	case MsgHeartbeatAck:
		return "HeartbeatAck"
	}
	return "Unknown"
}

// Message is the single wire envelope for all protocol traffic.
//
// Fields used per kind:
//   - Prepare:       Ballot
//   - Promise:       Ballot, Log, CommitIndex (acceptor state snapshot)
//   - Accept:        Ballot, Index, Entry
//   - Accepted:      Ballot, Index
//   - Heartbeat:     Ballot, CommitIndex, Log (leader log for reconciliation)
//   - HeartbeatAck:  Ballot
type Message struct {
	Kind        MsgKind
	From        int
	To          int
	Ballot      Ballot
	Index       int
	Entry       Entry
	Log         []Entry
	CommitIndex int
}
