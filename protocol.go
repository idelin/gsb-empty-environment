package netsec

// Protocol is the wire protocol selected by the first byte of a connection.
type Protocol uint8

const (
	// ProtocolUnknown means no request has selected a protocol yet.
	ProtocolUnknown Protocol = iota
	// ProtocolText is the Memcached text protocol.
	ProtocolText
	// ProtocolBinary is the Memcached binary protocol (request magic 0x80).
	ProtocolBinary
)

func (p Protocol) String() string {
	switch p {
	case ProtocolText:
		return "text"
	case ProtocolBinary:
		return "binary"
	default:
		return "unknown"
	}
}

// ResponseKind tells the connection handler how many response lines or frames
// it should read after forwarding a request.
type ResponseKind uint8

const (
	// ResponseNone means the request is quiet/noreply.
	ResponseNone ResponseKind = iota
	// ResponseSingle means one status line or one binary frame.
	ResponseSingle
	// ResponseMulti means text lines terminated by END (get/gets/gat/stats).
	ResponseMulti
)

// Request is one normalized client request. Bytes is ready to be written to
// the upstream Memcached server after the connection handler has checked the
// session protocol.
type Request struct {
	Protocol     Protocol
	Command      string
	Keys         [][]byte
	Value        []byte
	NoReply      bool
	CloseAfter   bool
	ResponseKind ResponseKind
	Bytes        []byte
	Opaque       uint32
	BinaryOpcode byte
}

// Key returns the first key, if any.
func (r *Request) Key() []byte {
	if len(r.Keys) == 0 {
		return nil
	}
	return r.Keys[0]
}
