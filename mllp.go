package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// MLLP framing bytes.
const (
	StartBlock     byte = 0x0B
	EndBlock       byte = 0x1C
	CarriageReturn byte = 0x0D
)

// Aliases used by some HL7/MLLP integrations.
const (
	MLLPStartBlock = StartBlock
	MLLPEndBlock   = EndBlock
	MLLPCR         = CarriageReturn
)

var (
	// ErrProtocol marks a stream that can no longer be trusted. A caller
	// receiving this error should close the connection.
	ErrProtocol = errors.New("mllp: protocol error")
	// ErrOutsideFrame reports a byte that appeared where a 0x0B start block
	// was required.
	ErrOutsideFrame = errors.New("mllp: data outside a frame")
	// ErrNestedStart reports a second 0x0B inside a frame.
	ErrNestedStart = errors.New("mllp: nested start block")
	// ErrInvalidTerminator reports 0x1C that is not immediately followed by 0x0D.
	ErrInvalidTerminator = errors.New("mllp: end block must be followed by carriage return")
	// ErrFrameTooLong reports a payload larger than the configured limit.
	ErrFrameTooLong = errors.New("mllp: frame exceeds payload limit")
	// ErrTruncatedFrame reports EOF in the middle of a frame or terminator.
	ErrTruncatedFrame = errors.New("mllp: truncated frame")
	// ErrForbiddenByte reports 0x0B or 0x1C in a message, or a raw MLLP
	// delimiter in a stored field.
	ErrForbiddenByte = errors.New("hl7: forbidden control byte")
	// ErrInvalidSegment reports an empty or malformed HL7 segment.
	ErrInvalidSegment = errors.New("hl7: invalid segment")
	// ErrForbiddenEscape reports an HL7 escape sequence that decodes to a
	// structural delimiter.
	ErrForbiddenEscape = errors.New("hl7: escape sequence decodes to a forbidden delimiter")
	// ErrMalformedEscape reports an unterminated or malformed HL7 escape.
	ErrMalformedEscape = errors.New("hl7: malformed escape sequence")
)

// Reader parses MLLP frames from a byte stream. It accepts input in chunks
// whose boundaries may fall at any byte, including between 0x1C and 0x0D.
//
// The size limit counts payload bytes between 0x0B and 0x1C. A limit of zero
// means unlimited. A frame that exceeds the limit is discarded and the reader
// resynchronizes at the next 0x0B.
type Reader struct {
	br              *bufio.Reader
	maxPayloadBytes int

	fatal             error
	discardUntilStart bool
}

// NewReader returns a Reader over r. maxPayloadBytes is an optional payload
// limit; zero or a negative value means unlimited.
func NewReader(r io.Reader, maxPayloadBytes ...int) *Reader {
	limit := 0
	if len(maxPayloadBytes) > 0 {
		limit = maxPayloadBytes[0]
	}
	if limit < 0 {
		limit = 0
	}
	return &Reader{
		br:              bufio.NewReader(r),
		maxPayloadBytes: limit,
	}
}

// NewLimitedReader is an explicit-name alias for NewReader.
func NewLimitedReader(r io.Reader, maxPayloadBytes int) *Reader {
	return NewReader(r, maxPayloadBytes)
}

// SetMaxPayloadBytes changes the payload limit for subsequent frames.
func (r *Reader) SetMaxPayloadBytes(n int) {
	if n < 0 {
		n = 0
	}
	r.maxPayloadBytes = n
}

// ReadFrame returns the message body between 0x0B and 0x1C 0x0D. The
// delimiters are not included.
//
// Protocol violations (bytes outside a frame, a nested 0x0B, or an invalid
// terminator) make the reader permanently fail. An oversized frame is
// discarded and the reader resynchronizes at the next 0x0B so that the caller
// can continue reading.
func (r *Reader) ReadFrame() ([]byte, error) {
	if r.fatal != nil {
		return nil, r.fatal
	}

	started := false
	for !started {
		b, err := r.br.ReadByte()
		if err != nil {
			return nil, streamReadError(err, false)
		}
		if b == StartBlock {
			r.discardUntilStart = false
			started = true
			break
		}
		if r.discardUntilStart {
			continue
		}
		r.fatal = fmt.Errorf("%w: %w: byte %#x before start block", ErrProtocol, ErrOutsideFrame, b)
		return nil, r.fatal
	}

	body := make([]byte, 0, 256)
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return nil, streamReadError(err, true)
		}

		switch b {
		case StartBlock:
			r.fatal = fmt.Errorf("%w: %w: 0x0B at payload offset %d", ErrProtocol, ErrNestedStart, len(body))
			return nil, r.fatal

		case EndBlock:
			next, err := r.br.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("%w: %w: end block without carriage return",
					ErrTruncatedFrame, ErrInvalidTerminator)
			}
			if next != CarriageReturn {
				r.fatal = fmt.Errorf("%w: %w: byte %#x follows 0x1C",
					ErrProtocol, ErrInvalidTerminator, next)
				return nil, r.fatal
			}
			return body, nil

		default:
			if r.maxPayloadBytes > 0 && len(body) >= r.maxPayloadBytes {
				r.discardUntilStart = true
				return nil, fmt.Errorf("%w: limit is %d bytes", ErrFrameTooLong, r.maxPayloadBytes)
			}
			body = append(body, b)
		}
	}
}

// ReadMessage is an alias for ReadFrame.
func (r *Reader) ReadMessage() ([]byte, error) {
	return r.ReadFrame()
}

// ReadFrame reads one frame using a new Reader. The Reader is discarded after
// the call, so this helper is intended for one-shot decoding.
func ReadFrame(r io.Reader, maxPayloadBytes ...int) ([]byte, error) {
	return NewReader(r, maxPayloadBytes...).ReadFrame()
}

// ReadMessage is an alias for ReadFrame.
func ReadMessage(r io.Reader, maxPayloadBytes ...int) ([]byte, error) {
	return ReadFrame(r, maxPayloadBytes...)
}

func streamReadError(err error, inFrame bool) error {
	if errors.Is(err, io.EOF) && inFrame {
		return fmt.Errorf("%w: %w", ErrTruncatedFrame, io.ErrUnexpectedEOF)
	}
	return err
}

// Writer validates and writes complete MLLP frames.
type Writer struct {
	w io.Writer
}

// NewWriter returns a Writer over w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// WriteFrame validates body and writes 0x0B body 0x1C 0x0D. Validation happens
// before any byte is written, so a rejected message never produces a partial
// frame.
func (w *Writer) WriteFrame(body []byte) error {
	framed, err := MarshalFrame(body)
	if err != nil {
		return err
	}
	return writeAll(w.w, framed)
}

// WriteMessage is an alias for WriteFrame.
func (w *Writer) WriteMessage(body []byte) error {
	return w.WriteFrame(body)
}

// WriteFrame validates body and writes one frame to w.
func WriteFrame(w io.Writer, body []byte) error {
	return NewWriter(w).WriteFrame(body)
}

// WriteMessage is an alias for WriteFrame.
func WriteMessage(w io.Writer, body []byte) error {
	return WriteFrame(w, body)
}

// MarshalFrame validates body and returns a complete MLLP frame.
//
// The body must not contain 0x0B or 0x1C. A 0x0D is accepted only as a segment
// separator after a complete segment, or as the final message terminator.
func MarshalFrame(body []byte) ([]byte, error) {
	// Validate the same immutable copy that is placed in the frame so a caller
	// cannot change validated bytes into delimiters before they are written.
	body = bytes.Clone(body)
	if err := ValidateOutboundMessage(body); err != nil {
		return nil, err
	}
	framed := make([]byte, 0, len(body)+3)
	framed = append(framed, StartBlock)
	framed = append(framed, body...)
	framed = append(framed, EndBlock, CarriageReturn)
	return framed, nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
