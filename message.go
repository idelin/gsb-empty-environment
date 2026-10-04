package merge

// MsgType is the kind of protocol message exchanged between nodes.
type MsgType int

const (
	MsgPing MsgType = iota
	MsgPingReq
	MsgAck
	MsgMergeSync
	MsgMergeDone
)

func (t MsgType) String() string {
	switch t {
	case MsgPing:
		return "Ping"
	case MsgPingReq:
		return "PingReq"
	case MsgAck:
		return "Ack"
	case MsgMergeSync:
		return "MergeSync"
	case MsgMergeDone:
		return "MergeDone"
	}
	return "Unknown"
}

// Message is the unit of the gossip / merge protocol. Every message carries a
// digest of the sender: its cluster identity, epoch, Lamport clock and its
// membership table (member ID -> highest Lamport heartbeat observed).
type Message struct {
	Type      MsgType
	From      int
	To        int
	ClusterID string
	Epoch     uint64
	Lamport   uint64
	Members   map[int]uint64
}
