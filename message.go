package correlator

import "encoding/binary"

// Kind identifies the frame type on the wire.
type Kind byte

const (
	KindRequest  Kind = 1
	KindResponse Kind = 2
)

// Message is one decoded frame. Requests and responses are correlated by
// the (Epoch, Seq) pair; Payload is opaque to the correlator.
type Message struct {
	Kind    Kind
	Epoch   uint64
	Seq     uint64
	Payload []byte
}

// Wire format (all integers big-endian):
//
//	[0:2]  magic 0xC0 0xDE
//	[2]    kind (1=request, 2=response)
//	[3:11] epoch uint64
//	[11:19] seq uint64
//	[19:23] payload length uint32
//	[23:]  payload
const (
	magic0     = 0xC0
	magic1     = 0xDE
	headerSize = 2 + 1 + 8 + 8 + 4

	// maxPayloadCap is the absolute ceiling for one frame's payload; a
	// Parser may be configured with a stricter quota.
	maxPayloadCap = 1 << 20
)

// Marshal encodes m into a single frame.
func (m Message) Marshal() []byte {
	buf := make([]byte, headerSize+len(m.Payload))
	buf[0] = magic0
	buf[1] = magic1
	buf[2] = byte(m.Kind)
	binary.BigEndian.PutUint64(buf[3:11], m.Epoch)
	binary.BigEndian.PutUint64(buf[11:19], m.Seq)
	binary.BigEndian.PutUint32(buf[19:23], uint32(len(m.Payload)))
	copy(buf[23:], m.Payload)
	return buf
}
