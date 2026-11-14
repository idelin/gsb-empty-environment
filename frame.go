package netsec

import (
	"encoding/binary"
	"fmt"
	"io"
)

// FrameType is an HTTP/2 frame type.
type FrameType uint8

const (
	FrameData         FrameType = 0x0
	FrameHeaders      FrameType = 0x1
	FramePriority     FrameType = 0x2
	FrameRSTStream    FrameType = 0x3
	FrameSettings     FrameType = 0x4
	FramePushPromise  FrameType = 0x5
	FramePing         FrameType = 0x6
	FrameGoAway       FrameType = 0x7
	FrameWindowUpdate FrameType = 0x8
	FrameContinuation FrameType = 0x9
)

func (t FrameType) String() string {
	switch t {
	case FrameData:
		return "DATA"
	case FrameHeaders:
		return "HEADERS"
	case FramePriority:
		return "PRIORITY"
	case FrameRSTStream:
		return "RST_STREAM"
	case FrameSettings:
		return "SETTINGS"
	case FramePushPromise:
		return "PUSH_PROMISE"
	case FramePing:
		return "PING"
	case FrameGoAway:
		return "GOAWAY"
	case FrameWindowUpdate:
		return "WINDOW_UPDATE"
	case FrameContinuation:
		return "CONTINUATION"
	default:
		return fmt.Sprintf("UNKNOWN_FRAME_0x%x", uint8(t))
	}
}

// FrameFlags is the eight-bit HTTP/2 flags field.
type FrameFlags uint8

const (
	FlagEndStream  FrameFlags = 0x1
	FlagAck        FrameFlags = 0x1
	FlagEndHeaders FrameFlags = 0x4
	FlagPadded     FrameFlags = 0x8
	FlagPriority   FrameFlags = 0x20
)

// Has reports whether flag is set.
func (f FrameFlags) Has(flag FrameFlags) bool { return f&flag != 0 }

// Frame is a structurally decoded HTTP/2 frame. Payload is the raw frame
// payload, including padding and priority fields for HEADERS frames.
type Frame struct {
	Type     FrameType
	Flags    FrameFlags
	StreamID uint32
	Payload  []byte
}

// HeaderBlockFragment returns the HPACK fragment carried by a HEADERS or
// CONTINUATION frame after removing the optional pad length, priority fields
// and trailing padding.
func (f Frame) HeaderBlockFragment() ([]byte, error) {
	switch f.Type {
	case FrameContinuation:
		return f.Payload, nil
	case FrameHeaders:
		return headerPayload(f.Flags, f.Payload)
	default:
		return nil, fmt.Errorf("netsec: %s frame does not carry a header block", f.Type)
	}
}

func headerPayload(flags FrameFlags, payload []byte) ([]byte, error) {
	p := payload
	var padLen int
	if flags.Has(FlagPadded) {
		if len(p) == 0 {
			return nil, connErr(CodeProtocol, "padded HEADERS is missing pad length", nil)
		}
		padLen = int(p[0])
		p = p[1:]
	}
	if flags.Has(FlagPriority) {
		if len(p) < 5 {
			return nil, connErr(CodeProtocol, "prioritized HEADERS is missing priority fields", nil)
		}
		p = p[5:]
	}
	if padLen > len(p) {
		return nil, connErr(CodeProtocol, "HEADERS padding exceeds payload", nil)
	}
	return p[:len(p)-padLen], nil
}

// ReadFrame reads one HTTP/2 frame from r. maxFrameSize is the maximum
// accepted payload size; zero means 16 MiB.
func ReadFrame(r io.Reader, maxFrameSize uint32) (Frame, error) {
	if maxFrameSize == 0 {
		maxFrameSize = 16 << 20
	}
	var hdr [9]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	length := uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2])
	if length > maxFrameSize {
		return Frame{}, connErr(CodeFrameSize, "frame exceeds configured maximum", nil)
	}
	f := Frame{
		Type:     FrameType(hdr[3]),
		Flags:    FrameFlags(hdr[4]),
		StreamID: binary.BigEndian.Uint32(hdr[5:9]) & 0x7fffffff,
	}
	if length == 0 {
		return f, nil
	}
	f.Payload = make([]byte, int(length))
	if _, err := io.ReadFull(r, f.Payload); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// WriteFrame writes an HTTP/2 frame to w.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Payload) > 0xffffff {
		return fmt.Errorf("netsec: frame payload too large: %d", len(f.Payload))
	}
	var hdr [9]byte
	length := len(f.Payload)
	hdr[0] = byte(length >> 16)
	hdr[1] = byte(length >> 8)
	hdr[2] = byte(length)
	hdr[3] = byte(f.Type)
	hdr[4] = byte(f.Flags)
	binary.BigEndian.PutUint32(hdr[5:9], f.StreamID&0x7fffffff)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(f.Payload)
	return err
}
