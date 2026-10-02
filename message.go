package swim

// MsgType is the kind of a protocol Message.
type MsgType int

const (
	Ping MsgType = iota
	Ack
	PingReq
)

func (t MsgType) String() string {
	switch t {
	case Ping:
		return "Ping"
	case Ack:
		return "Ack"
	case PingReq:
		return "PingReq"
	}
	return "Unknown"
}

// Message is the wire format exchanged between nodes.
type Message struct {
	Type MsgType
	From string // sender; for Ack, the node that was pinged
	Seq  int

	// Target is set on PingReq: the node the proxy should ping.
	Target string
	// ForwardTo is set on a Ping issued by a proxy and echoed back on
	// the resulting Ack, so the proxy knows where to forward it.
	ForwardTo string
	// Via is set by a proxy when forwarding an Ack to the originator.
	Via string
}
