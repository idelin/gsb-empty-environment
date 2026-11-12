package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
)

// Validation errors. They are returned wrapped; callers should use errors.Is.
var (
	// ErrPseudoOrder is returned when a regular header appears before all
	// pseudo-headers, or a pseudo-header appears after the regular section.
	ErrPseudoOrder = errors.New("netsec: pseudo-header ordering violation")
	// ErrDuplicatePseudo is returned when a request pseudo-header occurs more
	// than once.
	ErrDuplicatePseudo = errors.New("netsec: duplicate pseudo-header")
	// ErrUnknownPseudo is returned for pseudo-headers other than the four
	// request pseudo-headers defined by RFC 7540.
	ErrUnknownPseudo = errors.New("netsec: unknown pseudo-header")
	// ErrConnectionField is returned for headers that are hop-by-hop and must
	// not cross an HTTP/2 to HTTP/1 boundary.
	ErrConnectionField = errors.New("netsec: connection-specific header forbidden")
	// ErrAuthorityMismatch is returned when :authority and Host disagree.
	ErrAuthorityMismatch = errors.New("netsec: :authority and Host do not match")
	// ErrMissingPseudo is returned when a required request pseudo-header is
	// absent.
	ErrMissingPseudo = errors.New("netsec: missing required pseudo-header")
	// ErrTrailer is returned when a trailer section tries to smuggle a
	// pseudo-header, connection-specific field or length declaration.
	ErrTrailer = errors.New("netsec: invalid trailer field")
	// ErrLengthMismatch is returned when the declared Content-Length does not
	// equal the number of DATA frame octets actually received.
	ErrLengthMismatch = errors.New("netsec: content-length does not match received body")
	// ErrStreamState is returned for frames illegal in the current stream
	// state (for example DATA after END_STREAM, or a second trailer).
	ErrStreamState = errors.New("netsec: illegal frame for stream state")
)

// MaxBodySize caps the buffered request body (64 MiB).
const MaxBodySize = 64 << 20

type streamState uint8

const (
	stateIdle streamState = iota
	stateOpen             // headers received, DATA/trailer expected
	stateHalfClosed
	stateClosed
	stateError
)

// h2Field is one validated regular header entry in wire order.
type h2Field struct {
	Name  string
	Value string
}

// Converter turns one HTTP/2 request stream into a validated HTTP/1 request.
// A Converter is bound to a single stream and is not safe for concurrent use.
// The downstream request is only made available after the whole stream has
// been received and every validation rule has passed.
type Converter struct {
	streamID uint32
	decoder  *hpackDecoder
	state    streamState

	pseudo  map[string]string
	headers []h2Field // regular request headers, wire order
	trailer http.Header

	body        bytes.Buffer
	declaredLen int64 // -1 when no Content-Length was received
	hasLength   bool

	block          bytes.Buffer // accumulated HEADERS/CONTINUATION bytes
	pendingBlock   bool         // a header block is being assembled
	blockStreamID  uint32
	blockIsTrailer bool
	blockEndStream bool
	ready          bool
	request        *http.Request
	failure        error
	failureCode    uint32
}

// NewConverter starts a conversion for streamID. maxDynamicTable bounds the
// HPACK dynamic table size advertised via SETTINGS_HEADER_TABLE_SIZE; pass 0
// to forbid dynamic table use.
func NewConverter(streamID uint32, maxDynamicTable uint32) *Converter {
	return &Converter{
		streamID:    streamID,
		decoder:     newHPACKDecoder(int(maxDynamicTable), DefaultMaxHeaderListSize),
		state:       stateIdle,
		pseudo:      make(map[string]string),
		declaredLen: -1,
		failureCode: ErrCodeProtocol,
	}
}

// HandleFrame feeds one raw HTTP/2 frame to the converter. Connection-level
// frames (SETTINGS/PING/GOAWAY/WINDOW_UPDATE and unknown types) are ignored by
// the stream converter. A non-nil error is terminal: the caller must send
// RST_STREAM with ErrorCode and stop using the converter.
func (c *Converter) HandleFrame(f *Frame) error {
	if c.state == stateError {
		return c.failure
	}
	if f.Stream != 0 && f.Stream != c.streamID {
		// Frames for other streams are the caller's responsibility.
		return nil
	}
	switch f.Type {
	case FrameData:
		return c.handleData(f)
	case FrameHeaders:
		return c.handleHeaders(f)
	case FrameContinuation:
		return c.handleContinuation(f)
	case FrameRSTStream:
		c.state = stateClosed
		return nil
	case FrameSettings, FramePing, FrameGoAway, FrameWindowUpdate:
		return nil
	default:
		// Unknown frame types are ignored per RFC 7540 section 5.1.
		return nil
	}
}

// Err returns the terminal validation error, if any.
func (c *Converter) Err() error { return c.failure }

// ErrorCode is the RST_STREAM error code to send alongside Err.
func (c *Converter) ErrorCode() uint32 { return c.failureCode }

// Ready reports whether the complete, validated HTTP/1 request can be taken.
func (c *Converter) Ready() bool { return c.ready }

// Request returns the validated downstream request. It must only be called
// after Ready returns true.
func (c *Converter) Request() *http.Request {
	if !c.ready {
		return nil
	}
	return c.request
}

func (c *Converter) fail(code uint32, err error) error {
	c.state = stateError
	c.failure = err
	c.failureCode = code
	c.ready = false
	c.request = nil
	return err
}

func (c *Converter) handleHeaders(f *Frame) error {
	if c.pendingBlock {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: HEADERS while header block incomplete", ErrStreamState))
	}
	if c.state == stateHalfClosed || c.state == stateClosed {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: HEADERS after END_STREAM", ErrStreamState))
	}
	fragment, err := f.HeaderBlockFragment()
	if err != nil {
		return c.fail(ErrCodeProtocol, err)
	}
	c.block.Reset()
	c.block.Write(fragment)
	c.blockStreamID = f.Stream
	// The first HEADERS block opens the stream; a later block is the trailer.
	c.blockIsTrailer = c.state == stateOpen
	c.blockEndStream = f.Flags.EndStream()
	c.pendingBlock = true
	if f.Flags.EndHeaders() {
		return c.finishBlock(f.Flags.EndStream())
	}
	return nil
}

func (c *Converter) handleContinuation(f *Frame) error {
	if !c.pendingBlock {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: CONTINUATION without HEADERS", ErrStreamState))
	}
	if f.Stream != c.blockStreamID {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: CONTINUATION on wrong stream", ErrStreamState))
	}
	c.block.Write(f.Payload)
	if f.Flags.EndHeaders() {
		return c.finishBlock(c.blockEndStream)
	}
	return nil
}

func (c *Converter) finishBlock(endStream bool) error {
	c.pendingBlock = false
	fields, err := c.decoder.Decode(c.block.Bytes())
	if err != nil {
		return c.fail(ErrCodeCompression, err)
	}
	isTrailer := c.blockIsTrailer
	c.block.Reset()
	if isTrailer {
		if err := c.validateTrailer(fields); err != nil {
			return c.fail(ErrCodeProtocol, err)
		}
	} else {
		if err := c.validateHeaders(fields); err != nil {
			return c.fail(ErrCodeProtocol, err)
		}
		c.state = stateOpen
	}
	if endStream {
		return c.complete()
	}
	return nil
}

func (c *Converter) handleData(f *Frame) error {
	if c.pendingBlock {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: DATA while header block incomplete", ErrStreamState))
	}
	if c.state != stateOpen {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: DATA outside open stream", ErrStreamState))
	}
	payload, endStream, err := f.DataPayload()
	if err != nil {
		return c.fail(ErrCodeProtocol, err)
	}
	if c.body.Len()+len(payload) > MaxBodySize {
		return c.fail(ErrCodeInternal, errors.New("netsec: request body exceeds size limit"))
	}
	// Length is accumulated from actual DATA frame payloads only; HPACK padding
	// and any declared Content-Length cannot influence the count.
	c.body.Write(payload)
	if endStream {
		return c.complete()
	}
	return nil
}

func (c *Converter) complete() error {
	if c.state == stateHalfClosed || c.state == stateClosed {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: repeated END_STREAM", ErrStreamState))
	}
	if c.hasLength && c.body.Len() != int(c.declaredLen) {
		return c.fail(ErrCodeProtocol, fmt.Errorf("%w: declared %d, received %d",
			ErrLengthMismatch, c.declaredLen, c.body.Len()))
	}
	req, err := c.buildRequest()
	if err != nil {
		return c.fail(ErrCodeProtocol, err)
	}
	c.request = req
	c.ready = true
	c.state = stateHalfClosed
	return nil
}
