package proxy

import (
	"encoding/binary"
	"fmt"
	"io"

	"example.org/offlinecli/hpack"
)

// ErrCode is an HTTP/2 error code (RFC 7540 section 7).
type ErrCode uint32

const (
	ErrCodeProtocol ErrCode = 0x1
	ErrCodeCompress ErrCode = 0x9
)

// ConnectionError terminates the whole connection.
type ConnectionError struct {
	Code ErrCode
	Msg  string
}

func (e ConnectionError) Error() string {
	return fmt.Sprintf("proxy: connection error 0x%x: %s", uint32(e.Code), e.Msg)
}

// Message is a fully received request header block.
type Message struct {
	StreamID  uint32
	Fields    []HeaderField
	EndStream bool
}

// Receiver decodes frames from a shared upstream connection. Header blocks
// are reassembled across CONTINUATION frames; any frame interleaved into a
// partially received header block is a connection-level protocol error.
type Receiver struct {
	r        io.Reader
	dec      *hpack.Decoder
	maxFrame uint32

	// OnPeerSettings is called for each non-ACK SETTINGS frame with the
	// peer's HEADER_TABLE_SIZE (if present). The caller should forward it
	// to Session.ApplyPeerSettings so the reduction is acknowledged.
	OnPeerSettings func(headerTableSize uint32, present bool)
	// OnSettingsAck is called when the peer acknowledges our SETTINGS.
	OnSettingsAck func()
	// OnPing is called for each PING frame.
	OnPing func(data [8]byte, ack bool)
}

// NewReceiver creates a Receiver that rejects HPACK dynamic table size
// updates larger than maxDynamicTableSize.
func NewReceiver(r io.Reader, maxDynamicTableSize uint32) *Receiver {
	return &Receiver{
		r:        r,
		dec:      hpack.NewDecoder(maxDynamicTableSize),
		maxFrame: defaultMaxFrameSize,
	}
}

// SetMaxFrameSize sets the largest accepted frame payload.
func (rc *Receiver) SetMaxFrameSize(n uint32) {
	rc.maxFrame = n
}

// Next reads frames until a complete request header block has been
// reassembled and decoded. Control frames (SETTINGS, PING) are handled via
// callbacks and skipped.
func (rc *Receiver) Next() (*Message, error) {
	for {
		h, payload, err := readFrame(rc.r, rc.maxFrame)
		if err != nil {
			return nil, err
		}
		switch h.Type {
		case FrameSettings:
			if err := rc.handleSettings(h, payload); err != nil {
				return nil, err
			}
		case FramePing:
			rc.handlePing(h, payload)
		case FrameHeaders:
			return rc.readHeaderBlock(h, payload)
		default:
			// Unknown and unrelated frame types are ignored
			// (RFC 7540 section 5.5).
		}
	}
}

func (rc *Receiver) handleSettings(h FrameHeader, payload []byte) error {
	if h.StreamID != 0 {
		return ConnectionError{ErrCodeProtocol, "SETTINGS on non-zero stream"}
	}
	if h.Flags&FlagAck != 0 {
		if len(payload) != 0 {
			return ConnectionError{ErrCodeProtocol, "SETTINGS ACK with payload"}
		}
		if rc.OnSettingsAck != nil {
			rc.OnSettingsAck()
		}
		return nil
	}
	if len(payload)%6 != 0 {
		return ConnectionError{ErrCodeProtocol, "malformed SETTINGS payload"}
	}
	var tableSize uint32
	present := false
	for i := 0; i+6 <= len(payload); i += 6 {
		id := binary.BigEndian.Uint16(payload[i:])
		val := binary.BigEndian.Uint32(payload[i+2:])
		if id == SettingHeaderTableSize {
			tableSize = val
			present = true
		}
	}
	if rc.OnPeerSettings != nil {
		rc.OnPeerSettings(tableSize, present)
	}
	return nil
}

func (rc *Receiver) handlePing(h FrameHeader, payload []byte) {
	if rc.OnPing == nil || len(payload) != 8 {
		return
	}
	var data [8]byte
	copy(data[:], payload)
	rc.OnPing(data, h.Flags&FlagAck != 0)
}

// readHeaderBlock reassembles a header block. While a block is partially
// received, only CONTINUATION frames for the same stream are legal; any
// other frame is a protocol error.
func (rc *Receiver) readHeaderBlock(h FrameHeader, payload []byte) (*Message, error) {
	if h.StreamID == 0 {
		return nil, ConnectionError{ErrCodeProtocol, "HEADERS on stream 0"}
	}
	if h.Flags&(FlagPadded|FlagPriority) != 0 {
		return nil, ConnectionError{ErrCodeProtocol, "unsupported HEADERS padding/priority"}
	}
	block := append([]byte(nil), payload...)
	endStream := h.Flags&FlagEndStream != 0
	for h.Flags&FlagEndHeaders == 0 {
		next, p, err := readFrame(rc.r, rc.maxFrame)
		if err != nil {
			return nil, err
		}
		if next.Type != FrameContinuation || next.StreamID != h.StreamID {
			return nil, ConnectionError{
				ErrCodeProtocol,
				fmt.Sprintf("frame type 0x%x interleaved into header block on stream %d", next.Type, h.StreamID),
			}
		}
		block = append(block, p...)
		h.Flags = next.Flags
	}
	fields, err := rc.dec.Decode(block)
	if err != nil {
		return nil, ConnectionError{ErrCodeCompress, err.Error()}
	}
	return &Message{StreamID: h.StreamID, Fields: fields, EndStream: endStream}, nil
}
