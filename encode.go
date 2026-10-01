package streamdec

import "encoding/binary"

// Frame wraps a serialized object payload with its 4-byte big-endian
// length header. It is the counterpart of Decoder and is used by the
// tests and the demo program.
func Frame(payload []byte) []byte {
	out := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint32(out, uint32(len(payload)))
	copy(out[HeaderSize:], payload)
	return out
}
