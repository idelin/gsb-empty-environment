// Package framepack packs queued messages into length-capped frames with
// fragmentation, priority-aware fair scheduling, cancellation, and a
// streaming parser that tolerates arbitrarily chunked input.
package framepack

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderSize is the fixed wire header length. It always counts toward the
// maximum frame length.
const HeaderSize = 16

const (
	magic   uint16 = 0xF1A5
	version byte   = 1
)

const (
	// FlagStart marks the first fragment of a message.
	FlagStart byte = 1 << 0
	// FlagEnd marks the last fragment of a message. A message that fits in
	// a single frame carries FlagStart|FlagEnd on that one frame.
	FlagEnd byte = 1 << 1

	knownFlags = FlagStart | FlagEnd
)

var (
	// ErrNeedMoreData means the buffer holds a prefix of a valid frame;
	// feeding more bytes may complete it. It is not corruption.
	ErrNeedMoreData = errors.New("framepack: need more data")
	// ErrBadData means the bytes cannot belong to a valid frame no matter
	// how much more data arrives (bad magic, version, flags, or a length
	// that exceeds a configured quota).
	ErrBadData = errors.New("framepack: bad data")
)

// Frame is one wire frame: a fixed header plus at most maxFrame-HeaderSize
// payload bytes belonging to one message fragment.
type Frame struct {
	MsgID    uint32
	Priority uint8
	Flags    byte
	TotalLen uint32 // full message length, for reassembly quota checks
	Payload  []byte
}

// Start reports whether this is the first fragment of its message.
func (f Frame) Start() bool { return f.Flags&FlagStart != 0 }

// End reports whether this is the last fragment of its message.
func (f Frame) End() bool { return f.Flags&FlagEnd != 0 }

// EncodeFrame serializes f. The result is HeaderSize+len(f.Payload) bytes.
func EncodeFrame(f Frame) []byte {
	buf := make([]byte, HeaderSize+len(f.Payload))
	binary.BigEndian.PutUint16(buf[0:2], magic)
	buf[2] = version
	buf[3] = f.Flags
	binary.BigEndian.PutUint32(buf[4:8], f.MsgID)
	buf[8] = f.Priority
	buf[9] = 0
	binary.BigEndian.PutUint16(buf[10:12], uint16(len(f.Payload)))
	binary.BigEndian.PutUint32(buf[12:16], f.TotalLen)
	copy(buf[HeaderSize:], f.Payload)
	return buf
}

// parseFrame decodes one frame from the front of buf.
//
// It returns ErrNeedMoreData when buf is a strict prefix of a frame, and
// ErrBadData when the bytes are invalid regardless of what follows. Every
// length quota (maxFrame, maxMsg) is checked before any payload allocation.
func parseFrame(buf []byte, maxFrame, maxMsg int) (Frame, int, error) {
	if len(buf) < HeaderSize {
		return Frame{}, 0, ErrNeedMoreData
	}
	if binary.BigEndian.Uint16(buf[0:2]) != magic {
		return Frame{}, 0, fmt.Errorf("%w: bad magic 0x%04x", ErrBadData, binary.BigEndian.Uint16(buf[0:2]))
	}
	if buf[2] != version {
		return Frame{}, 0, fmt.Errorf("%w: unsupported version %d", ErrBadData, buf[2])
	}
	flags := buf[3]
	if flags&^knownFlags != 0 {
		return Frame{}, 0, fmt.Errorf("%w: unknown flags 0x%02x", ErrBadData, flags)
	}
	payloadLen := int(binary.BigEndian.Uint16(buf[10:12]))
	// Length quota check happens before any allocation below.
	if HeaderSize+payloadLen > maxFrame {
		return Frame{}, 0, fmt.Errorf("%w: frame length %d exceeds max frame %d", ErrBadData, HeaderSize+payloadLen, maxFrame)
	}
	totalLen := binary.BigEndian.Uint32(buf[12:16])
	if totalLen > uint32(maxMsg) {
		return Frame{}, 0, fmt.Errorf("%w: message length %d exceeds max message %d", ErrBadData, totalLen, maxMsg)
	}
	if len(buf) < HeaderSize+payloadLen {
		return Frame{}, 0, ErrNeedMoreData
	}
	payload := make([]byte, payloadLen)
	copy(payload, buf[HeaderSize:HeaderSize+payloadLen])
	f := Frame{
		MsgID:    binary.BigEndian.Uint32(buf[4:8]),
		Priority: buf[8],
		Flags:    flags,
		TotalLen: totalLen,
		Payload:  payload,
	}
	return f, HeaderSize + payloadLen, nil
}
