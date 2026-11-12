// Package netsec reassembles HTTP/2 header block fragments carried by
// HEADERS and CONTINUATION frames before handing the complete block to an
// HPACK decoder supplied by the receiving host.
//
// HTTP/2 requires that a HEADERS frame without END_HEADERS be followed on
// the same connection by CONTINUATION frames for the same stream until
// END_HEADERS is seen; no other frame may intervene (RFC 9113 section
// 8.2). An assembler enforces that rule, applies separate limits to the
// accumulated block bytes and to the number of decoded header fields, and
// discards every partial block when a stream or the whole connection is
// torn down, so a half block can never be reused by a later request.
package netsec

import (
	"errors"
	"fmt"

	"example.org/netsec/q0122/hpack"
)

// FrameType classifies the frames the assembler understands. HEADERS and
// CONTINUATION carry header blocks; FrameReset models RST_STREAM so the
// assembler can release a half block without misreading it as an illegal
// interleave. Every other wire frame type maps to FrameOther.
type FrameType uint8

const (
	FrameHeaders FrameType = iota + 1
	FrameContinuation
	// FrameReset represents RST_STREAM for the frame's stream. It is a
	// legal frame even while a header block is open: the open partial
	// block for that stream is discarded without a connection error.
	FrameReset
	// FrameOther covers every frame type this package does not consume
	// (DATA, SETTINGS, WINDOW_UPDATE, ...). While a header block is open
	// any such frame is an interleave violation; otherwise it is ignored.
	FrameOther
)

// Frame is the host's parsed view of a HEADERS or CONTINUATION frame.
//
// Payload is the raw header block fragment only: the host strips padding,
// stream dependency, weight and EDC bytes before calling HandleFrame.
// EndHeaders mirrors the HTTP/2 END_HEADERS flag (0x4); the END_STREAM flag
// (0x1) is irrelevant to assembly and intentionally absent.
type Frame struct {
	Type       FrameType
	StreamID   uint32
	Payload    []byte
	EndHeaders bool
}

// Field is one decoded header field, re-exported so callers do not need to
// import the internal HPACK package directly.
type Field = hpack.Field

// Limits bounds one reassembled header block. The two limits are enforced
// independently:
//
//   - MaxBlockBytes caps the concatenated frame payload bytes, guarding
//     memory before HPACK decoding runs.
//   - MaxFields caps the number of decoded header fields, guarding work and
//     slice growth after HPACK expansion.
//
// A zero value means "no limit"; NewDefaultLimits provides conservative
// production defaults.
type Limits struct {
	MaxBlockBytes int
	MaxFields     int
}

// Default limits, chosen well above typical browser requests but small
// enough to stop abuse early.
const (
	defaultMaxBlockBytes = 1 << 16 // 64 KiB
	defaultMaxFields     = 128
)

// NewDefaultLimits returns the built-in conservative limit set.
func NewDefaultLimits() Limits {
	return Limits{MaxBlockBytes: defaultMaxBlockBytes, MaxFields: defaultMaxFields}
}

// Decoder decodes one complete HPACK header block. *hpack.Decoder satisfies
// this interface; tests can supply a fake to inject decoding failures.
type Decoder interface {
	DecodeFull(block []byte) ([]hpack.Field, error)
}

// Result is published for a frame sequence terminated by END_HEADERS.
type Result struct {
	StreamID uint32
	Fields   []Field
	// BlockBytes is the exact number of reassembled payload bytes.
	BlockBytes int
}

var (
	// ErrConnectionClosed is returned after Close or a fatal connection
	// error: the assembler stops publishing results permanently.
	ErrConnectionClosed = errors.New("netsec: header assembler connection is closed")
	// ErrInterleave is the connection error for any frame that appears
	// while a header block is still open, including a CONTINUATION on a
	// different stream or an unrelated frame type.
	ErrInterleave = errors.New("netsec: frame interleaved inside an unfinished header block")
	// ErrContinuationWithoutHeaders is returned for a CONTINUATION that is
	// not continuing an open header block.
	ErrContinuationWithoutHeaders = errors.New("netsec: CONTINUATION without preceding HEADERS")
	// ErrContinuationStreamMismatch marks a CONTINUATION whose stream id
	// differs from the open HEADERS stream.
	ErrContinuationStreamMismatch = errors.New("netsec: CONTINUATION stream id does not match HEADERS")
	// ErrOpenBlockInterrupted reports RST_STREAM or any other legal frame
	// that tears down the stream while its header block is still open.
	ErrOpenBlockInterrupted = errors.New("netsec: open header block interrupted by stream reset")
	// ErrBlockTooLarge reports a block whose accumulated bytes exceed
	// MaxBlockBytes.
	ErrBlockTooLarge = errors.New("netsec: header block exceeds byte limit")
	// ErrTooManyFields reports a block decoding to more than MaxFields.
	ErrTooManyFields = errors.New("netsec: header block exceeds field count limit")
)

// ErrorCode classifies a failure so the host can choose between sending a
// GOAWAY (connection-level) or RST_STREAM (stream-level).
type ErrorCode int

const (
	// NoError is returned with a nil error.
	NoError ErrorCode = iota
	// ProtocolError requires terminating the whole HTTP/2 connection.
	ProtocolError
	// StreamError only invalidates the affected stream.
	StreamError
	// ClosedError means the assembler was already shut down.
	ClosedError
)

// FrameError attaches an ErrorCode and the offending stream id to an error.
type FrameError struct {
	Code     ErrorCode
	StreamID uint32
	Err      error
}

func (e *FrameError) Error() string {
	return fmt.Sprintf("netsec: stream %d: %v", e.StreamID, e.Err)
}

func (e *FrameError) Unwrap() error { return e.Err }

func protocolError(streamID uint32, err error) *FrameError {
	return &FrameError{Code: ProtocolError, StreamID: streamID, Err: err}
}

func streamError(streamID uint32, err error) *FrameError {
	return &FrameError{Code: StreamError, StreamID: streamID, Err: err}
}

// partialBlock is the open accumulation state for one stream.
type partialBlock struct {
	streamID uint32
	buf      []byte
}

// Assembler enforces HEADERS/CONTINUATION exclusivity for one connection.
// Methods are not safe for concurrent use; an HTTP/2 frame read loop is
// expected to drive them from a single goroutine.
type Assembler struct {
	decoder Decoder
	limits  Limits

	open      *partialBlock
	closed    bool
	published []Result
}

// New creates an Assembler bound to the host's HPACK decoder and limits.
func New(decoder Decoder, limits Limits) *Assembler {
	return &Assembler{decoder: decoder, limits: limits}
}

// HasOpenBlock reports whether the assembler is waiting for CONTINUATION
// frames. It is mainly useful for host-side diagnostics and tests.
func (a *Assembler) HasOpenBlock() bool { return a.open != nil }

// OpenStreamID returns the stream whose header block is open, or 0.
func (a *Assembler) OpenStreamID() uint32 {
	if a.open == nil {
		return 0
	}
	return a.open.streamID
}

// HandleFrame accepts one parsed frame at a time in connection order.
//
// A non-nil result is published exactly once, on the frame carrying
// END_HEADERS. Every protocol violation returns a ProtocolError: the host
// must send GOAWAY and stop feeding frames, because the assembler closes
// itself and will reject all further input with ErrConnectionClosed.
// Stream-level failures (oversized block, malformed HPACK) return a
// StreamError and drop only that stream's partial block.
func (a *Assembler) HandleFrame(f Frame) (*Result, error) {
	if a.closed {
		return nil, &FrameError{Code: ClosedError, StreamID: f.StreamID, Err: ErrConnectionClosed}
	}
	switch f.Type {
	case FrameHeaders:
		return a.handleHeaders(f)
	case FrameContinuation:
		return a.handleContinuation(f)
	case FrameReset:
		// RFC 9113: RST_STREAM terminates the stream immediately. If that
		// stream owns the open block, drop the half block; a reset for
		// another stream while a block is open cannot interleave either,
		// because only CONTINUATION on the open stream may appear.
		if a.open != nil && a.open.streamID == f.StreamID {
			a.dropOpen()
			return nil, streamError(f.StreamID, ErrOpenBlockInterrupted)
		}
		if a.open != nil {
			return nil, a.failConnection(f.StreamID, ErrInterleave)
		}
		return nil, nil
	default:
		// Any other frame type while a block is open violates exclusivity;
		// outside of an open block it is simply not this package's job.
		if a.open != nil {
			return nil, a.failConnection(f.StreamID, ErrInterleave)
		}
		return nil, nil
	}
}

func (a *Assembler) handleHeaders(f Frame) (*Result, error) {
	if a.open != nil {
		// A second HEADERS before END_HEADERS is itself an interleave.
		return nil, a.failConnection(f.StreamID, ErrInterleave)
	}
	a.open = &partialBlock{streamID: f.StreamID}
	if err := a.append(f); err != nil {
		return nil, err
	}
	if f.EndHeaders {
		return a.finish()
	}
	return nil, nil
}

func (a *Assembler) handleContinuation(f Frame) (*Result, error) {
	if a.open == nil {
		return nil, a.failConnection(f.StreamID, ErrContinuationWithoutHeaders)
	}
	if f.StreamID != a.open.streamID {
		return nil, a.failConnection(f.StreamID, ErrContinuationStreamMismatch)
	}
	if err := a.append(f); err != nil {
		return nil, err
	}
	if !f.EndHeaders {
		return nil, nil
	}
	return a.finish()
}

// append enforces the byte limit while copying the fragment. The copy keeps
// the assembler independent of the host reusing its frame buffers.
func (a *Assembler) append(f Frame) error {
	if a.limits.MaxBlockBytes > 0 &&
		len(a.open.buf)+len(f.Payload) > a.limits.MaxBlockBytes {
		a.dropOpen()
		return streamError(f.StreamID, ErrBlockTooLarge)
	}
	a.open.buf = append(a.open.buf, f.Payload...)
	return nil
}

// finish decodes the complete block, enforces the field limit, publishes the
// result and clears the open state. On failure the partial block is dropped.
func (a *Assembler) finish() (*Result, error) {
	block := a.open
	a.open = nil

	fields, err := a.decoder.DecodeFull(block.buf)
	if err != nil {
		return nil, streamError(block.streamID, err)
	}
	if a.limits.MaxFields > 0 && len(fields) > a.limits.MaxFields {
		return nil, streamError(block.streamID, ErrTooManyFields)
	}

	result := &Result{
		StreamID:   block.streamID,
		Fields:     fields,
		BlockBytes: len(block.buf),
	}
	a.published = append(a.published, *result)
	return result, nil
}

// failConnection records a protocol violation, discards all partial state
// and permanently closes the assembler.
func (a *Assembler) failConnection(streamID uint32, err error) *FrameError {
	a.open = nil
	a.closed = true
	return protocolError(streamID, err)
}

// dropOpen discards the open partial block without closing the connection.
func (a *Assembler) dropOpen() {
	a.open = nil
}

// ResetStream drops any partial block for streamID, as required when the
// host sends or receives RST_STREAM. A reset stream must never leave bytes
// behind that a later frame sequence could extend.
func (a *Assembler) ResetStream(streamID uint32) {
	if a.open != nil && a.open.streamID == streamID {
		a.dropOpen()
	}
}

// CloseStream is called when the host closes a stream normally. It behaves
// like ResetStream for assembly purposes: no half block survives.
func (a *Assembler) CloseStream(streamID uint32) {
	a.ResetStream(streamID)
}

// Close tears the whole connection down, discarding every partial block and
// rejecting all later frames. Already published results remain untouched.
func (a *Assembler) Close() {
	a.open = nil
	a.closed = true
}

// PublishedCount reports how many complete header blocks have been emitted.
func (a *Assembler) PublishedCount() int { return len(a.published) }

// PublishedSnapshot returns a defensive copy of completed results, letting a
// test prove that a later failure never rewrites earlier requests.
func (a *Assembler) PublishedSnapshot() []Result {
	out := make([]Result, len(a.published))
	copy(out, a.published)
	return out
}
