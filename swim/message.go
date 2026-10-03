package swim

// MsgType 是 SWIM 协议消息类型。
type MsgType int

const (
	Ping MsgType = iota
	PingReq
	Ack
)

func (t MsgType) String() string {
	switch t {
	case Ping:
		return "PING"
	case PingReq:
		return "PING_REQ"
	case Ack:
		return "ACK"
	}
	return "UNKNOWN"
}

// Message 是节点间交换的协议消息。
// PingReq 中 Target 为被代理探测的节点；Ack 通过 Seq 与请求配对。
type Message struct {
	Type   MsgType
	From   string
	To     string
	Target string
	Seq    uint64
}
