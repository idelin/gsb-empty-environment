// Package netsec implements a hardened HTTP/2 to HTTP/1 request boundary for
// proxies. See the package README for the threat model and validation rules.
package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame type identifiers from RFC 7540 section 11.2.
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

// Frame flags from RFC 7540 section 6.
const (
	flagEndStream  FrameFlags = 0x1
	flagAck        FrameFlags = 0x1
	flagEndHeaders FrameFlags = 0x4
	flagPadded     FrameFlags = 0x8
	flagPriority   FrameFlags = 0x20
)

// Stream error codes from RFC 7540 section 7.
const (
	ErrCodeProtocol     uint32 = 0x1
	ErrCodeInternal     uint32 = 0x2
	ErrCodeFlowControl  uint32 = 0x3
	ErrCodeFrameSize    uint32 = 0x6
	ErrCodeRefused      uint32 = 0x7
	ErrCodeCancel       uint32 = 0x8
	ErrCodeCompression  uint32 = 0x9
	ErrCodeConnectError uint32 = 0xa
)

// MaxFramePayloadSize is the largest frame payload permitted by RFC 7540
// section 4.2: the 24-bit length field spans 0 to 2^24-1 octets.
const MaxFramePayloadSize = 1<<24 - 1

// FrameType is the 8-bit HTTP/2 frame type.
type FrameType uint8

// FrameFlags is the 8-bit flag field of an HTTP/2 frame header.
type FrameFlags uint8

// EndStream reports whether the END_STREAM flag is set.
func (f FrameFlags) EndStream() bool { return f&flagEndStream != 0 }

// EndHeaders reports whether the END_HEADERS flag is set.
func (f FrameFlags) EndHeaders() bool { return f&flagEndHeaders != 0 }

// Padded reports whether the PADDED flag is set.
func (f FrameFlags) Padded() bool { return f&flagPadded != 0 }

// Priority reports whether the PRIORITY flag is set.
func (f FrameFlags) Priority() bool { return f&flagPriority != 0 }

// Ack reports whether the ACK flag is set (SETTINGS/PING).
func (f FrameFlags) Ack() bool { return f&flagAck != 0 }

// Frame is a single decoded HTTP/2 frame. Payload is the raw frame payload and
// remains valid only until the next ReadFrame call. Helper methods parse the
// payload in a frame-type specific way.
type Frame struct {
	Type    FrameType
	Flags   FrameFlags
	Stream  uint32
	Payload []byte
}

// FrameError is returned for malformed framing (RFC 7540 section 4.1); the
// connection must be terminated when it is returned.
type FrameError struct {
	Code    uint32
	Message string
}

func (e *FrameError) Error() string {
	return fmt.Sprintf("netsec: frame error (code %#x): %s", e.Code, e.Message)
}

func frameError(code uint32, format string, args ...any) error {
	return &FrameError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// FrameReader reads raw HTTP/2 frames from an underlying reader. It enforces
// the common framing rules of RFC 7540 section 4.1.
type FrameReader struct {
	r   io.Reader
	buf [MaxFramePayloadSize]byte
}

// NewFrameReader wraps r as an HTTP/2 frame reader.
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: r}
}

// ReadFrame reads and validates the next frame. The returned Frame shares the
// reader's internal scratch buffer and is overwritten by the next call.
func (fr *FrameReader) ReadFrame() (*Frame, error) {
	var header [9]byte
	if _, err := io.ReadFull(fr.r, header[:]); err != nil {
		return nil, err
	}
	length := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
	if length > MaxFramePayloadSize {
		return nil, frameError(ErrCodeFrameSize, "frame payload of %d bytes exceeds %d", length, MaxFramePayloadSize)
	}
	payload := fr.buf[:length]
	if _, err := io.ReadFull(fr.r, payload); err != nil {
		return nil, err
	}
	f := &Frame{
		Type:    FrameType(header[3]),
		Flags:   FrameFlags(header[4]),
		Stream:  binary.BigEndian.Uint32(header[5:9]) & 0x7fffffff,
		Payload: payload,
	}
	if err := validateFrame(f); err != nil {
		return nil, err
	}
	return f, nil
}

func validateFrame(f *Frame) error {
	switch f.Type {
	case FrameSettings:
		if f.Stream != 0 {
			return frameError(ErrCodeProtocol, "SETTINGS on stream %d", f.Stream)
		}
		if len(f.Payload)%6 != 0 {
			return frameError(ErrCodeFrameSize, "SETTINGS payload length %d is not a multiple of 6", len(f.Payload))
		}
		if f.Flags.Ack() && len(f.Payload) != 0 {
			return frameError(ErrCodeFrameSize, "SETTINGS ACK must have an empty payload")
		}
	case FramePing:
		if f.Stream != 0 {
			return frameError(ErrCodeProtocol, "PING on stream %d", f.Stream)
		}
		if len(f.Payload) != 8 {
			return frameError(ErrCodeFrameSize, "PING payload must be 8 bytes, got %d", len(f.Payload))
		}
	case FrameGoAway:
		if f.Stream != 0 {
			return frameError(ErrCodeProtocol, "GOAWAY on stream %d", f.Stream)
		}
		if len(f.Payload) < 8 {
			return frameError(ErrCodeFrameSize, "GOAWAY payload must be at least 8 bytes")
		}
	case FrameWindowUpdate:
		if len(f.Payload) != 4 {
			return frameError(ErrCodeFrameSize, "WINDOW_UPDATE payload must be 4 bytes")
		}
		if binary.BigEndian.Uint32(f.Payload)&0x7fffffff == 0 {
			return frameError(ErrCodeProtocol, "WINDOW_UPDATE increment must be non-zero")
		}
	case FrameRSTStream:
		if f.Stream == 0 {
			return frameError(ErrCodeProtocol, "RST_STREAM on connection stream")
		}
		if len(f.Payload) != 4 {
			return frameError(ErrCodeFrameSize, "RST_STREAM payload must be 4 bytes")
		}
	case FrameHeaders:
		if f.Stream == 0 {
			return frameError(ErrCodeProtocol, "HEADERS on connection stream")
		}
	case FrameData:
		if f.Stream == 0 {
			return frameError(ErrCodeProtocol, "DATA on connection stream")
		}
	case FrameContinuation:
		if f.Stream == 0 {
			return frameError(ErrCodeProtocol, "CONTINUATION on connection stream")
		}
	case FramePushPromise, FramePriority:
		// Servers never send PUSH_PROMISE; PRIORITY frames are not used here.
		return frameError(ErrCodeProtocol, "unexpected frame type %d", f.Type)
	}
	return nil
}

// HeaderBlockFragment returns the HPACK header block fragment carried by a
// HEADERS or CONTINUATION frame, stripping PADDED and PRIORITY fields.
func (f *Frame) HeaderBlockFragment() ([]byte, error) {
	payload := f.Payload
	if f.Flags.Padded() {
		if len(payload) < 1 {
			return nil, frameError(ErrCodeProtocol, "truncated padding length")
		}
		padLen := int(payload[0])
		payload = payload[1:]
		if padLen >= len(payload) {
			return nil, frameError(ErrCodeProtocol, "padding length %d exceeds payload", padLen)
		}
		payload = payload[:len(payload)-padLen]
	}
	if f.Type == FrameHeaders && f.Flags.Priority() {
		if len(payload) < 5 {
			return nil, frameError(ErrCodeProtocol, "truncated PRIORITY fields")
		}
		payload = payload[5:]
	}
	return payload, nil
}

// DataPayload returns the unpadded application data of a DATA frame and the
// END_STREAM flag.
func (f *Frame) DataPayload() ([]byte, bool, error) {
	payload := f.Payload
	if f.Flags.Padded() {
		if len(payload) < 1 {
			return nil, false, frameError(ErrCodeProtocol, "truncated padding length")
		}
		padLen := int(payload[0])
		payload = payload[1:]
		if padLen > len(payload) {
			return nil, false, frameError(ErrCodeProtocol, "padding length %d exceeds payload", padLen)
		}
		payload = payload[:len(payload)-padLen]
	}
	return payload, f.Flags.EndStream(), nil
}

// RSTCode returns the error code carried by a RST_STREAM frame.
func (f *Frame) RSTCode() (uint32, error) {
	if f.Type != FrameRSTStream || len(f.Payload) != 4 {
		return 0, errors.New("netsec: not a RST_STREAM frame")
	}
	return binary.BigEndian.Uint32(f.Payload), nil
}

// WriteFrame serializes a frame with the given type, flags, stream identifier
// and payload onto w.
func WriteFrame(w io.Writer, typ FrameType, flags FrameFlags, stream uint32, payload []byte) error {
	if len(payload) > MaxFramePayloadSize {
		return fmt.Errorf("netsec: frame payload too large: %d", len(payload))
	}
	var header [9]byte
	header[0] = byte(len(payload) >> 16)
	header[1] = byte(len(payload) >> 8)
	header[2] = byte(len(payload))
	header[3] = byte(typ)
	header[4] = byte(flags)
	binary.BigEndian.PutUint32(header[5:], stream)
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

// RSTStreamFrame builds a RST_STREAM frame payload for the given error code.
func RSTStreamFrame(code uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, code)
	return buf
}
