// Package delimparse implements a pausable parser for delimited text
// (CSV-like) formats, intended to be driven by importers.
//
// Input is fed to the parser in arbitrary chunks; quoted fields may
// contain newlines and escape sequences that span chunk boundaries.
// Fields report their raw byte ranges as absolute offsets into the
// input stream, so callers can re-slice the original source. A quote
// that appears illegally causes the offending physical line to be
// isolated and reported as an error, without being merged into the
// following record. A per-field memory cap can be configured; when a
// field exceeds the cap its decoded value is dropped, but the parser
// keeps scanning to locate the end of the record.
//
// A Parser is not safe for concurrent use.
package delimparse

// Options configures a Parser.
type Options struct {
	// Comma is the field delimiter. Defaults to ','.
	Comma byte
	// Quote is the quoting character. Defaults to '"'.
	Quote byte
	// Escape, when non-zero, is an escape character usable inside
	// quoted fields: the byte following Escape is always literal.
	// When zero, a doubled Quote inside a quoted field represents a
	// literal quote instead.
	Escape byte
	// MaxFieldBytes caps the decoded size of a single field. When a
	// field exceeds the cap its Value is dropped and Truncated is
	// set, but the parser keeps scanning to locate the end of the
	// record. Zero means no limit.
	MaxFieldBytes int
}

// Field is a single parsed field.
type Field struct {
	Start int64 // absolute offset of the first raw byte (including the opening quote)
	End   int64 // absolute offset one past the last raw byte (including the closing quote)
	// Value is the decoded field value (quotes stripped, escapes
	// resolved). It is nil when Truncated is true.
	Value     []byte
	Quoted    bool // whether the field was quoted in the input
	Truncated bool // whether the decoded value exceeded MaxFieldBytes
}

// Record is a single parsed record (one logical line).
type Record struct {
	Fields []Field
	Start  int64 // absolute offset of the first byte of the record
	End    int64 // absolute offset one past the record terminator (or EOF)
}

// Error describes a malformed physical line. The range [Start, End)
// covers exactly one physical line, including its terminating newline
// (or up to EOF). For an unterminated quoted field at EOF the range
// covers the incomplete record instead.
type Error struct {
	Message string
	Start   int64
	End     int64
}

// Event is emitted by Parser.Feed and Parser.Close. Exactly one of
// Record or Err is non-nil.
type Event struct {
	Record *Record
	Err    *Error
}

// Error messages reported in Error.Message.
const (
	ErrBareQuote    = "bare quote in unquoted field"
	ErrAfterQuote   = "unexpected character after closing quote"
	ErrUnterminated = "unterminated quoted field"
)

type state int8

const (
	stateFieldStart state = iota
	stateUnquoted
	stateQuoted
	stateEscape
	stateQuoteInQuoted
	stateAfterQuote
	stateAfterQuoteCR
	stateSkipLine
	stateClosed
)

// Parser is an incremental, pausable parser for delimited text.
type Parser struct {
	comma    byte
	quote    byte
	escape   byte
	maxField int

	off        int64 // absolute offset of the next byte to process
	lineStart  int64 // absolute offset of the current physical line
	recStart   int64 // absolute offset of the current record
	fieldStart int64 // absolute offset of the current field

	state     state
	fields    []Field
	buf       []byte // decoded value of the current field
	quoted    bool
	truncated bool
	prevCR    bool // previous byte was '\r' (tentative part of a CRLF)

	errMsg   string
	errStart int64
}

// NewParser returns a Parser configured by opt.
func NewParser(opt Options) *Parser {
	p := &Parser{
		comma:    opt.Comma,
		quote:    opt.Quote,
		escape:   opt.Escape,
		maxField: opt.MaxFieldBytes,
		state:    stateFieldStart,
	}
	if p.comma == 0 {
		p.comma = ','
	}
	if p.quote == 0 {
		p.quote = '"'
	}
	return p
}

// Feed consumes one chunk of input and returns the events completed
// by it. Chunks may be split at any byte boundary. Feed must not be
// called after Close.
func (p *Parser) Feed(chunk []byte) []Event {
	var events []Event
	if p.state == stateClosed {
		return events
	}
	for _, c := range chunk {
		p.step(c, &events)
		p.off++
	}
	return events
}

// Close signals end of input and returns any final events: a pending
// record is flushed, an incomplete line in error state is reported,
// and an unterminated quoted field is reported as an error.
func (p *Parser) Close() []Event {
	var events []Event
	switch p.state {
	case stateFieldStart:
		if len(p.fields) > 0 {
			p.endField(p.off)
			p.endRecord(&events, p.off)
		}
	case stateUnquoted:
		p.endField(p.off)
		p.endRecord(&events, p.off)
	case stateQuoted, stateEscape:
		events = append(events, Event{Err: &Error{
			Message: ErrUnterminated,
			Start:   p.recStart,
			End:     p.off,
		}})
	case stateQuoteInQuoted, stateAfterQuote:
		p.endField(p.off)
		p.endRecord(&events, p.off)
	case stateAfterQuoteCR:
		p.endRecord(&events, p.off)
	case stateSkipLine:
		events = append(events, Event{Err: &Error{
			Message: p.errMsg,
			Start:   p.errStart,
			End:     p.off,
		}})
	}
	p.state = stateClosed
	return events
}

// Parse is a convenience helper that parses data in one shot.
func Parse(data []byte, opt Options) (records []Record, errs []Error) {
	p := NewParser(opt)
	events := append(p.Feed(data), p.Close()...)
	for _, e := range events {
		if e.Record != nil {
			records = append(records, *e.Record)
		} else {
			errs = append(errs, *e.Err)
		}
	}
	return records, errs
}

func (p *Parser) step(c byte, events *[]Event) {
	off := p.off
	switch p.state {
	case stateFieldStart:
		switch {
		case c == p.comma:
			p.endField(off)
			p.fieldStart = off + 1
		case c == p.quote:
			p.quoted = true
			p.state = stateQuoted
		case c == '\n':
			if len(p.fields) > 0 {
				p.endField(off)
				p.endRecord(events, off+1)
			}
			// Otherwise the line is blank and skipped.
			p.nextLine(off)
		case c == '\r':
			p.put(c)
			p.prevCR = true
			p.state = stateUnquoted
		default:
			p.put(c)
			p.state = stateUnquoted
		}

	case stateUnquoted:
		switch {
		case c == p.comma:
			p.endField(off)
			p.fieldStart = off + 1
			p.state = stateFieldStart
		case c == p.quote:
			p.startError(ErrBareQuote)
		case c == '\n':
			rawEnd := off
			if p.prevCR {
				// The '\r' belongs to the CRLF line ending.
				rawEnd = off - 1
				if !p.truncated {
					p.buf = p.buf[:len(p.buf)-1]
				}
			}
			if len(p.fields) > 0 || len(p.buf) > 0 || p.truncated {
				p.endField(rawEnd)
				p.endRecord(events, off+1)
			}
			// Otherwise the line is blank and skipped.
			p.nextLine(off)
		case c == '\r':
			p.put(c)
			p.prevCR = true
		default:
			p.put(c)
			p.prevCR = false
		}

	case stateQuoted:
		switch {
		case p.escape != 0 && c == p.escape:
			p.state = stateEscape
		case c == p.quote:
			if p.escape != 0 {
				p.state = stateAfterQuote
			} else {
				p.state = stateQuoteInQuoted
			}
		case c == '\n':
			p.put(c)
			p.lineStart = off + 1 // physical line advances inside quotes
		default:
			p.put(c)
		}

	case stateEscape:
		p.put(c)
		if c == '\n' {
			p.lineStart = off + 1
		}
		p.state = stateQuoted

	case stateQuoteInQuoted:
		// A quote was seen inside a quoted field with no escape
		// character configured: it is either a doubled quote or the
		// closing quote.
		switch {
		case c == p.quote:
			p.put(p.quote)
			p.state = stateQuoted
		case c == p.comma:
			p.endField(off)
			p.fieldStart = off + 1
			p.state = stateFieldStart
		case c == '\n':
			p.endField(off)
			p.endRecord(events, off+1)
			p.nextLine(off)
		case c == '\r':
			p.endField(off)
			p.state = stateAfterQuoteCR
		default:
			p.startError(ErrAfterQuote)
		}

	case stateAfterQuote:
		switch {
		case c == p.comma:
			p.endField(off)
			p.fieldStart = off + 1
			p.state = stateFieldStart
		case c == '\n':
			p.endField(off)
			p.endRecord(events, off+1)
			p.nextLine(off)
		case c == '\r':
			p.endField(off)
			p.state = stateAfterQuoteCR
		default:
			p.startError(ErrAfterQuote)
		}

	case stateAfterQuoteCR:
		if c == '\n' {
			p.endRecord(events, off+1)
			p.nextLine(off)
		} else {
			p.startError(ErrAfterQuote)
		}

	case stateSkipLine:
		// Isolate the bad physical line: consume it up to and
		// including its newline, with no quote processing.
		if c == '\n' {
			*events = append(*events, Event{Err: &Error{
				Message: p.errMsg,
				Start:   p.errStart,
				End:     off + 1,
			}})
			p.nextLine(off)
		}
	}
}

// put appends one decoded byte to the current field, enforcing the
// per-field memory cap. Once the cap is exceeded the decoded value is
// dropped, but the caller keeps driving the state machine so the end
// of the record is still located.
func (p *Parser) put(c byte) {
	if p.truncated {
		return
	}
	if p.maxField > 0 && len(p.buf)+1 > p.maxField {
		p.truncated = true
		p.buf = nil
		return
	}
	p.buf = append(p.buf, c)
}

func (p *Parser) endField(rawEnd int64) {
	value := p.buf
	if p.truncated {
		value = nil
	} else if value == nil {
		value = []byte{}
	}
	p.fields = append(p.fields, Field{
		Start:     p.fieldStart,
		End:       rawEnd,
		Value:     value,
		Quoted:    p.quoted,
		Truncated: p.truncated,
	})
	p.buf = nil
	p.quoted = false
	p.truncated = false
	p.prevCR = false
}

func (p *Parser) endRecord(events *[]Event, end int64) {
	*events = append(*events, Event{Record: &Record{
		Fields: p.fields,
		Start:  p.recStart,
		End:    end,
	}})
	p.fields = nil
}

// startError discards the partially parsed record and switches to
// isolating the current physical line.
func (p *Parser) startError(msg string) {
	p.errMsg = msg
	p.errStart = p.lineStart
	p.state = stateSkipLine
	p.fields = nil
	p.buf = nil
	p.quoted = false
	p.truncated = false
	p.prevCR = false
}

// nextLine resets the per-line bookkeeping after a physical newline
// has been consumed at absolute offset off.
func (p *Parser) nextLine(off int64) {
	p.lineStart = off + 1
	p.recStart = off + 1
	p.fieldStart = off + 1
	p.state = stateFieldStart
	p.prevCR = false
}
