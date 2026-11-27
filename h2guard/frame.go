// Package h2guard implements an importable, dependency-free HTTP/2 (RFC 9113)
// raw-frame security parser for Go API gateways and reverse proxies.
//
// The package deliberately talks to the wire directly: it parses the fixed
// 9-byte frame header and frame payloads itself and ships a small, auditable
// HPACK decoder instead of relying on golang.org/x/net/http2 or any other
// external HTTP/2 implementation. It is built around three defenses:
//
//   - A strict frame-ordering / header-block state machine that rejects
//     inter-stream interleaving and bounds CONTINUATION frame counts and
//     header-block bytes (CONTINUATION flood).
//   - Strict pseudo-header and field validation that blocks request
//     smuggling primitives (duplicate/ordered pseudo-headers, uppercase
//     names, CR/LF/NUL in names or values, hop-by-hop headers,
//     Content-Length/data mismatches).
//   - A sliding-window Rapid Reset (CVE-2023-44487) detector that trips
//     ENHANCE_YOUR_CALM and drives a GOAWAY teardown.
package h2guard

import (
	"encoding/binary"
	"errors"
	"io"
)

// Frame type codes (RFC 9113 section 11.2).
const (
	FrameData         byte = 0x0
	FrameHeaders      byte = 0x1
	FramePriority     byte = 0x2
	FrameRSTStream    byte = 0x3
	FrameSettings     byte = 0x4
	FramePushPromise  byte = 0x5
	FramePing         byte = 0x6
	FrameGoAway       byte = 0x7
	FrameWindowUpdate byte = 0x8
	FrameContinuation byte = 0x9
	FrameAltSvc       byte = 0xa
)

// Frame flags.
const (
	FlagEndStream  byte = 0x1
	FlagAck        byte = 0x1
	FlagEndHeaders byte = 0x4
	FlagPadded     byte = 0x8
	FlagPriority   byte = 0x20
)

// HTTP/2 error codes (RFC 9113 section 11.4).
const (
	ErrCodeProtocol           uint32 = 0x1
	ErrCodeInternal           uint32 = 0x2
	ErrCodeFlowControl        uint32 = 0x3
	ErrCodeSettingsTimeout    uint32 = 0x4
	ErrCodeStreamClosed       uint32 = 0x5
	ErrCodeFrameSize          uint32 = 0x6
	ErrCodeRefusedStream      uint32 = 0x7
	ErrCodeCancel             uint32 = 0x8
	ErrCodeCompression        uint32 = 0x9
	ErrCodeConnect            uint32 = 0xa
	ErrCodeEnhanceYourCalm    uint32 = 0xb
	ErrCodeInadequateSecurity uint32 = 0xc
	ErrCodeHTTP11Required     uint32 = 0xd
)

// Protocol limits.
const (
	FrameHeaderLen      = 9
	MinMaxFrameSize     = 1 << 14 // 16384
	MaxFrameSizeHard    = 1 << 24 // 16777215, length field is 24 bits
	DefaultMaxFrameSize = MinMaxFrameSize
	MaxStreamID         = uint32(0x7fffffff)
)

// Frame is one parsed, uninterpreted HTTP/2 frame. Payload aliases the buffer
// read from the wire and must be consumed/copied before the next ReadFrame.
type Frame struct {
	Type     byte
	Flags    byte
	StreamID uint32
	Payload  []byte
}

// EndStream reports whether END_STREAM is set.
func (f *Frame) EndStream() bool { return f.Flags&FlagEndStream != 0 }

// EndHeaders reports whether END_HEADERS is set.
func (f *Frame) EndHeaders() bool { return f.Flags&FlagEndHeaders != 0 }

// Padded reports whether PADDED is set.
func (f *Frame) Padded() bool { return f.Flags&FlagPadded != 0 }

// Priority reports whether PRIORITY is set (HEADERS only).
func (f *Frame) Priority() bool { return f.Flags&FlagPriority != 0 }

// Ack reports whether ACK is set (SETTINGS/PING).
func (f *Frame) Ack() bool { return f.Flags&FlagAck != 0 }

// ErrShortRead is returned by ReadFrame when the stream ends in the middle of
// a frame header.
var ErrShortRead = errors.New("h2guard: truncated frame header")

// ReadFrame reads exactly one HTTP/2 frame from r. maxFrameSize is the active
// SETTINGS_MAX_FRAME_SIZE; a payload larger than maxFrameSize is rejected with
// FRAME_SIZE_ERROR semantics wrapped in a ConnError.
func ReadFrame(r io.Reader, maxFrameSize uint32) (*Frame, error) {
	var header [FrameHeaderLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrShortRead
		}
		return nil, err
	}
	length := uint32(header[0])<<16 | uint32(header[1])<<8 | uint32(header[2])
	if maxFrameSize == 0 {
		maxFrameSize = DefaultMaxFrameSize
	}
	if length > maxFrameSize {
		return nil, &ConnError{
			Code:    ErrCodeFrameSize,
			Message: "frame payload exceeds SETTINGS_MAX_FRAME_SIZE",
		}
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
	}
	return &Frame{
		Type:     header[3],
		Flags:    header[4],
		StreamID: binary.BigEndian.Uint32(header[5:9]) & MaxStreamID,
		Payload:  payload,
	}, nil
}

// BuildFrame serializes a frame with the given 9-byte header and payload. It
// performs no semantic validation; callers are the parser/tests building
// well-formed control frames.
func BuildFrame(typ byte, flags byte, streamID uint32, payload []byte) []byte {
	out := make([]byte, FrameHeaderLen+len(payload))
	out[0] = byte(len(payload) >> 16)
	out[1] = byte(len(payload) >> 8)
	out[2] = byte(len(payload))
	out[3] = typ
	out[4] = flags
	binary.BigEndian.PutUint32(out[5:9], streamID&MaxStreamID)
	copy(out[FrameHeaderLen:], payload)
	return out
}

// BuildRSTStream returns a RST_STREAM frame for streamID carrying code.
func BuildRSTStream(streamID uint32, code uint32) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, code)
	return BuildFrame(FrameRSTStream, 0, streamID, payload)
}

// BuildGoAway returns a GOAWAY frame carrying lastStreamID, code and the
// optional UTF-8 debug data.
func BuildGoAway(lastStreamID uint32, code uint32, debug []byte) []byte {
	payload := make([]byte, 8+len(debug))
	binary.BigEndian.PutUint32(payload[0:4], lastStreamID&MaxStreamID)
	binary.BigEndian.PutUint32(payload[4:8], code)
	copy(payload[8:], debug)
	return BuildFrame(FrameGoAway, 0, 0, payload)
}

// BuildSettings returns a SETTINGS frame (an ACK frame when ack is true, in
// which case the entries are ignored). Each entry is an id/value pair.
func BuildSettings(ack bool, entries map[uint16]uint32) []byte {
	if ack {
		return BuildFrame(FrameSettings, FlagAck, 0, nil)
	}
	payload := make([]byte, 0, 6*len(entries))
	for id, value := range entries {
		buf := make([]byte, 6)
		binary.BigEndian.PutUint16(buf[0:2], id)
		binary.BigEndian.PutUint32(buf[2:6], value)
		payload = append(payload, buf...)
	}
	return BuildFrame(FrameSettings, 0, 0, payload)
}

// BuildWindowUpdate returns a WINDOW_UPDATE frame.
func BuildWindowUpdate(streamID, increment uint32) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, increment&MaxStreamID)
	return BuildFrame(FrameWindowUpdate, 0, streamID, payload)
}
