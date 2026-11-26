package h2h1

import (
	"encoding/binary"
	"errors"
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
	FrameAltsvc       FrameType = 0xa
	FrameOrigin       FrameType = 0xc
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
		return fmt.Sprintf("FRAME_0x%x", uint8(t))
	}
}

// Frame flags. A flag byte is interpreted according to the frame type.
const (
	FlagDataEndStream          uint8 = 0x1
	FlagDataPadded             uint8 = 0x8
	FlagHeadersEndStream       uint8 = 0x1
	FlagHeadersEndHeaders      uint8 = 0x4
	FlagHeadersPadded          uint8 = 0x8
	FlagHeadersPriority        uint8 = 0x20
	FlagPushPromiseEndHeaders  uint8 = 0x4
	FlagPushPromisePadded      uint8 = 0x8
	FlagContinuationEndHeaders uint8 = 0x4
	FlagSettingsAck            uint8 = 0x1
	FlagPingAck                uint8 = 0x1
)

const (
	// FrameHeaderSize is the fixed HTTP/2 frame header length.
	FrameHeaderSize = 9
	// MaxFramePayloadSize is the largest value representable by the 24-bit
	// frame length field.
	MaxFramePayloadSize = 1<<24 - 1
)

// FrameHeader is the raw HTTP/2 frame header. StreamID has the reserved high
// bit cleared.
type FrameHeader struct {
	Length   uint32
	Type     FrameType
	Flags    uint8
	StreamID uint32
	Reserved bool
}

// Frame is a complete raw HTTP/2 frame.
type Frame struct {
	Header  FrameHeader
	Payload []byte
}

// ParseFrameHeader parses the fixed nine-byte HTTP/2 frame header.
func ParseFrameHeader(raw []byte) (FrameHeader, error) {
	var h FrameHeader
	if len(raw) < FrameHeaderSize {
		return h, errors.New("h2h1: truncated frame header")
	}
	h.Length = uint32(raw[0])<<16 | uint32(raw[1])<<8 | uint32(raw[2])
	h.Type = FrameType(raw[3])
	h.Flags = raw[4]
	h.StreamID = binary.BigEndian.Uint32(raw[5:9])
	h.Reserved = h.StreamID&0x80000000 != 0
	h.StreamID &= 0x7fffffff
	return h, nil
}

// ParseFrameBytes parses one complete frame from raw and returns the
// unconsumed suffix. It never silently discards a partial frame.
func ParseFrameBytes(raw []byte) (Frame, []byte, error) {
	h, err := ParseFrameHeader(raw)
	if err != nil {
		return Frame{}, raw, err
	}
	n := int(h.Length)
	if len(raw) < FrameHeaderSize+n {
		return Frame{}, raw, fmt.Errorf("h2h1: truncated %s frame on stream %d: want %d payload bytes, have %d",
			h.Type, h.StreamID, n, len(raw)-FrameHeaderSize)
	}
	payload := make([]byte, n)
	copy(payload, raw[FrameHeaderSize:FrameHeaderSize+n])
	return Frame{Header: h, Payload: payload}, raw[FrameHeaderSize+n:], nil
}

// ParseFrame parses exactly one frame and rejects any trailing bytes.
func ParseFrame(raw []byte) (Frame, error) {
	f, rest, err := ParseFrameBytes(raw)
	if err != nil {
		return Frame{}, err
	}
	if len(rest) != 0 {
		return Frame{}, fmt.Errorf("h2h1: %d trailing bytes after frame", len(rest))
	}
	return f, nil
}

// ParseAllFrames parses a complete byte stream containing only frames. A
// trailing partial frame is an error.
func ParseAllFrames(raw []byte) ([]Frame, error) {
	var frames []Frame
	rest := raw
	for len(rest) > 0 {
		f, next, err := ParseFrameBytes(rest)
		if err != nil {
			return nil, err
		}
		frames = append(frames, f)
		rest = next
	}
	return frames, nil
}

// ParseFrames is an alias for ParseAllFrames.
func ParseFrames(raw []byte) ([]Frame, error) {
	return ParseAllFrames(raw)
}

// ParseFrameStream reads frames until EOF. The caller can stop a stream with
// an error; io.EOF is returned only after a clean frame boundary.
func ParseFrameStream(r io.Reader) ([]Frame, error) {
	var frames []Frame
	for {
		f, err := ReadFrame(r)
		if errors.Is(err, io.EOF) {
			return frames, nil
		}
		if err != nil {
			return nil, err
		}
		frames = append(frames, *f)
	}
}

// ReadFrames is an alias for ParseFrameStream.
func ReadFrames(r io.Reader) ([]Frame, error) {
	return ParseFrameStream(r)
}

// ReadFrame reads exactly one frame. It returns io.EOF only if the reader ends
// before a frame header starts.
func ReadFrame(r io.Reader) (*Frame, error) {
	var hb [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, hb[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errors.New("h2h1: truncated frame header")
		}
		return nil, err
	}
	h, err := ParseFrameHeader(hb[:])
	if err != nil {
		return nil, err
	}
	payload := make([]byte, int(h.Length))
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("h2h1: truncated %s frame on stream %d", h.Type, h.StreamID)
		}
		return nil, err
	}
	return &Frame{Header: h, Payload: payload}, nil
}

// FrameStreamDecoder is a small incremental raw-frame reader.
type FrameStreamDecoder struct {
	r io.Reader
}

func NewFrameStreamDecoder(r io.Reader) *FrameStreamDecoder {
	return &FrameStreamDecoder{r: r}
}

func (d *FrameStreamDecoder) Next() (*Frame, error) {
	return ReadFrame(d.r)
}

// BuildFrame constructs a raw frame. Length is derived from payload.
func BuildFrame(frameType FrameType, flags uint8, streamID uint32, payload []byte) (Frame, error) {
	if streamID&0x80000000 != 0 {
		return Frame{}, errors.New("h2h1: stream id exceeds 31 bits")
	}
	if len(payload) > MaxFramePayloadSize {
		return Frame{}, errors.New("h2h1: frame payload exceeds 24-bit length field")
	}
	p := make([]byte, len(payload))
	copy(p, payload)
	return Frame{
		Header: FrameHeader{
			Length:   uint32(len(p)),
			Type:     frameType,
			Flags:    flags,
			StreamID: streamID,
		},
		Payload: p,
	}, nil
}

// Bytes serialises the frame including its nine-byte header.
func (f Frame) Bytes() []byte {
	raw := make([]byte, FrameHeaderSize+len(f.Payload))
	raw[0] = byte(len(f.Payload) >> 16)
	raw[1] = byte(len(f.Payload) >> 8)
	raw[2] = byte(len(f.Payload))
	raw[3] = byte(f.Header.Type)
	raw[4] = f.Header.Flags
	binary.BigEndian.PutUint32(raw[5:9], f.Header.StreamID&0x7fffffff)
	copy(raw[FrameHeaderSize:], f.Payload)
	return raw
}

// MarshalFrames serialises a frame sequence.
func MarshalFrames(frames []Frame) []byte {
	var out []byte
	for _, f := range frames {
		out = append(out, f.Bytes()...)
	}
	return out
}

// DataBody returns the DATA frame's octets with padding removed.
func (f Frame) DataBody() ([]byte, error) {
	if f.Header.Type != FrameData {
		return nil, fmt.Errorf("h2h1: frame %s is not DATA", f.Header.Type)
	}
	if f.Header.Flags&FlagDataPadded == 0 {
		return f.Payload, nil
	}
	if len(f.Payload) == 0 {
		return nil, errors.New("h2h1: padded DATA frame has no pad length")
	}
	pad := int(f.Payload[0])
	if pad >= len(f.Payload) {
		return nil, errors.New("h2h1: DATA padding consumes the whole payload")
	}
	return f.Payload[1 : len(f.Payload)-pad], nil
}

// HeaderBlockFragment extracts the HPACK block from a HEADERS or PUSH_PROMISE
// frame, removing the pad length and priority fields.
func (f Frame) HeaderBlockFragment() ([]byte, error) {
	var paddedFlag, priorityFlag uint8
	switch f.Header.Type {
	case FrameHeaders:
		paddedFlag, priorityFlag = FlagHeadersPadded, FlagHeadersPriority
	case FramePushPromise:
		paddedFlag = FlagPushPromisePadded
	default:
		return nil, fmt.Errorf("h2h1: frame %s has no header block", f.Header.Type)
	}
	payload := f.Payload
	var pad int
	if f.Header.Flags&paddedFlag != 0 {
		if len(payload) == 0 {
			return nil, errors.New("h2h1: padded HEADERS frame has no pad length")
		}
		pad = int(payload[0])
		payload = payload[1:]
	}
	if f.Header.Flags&priorityFlag != 0 {
		if len(payload) < 5 {
			return nil, errors.New("h2h1: HEADERS priority field is truncated")
		}
		payload = payload[5:]
	}
	if f.Header.Type == FramePushPromise {
		if len(payload) < 4 {
			return nil, errors.New("h2h1: PUSH_PROMISE promised stream id is truncated")
		}
		payload = payload[4:]
	}
	if pad > len(payload) {
		return nil, errors.New("h2h1: HEADERS padding exceeds payload")
	}
	return payload[:len(payload)-pad], nil
}

// Setting is one SETTINGS frame entry.
type Setting struct {
	ID    uint16
	Value uint32
}

const (
	SettingHeaderTableSize       uint16 = 0x1
	SettingEnablePush            uint16 = 0x2
	SettingMaxConcurrentStreams  uint16 = 0x3
	SettingInitialWindowSize     uint16 = 0x4
	SettingMaxFrameSize          uint16 = 0x5
	SettingMaxHeaderListSize     uint16 = 0x6
	SettingEnableConnectProtocol uint16 = 0x8
)

// ParseSettings validates and decodes a SETTINGS payload (excluding ACK).
func ParseSettings(payload []byte) ([]Setting, error) {
	if len(payload)%6 != 0 {
		return nil, errors.New("h2h1: SETTINGS payload is not a multiple of six bytes")
	}
	settings := make([]Setting, 0, len(payload)/6)
	for i := 0; i < len(payload); i += 6 {
		id := binary.BigEndian.Uint16(payload[i : i+2])
		value := binary.BigEndian.Uint32(payload[i+2 : i+6])
		switch id {
		case SettingHeaderTableSize:
		case SettingEnablePush:
			if value > 1 {
				return nil, fmt.Errorf("h2h1: invalid SETTINGS_ENABLE_PUSH value %d", value)
			}
		case SettingInitialWindowSize:
			if value > 0x7fffffff {
				return nil, fmt.Errorf("h2h1: invalid initial window size %d", value)
			}
		case SettingMaxFrameSize:
			if value < 16384 || value > MaxFramePayloadSize {
				return nil, fmt.Errorf("h2h1: invalid maximum frame size %d", value)
			}
		default:
			// Unknown settings must be ignored by HTTP/2 endpoints.
		}
		settings = append(settings, Setting{ID: id, Value: value})
	}
	return settings, nil
}
