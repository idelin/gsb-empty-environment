package streamdec

import (
	"fmt"
	"strconv"
)

const (
	// MaxStringLen is the maximum accepted string/field-name length in
	// bytes. A length header exceeding it is rejected before allocation.
	MaxStringLen = 1 << 20
	// MaxDepth is the maximum object nesting depth.
	MaxDepth = 100
	// maxIntDigits bounds the integer token ("-" + up to 19 digits fits int64).
	maxIntDigits = 20
)

// Error is a decoding failure. Offset is the absolute byte offset in the
// stream (counted across all Write calls) of the byte that caused it.
type Error struct {
	Offset int64
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("decode error at offset %d: %s", e.Offset, e.Msg)
}

type state int

const (
	stValue      state = iota // expecting a value
	stFieldOrEnd              // inside an object: expecting a field name or '}'
	stStrLen                  // reading the decimal length header of a string
	stStrBody                 // reading string bytes
	stInt                     // reading integer digits
)

// Decoder is an incremental decoder. It is safe to feed it one byte at a
// time; it pauses and resumes at any byte boundary. A Decoder is not safe
// for concurrent use.
type Decoder struct {
	st    state
	off   int64 // absolute offset of the next byte to consume
	depth int

	strIsField bool // current string is a field name (vs. a value)
	strLen     int  // accumulated length header
	strDigits  bool // saw at least one length digit
	strRemain  int  // string bytes still expected
	strBuf     []byte

	intBuf []byte

	buf     []Event // events of the top-level object currently in progress
	pending []Event // completed batch waiting to be collected by Write

	failed error // sticky until Reset
}

// NewDecoder returns a Decoder ready to consume a stream.
func NewDecoder() *Decoder {
	return &Decoder{st: stValue}
}

// Write consumes p and returns the event batches of every top-level object
// that became complete while consuming it. No events are returned for an
// object that is still incomplete. On error the decoder is left in a failed
// state; call Reset to reuse it.
func (d *Decoder) Write(p []byte) ([]Event, error) {
	if d.failed != nil {
		return nil, d.failed
	}
	var out []Event
	for _, b := range p {
		if err := d.step(b); err != nil {
			d.failed = err
			return nil, err
		}
		d.off++
		if d.pending != nil {
			out = append(out, d.pending...)
			d.pending = nil
		}
	}
	return out, nil
}

// Finish reports whether the stream ended on a clean boundary: no unclosed
// object and no truncated token.
func (d *Decoder) Finish() error {
	if d.failed != nil {
		return d.failed
	}
	if d.depth > 0 {
		return &Error{d.off, "end of input with unclosed object"}
	}
	switch d.st {
	case stValue:
		return nil
	case stStrLen, stStrBody:
		return &Error{d.off, "end of input in the middle of a string"}
	case stInt:
		return &Error{d.off, "end of input in the middle of an integer"}
	}
	return &Error{d.off, "end of input in an incomplete value"}
}

// Reset cancels the current partial object and releases its cached events
// and buffers, making the decoder ready for a fresh stream position. The
// absolute offset keeps counting so later errors still report stream
// offsets.
func (d *Decoder) Reset() {
	d.st = stValue
	d.depth = 0
	d.strIsField = false
	d.strLen = 0
	d.strDigits = false
	d.strRemain = 0
	d.strBuf = nil
	d.intBuf = nil
	d.buf = nil
	d.pending = nil
	d.failed = nil
}

func (d *Decoder) errf(format string, args ...any) *Error {
	return &Error{Offset: d.off, Msg: fmt.Sprintf(format, args...)}
}

func (d *Decoder) emit(e Event) { d.buf = append(d.buf, e) }

// valueDone is called when a complete value has been emitted.
func (d *Decoder) valueDone() {
	if d.depth == 0 {
		// A top-level value is complete: publish the whole batch.
		d.pending = d.buf
		d.buf = nil
		d.st = stValue
		return
	}
	d.emit(Event{Kind: EventFieldEnd})
	d.st = stFieldOrEnd
}

func (d *Decoder) stringDone(s string) {
	d.strBuf = nil
	if d.strIsField {
		d.emit(Event{Kind: EventFieldStart, Name: s})
		d.st = stValue
		return
	}
	d.emit(Event{Kind: EventString, Str: s})
	d.valueDone()
}

func (d *Decoder) step(b byte) error {
	switch d.st {
	case stValue:
		switch b {
		case '{':
			d.depth++
			if d.depth > MaxDepth {
				return d.errf("nesting depth exceeds maximum of %d", MaxDepth)
			}
			d.emit(Event{Kind: EventObjectStart})
			d.st = stFieldOrEnd
		case 'S':
			d.strIsField = false
			d.strLen, d.strDigits = 0, false
			d.st = stStrLen
		case 'I':
			d.intBuf = d.intBuf[:0]
			d.st = stInt
		case 'T':
			d.emit(Event{Kind: EventBool, Bool: true})
			d.valueDone()
		case 'F':
			d.emit(Event{Kind: EventBool, Bool: false})
			d.valueDone()
		case 'N':
			d.emit(Event{Kind: EventNull})
			d.valueDone()
		default:
			return d.errf("unexpected byte %q, expected a value", b)
		}
	case stFieldOrEnd:
		switch b {
		case '}':
			d.depth--
			d.emit(Event{Kind: EventObjectEnd})
			d.valueDone()
		case 'S':
			d.strIsField = true
			d.strLen, d.strDigits = 0, false
			d.st = stStrLen
		default:
			return d.errf("unexpected byte %q, expected a field name or '}'", b)
		}
	case stStrLen:
		switch {
		case b >= '0' && b <= '9':
			d.strDigits = true
			d.strLen = d.strLen*10 + int(b-'0')
			if d.strLen > MaxStringLen {
				return d.errf("string length exceeds maximum of %d bytes", MaxStringLen)
			}
		case b == ':':
			if !d.strDigits {
				return d.errf("string length header has no digits")
			}
			d.strRemain = d.strLen
			if d.strRemain == 0 {
				d.stringDone("")
			} else {
				// Length is validated above; only now allocate.
				d.strBuf = make([]byte, 0, d.strRemain)
				d.st = stStrBody
			}
		default:
			return d.errf("unexpected byte %q in string length header", b)
		}
	case stStrBody:
		d.strBuf = append(d.strBuf, b)
		d.strRemain--
		if d.strRemain == 0 {
			d.stringDone(string(d.strBuf))
		}
	case stInt:
		switch {
		case b == ';':
			if len(d.intBuf) == 0 || string(d.intBuf) == "-" {
				return d.errf("integer has no digits")
			}
			v, err := strconv.ParseInt(string(d.intBuf), 10, 64)
			if err != nil {
				return d.errf("invalid integer: %v", err)
			}
			d.intBuf = nil
			d.emit(Event{Kind: EventInt, Int: v})
			d.valueDone()
		case b >= '0' && b <= '9', b == '-' && len(d.intBuf) == 0:
			if len(d.intBuf) >= maxIntDigits {
				return d.errf("integer literal too long")
			}
			d.intBuf = append(d.intBuf, b)
		default:
			return d.errf("unexpected byte %q in integer", b)
		}
	}
	return nil
}
