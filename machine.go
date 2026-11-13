package netsec

import "errors"

// SettingMaxHeaderListSize is the HTTP/2 setting identifier for
// MAX_HEADER_LIST_SIZE (RFC 7540, section 6.5.2).
const SettingMaxHeaderListSize = 0x6

// ErrorCode is an HTTP/2 error code (RFC 7540, section 7).
type ErrorCode uint32

const (
	// ErrCodeProtocol is PROTOCOL_ERROR (0x1).
	ErrCodeProtocol ErrorCode = 0x1
	// ErrCodeEnhanceYourCalm is ENHANCE_YOUR_CALM (0xb).
	ErrCodeEnhanceYourCalm ErrorCode = 0xb
)

// Sentinel errors returned by the state machine. Use errors.Is to match them.
var (
	// ErrHeaderListTooLarge reports that a header block exceeded the
	// MAX_HEADER_LIST_SIZE limit in force for that block. The offending
	// stream fails with ENHANCE_YOUR_CALM.
	ErrHeaderListTooLarge = errors.New("netsec: header list too large")

	// ErrProtocol reports an HTTP/2 framing protocol error, such as a
	// CONTINUATION frame without an active header block.
	ErrProtocol = errors.New("netsec: protocol error")

	// ErrStreamClosed reports an operation on a stream that has already
	// been torn down or that previously failed.
	ErrStreamClosed = errors.New("netsec: stream closed")
)

// StreamError associates an enforcement failure with the affected stream and
// its HTTP/2 error code.
type StreamError struct {
	StreamID uint32
	Code     ErrorCode
	Err      error
}

func (e *StreamError) Error() string {
	return e.Err.Error() + " on stream " + uitoa(e.StreamID)
}

// Unwrap exposes the underlying sentinel error for errors.Is / errors.As.
func (e *StreamError) Unwrap() error { return e.Err }

// Connection holds the per-connection enforcement state. The zero value is not
// usable; create one with NewConnection.
type Connection struct {
	// limit is the currently advertised MAX_HEADER_LIST_SIZE. A zero value
	// means the size is unbounded.
	limit uint64

	// streams tracks the per-stream header block state, keyed by stream id.
	streams map[uint32]*streamState
}

// streamState is the state of a single stream.
type streamState struct {
	// active reports whether a header block is in progress, i.e. a HEADERS
	// frame has been received without its terminating frame.
	active bool

	// size is the number of header bytes received in the current block.
	size uint64

	// blockLimit is the MAX_HEADER_LIST_SIZE value snapshotted when the
	// current block began. It stays fixed for the lifetime of the block even
	// if the setting changes mid-block. Zero means unbounded.
	blockLimit uint64

	// failed is set once the stream has been failed, after which further
	// header frames are rejected.
	failed bool

	// closed reports that the stream has been torn down (RST_STREAM).
	closed bool
}

// NewConnection creates an enforcement state machine. initialLimit is the
// MAX_HEADER_LIST_SIZE value already in effect; pass zero for the default,
// unbounded behavior.
func NewConnection(initialLimit uint64) *Connection {
	return &Connection{
		limit:   initialLimit,
		streams: make(map[uint32]*streamState),
	}
}

// SetMaxHeaderListSize applies a SETTINGS update. The new value only
// constrains header blocks that begin after this call; header blocks already
// in progress keep using the limit that was current when they started. A zero
// value removes the bound.
func (c *Connection) SetMaxHeaderListSize(limit uint64) {
	c.limit = limit
}

// MaxHeaderListSize returns the currently advertised MAX_HEADER_LIST_SIZE.
func (c *Connection) MaxHeaderListSize() uint64 {
	return c.limit
}

func (c *Connection) stream(id uint32) *streamState {
	s, ok := c.streams[id]
	if !ok {
		s = &streamState{}
		c.streams[id] = s
	}
	return s
}

// exceeds reports whether size is over the current limit. A zero limit is
// unbounded.
func exceeds(limit, size uint64) bool {
	return limit != 0 && size > limit
}

// fail marks a stream failed and returns the corresponding StreamError.
func (c *Connection) fail(s *streamState, id uint32, code ErrorCode, sentinel error) error {
	s.failed = true
	s.active = false
	return &StreamError{StreamID: id, Code: code, Err: sentinel}
}

// HeadersFrame processes a HEADERS frame starting a new header block on the
// given stream. payloadLen is the number of header bytes carried by the frame.
// endHeaders reports whether the block ends on this frame (no CONTINUATION
// follows).
func (c *Connection) HeadersFrame(streamID uint32, payloadLen uint64, endHeaders bool) error {
	s := c.stream(streamID)
	if s.failed || s.closed {
		return &StreamError{StreamID: streamID, Code: ErrCodeProtocol, Err: ErrStreamClosed}
	}
	if s.active {
		// Starting a new block while one is still open is a framing error.
		return c.fail(s, streamID, ErrCodeProtocol, ErrProtocol)
	}

	// Snapshot the limit in force at the start of the block.
	s.active = true
	s.size = payloadLen
	s.blockLimit = c.limit

	if exceeds(s.blockLimit, s.size) {
		return c.fail(s, streamID, ErrCodeEnhanceYourCalm, ErrHeaderListTooLarge)
	}

	if endHeaders {
		s.active = false
	}
	return nil
}

// ContinuationFrame processes a CONTINUATION frame extending the header block
// already in progress on the given stream. payloadLen is the number of header
// bytes carried by the frame. endHeaders reports whether the block ends here.
func (c *Connection) ContinuationFrame(streamID uint32, payloadLen uint64, endHeaders bool) error {
	s := c.stream(streamID)
	if s.failed || s.closed {
		return &StreamError{StreamID: streamID, Code: ErrCodeProtocol, Err: ErrStreamClosed}
	}
	if !s.active {
		// CONTINUATION without a preceding HEADERS is a framing error.
		return c.fail(s, streamID, ErrCodeProtocol, ErrProtocol)
	}

	s.size += payloadLen

	// The block is checked against the limit snapshotted at its start, not
	// the current setting.
	if exceeds(s.blockLimit, s.size) {
		return c.fail(s, streamID, ErrCodeEnhanceYourCalm, ErrHeaderListTooLarge)
	}

	if endHeaders {
		s.active = false
	}
	return nil
}

// ResetStream tears down stream state on RST_STREAM.
func (c *Connection) ResetStream(streamID uint32) {
	delete(c.streams, streamID)
}

// HeaderBlockInProgress reports whether the stream currently has a header
// block waiting for more CONTINUATION frames.
func (c *Connection) HeaderBlockInProgress(streamID uint32) bool {
	s, ok := c.streams[streamID]
	return ok && s.active
}

// HeaderBlockSize returns the accumulated size of the header block currently
// in progress on the stream, or zero if none is active.
func (c *Connection) HeaderBlockSize(streamID uint32) uint64 {
	s, ok := c.streams[streamID]
	if !ok || !s.active {
		return 0
	}
	return s.size
}

// StreamFailed reports whether the stream has been failed by this machine.
func (c *Connection) StreamFailed(streamID uint32) bool {
	s, ok := c.streams[streamID]
	return ok && s.failed
}

// Event is an observation delivered by the protocol gateway.
type Event interface {
	eventMarker()
}

// SettingUpdateEvent delivers a SETTINGS frame value for MAX_HEADER_LIST_SIZE.
type SettingUpdateEvent struct {
	MaxHeaderListSize uint64
}

// HeadersEvent delivers a HEADERS frame observed by the gateway.
type HeadersEvent struct {
	StreamID   uint32
	PayloadLen uint64
	EndHeaders bool
}

// ContinuationEvent delivers a CONTINUATION frame observed by the gateway.
type ContinuationEvent struct {
	StreamID   uint32
	PayloadLen uint64
	EndHeaders bool
}

// StreamResetEvent delivers an RST_STREAM frame observed by the gateway.
type StreamResetEvent struct {
	StreamID uint32
}

func (SettingUpdateEvent) eventMarker() {}
func (HeadersEvent) eventMarker()       {}
func (ContinuationEvent) eventMarker()  {}
func (StreamResetEvent) eventMarker()   {}

// HandleEvent feeds a gateway observation into the machine. Setting and reset
// events never return an error; header-bearing events return the same errors
// as the typed frame methods.
func (c *Connection) HandleEvent(ev Event) error {
	switch e := ev.(type) {
	case SettingUpdateEvent:
		c.SetMaxHeaderListSize(e.MaxHeaderListSize)
		return nil
	case HeadersEvent:
		return c.HeadersFrame(e.StreamID, e.PayloadLen, e.EndHeaders)
	case ContinuationEvent:
		return c.ContinuationFrame(e.StreamID, e.PayloadLen, e.EndHeaders)
	case StreamResetEvent:
		c.ResetStream(e.StreamID)
		return nil
	default:
		return errors.New("netsec: unknown event type")
	}
}

// uitoa formats a uint32 without pulling in strconv.
func uitoa(u uint32) string {
	if u == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}
