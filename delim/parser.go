// Package delim implements a pausable delimited-text parser intended for
// importers. Input may arrive in arbitrarily sized chunks; quoted newlines
// and escape sequences may straddle chunk boundaries. Fields are reported
// as raw byte ranges into the input stream together with their decoded
// values, so callers that retain the stream can slice it zero-copy.
//
// Error recovery rules:
//   - An illegal quote (a bare quote inside an unquoted field, or trailing
//     garbage after a closing quote) isolates the physical line: the
//     remainder of that line is discarded and the next record starts on
//     the following line, so a bad line is never merged into its successor.
//   - When a field exceeds Config.MaxFieldSize its value is dropped (to
//     cap memory) but the parser keeps scanning, quote-aware, until it has
//     located the end of the record, then resumes with the next record.
package delim

// ErrorKind classifies a ParseError.
type ErrorKind int

const (
	// ErrIllegalQuote is a quote in an unquoted field, or non-delimiter
	// data after a closing quote. The error range covers the offending
	// quote byte.
	ErrIllegalQuote ErrorKind = iota + 1
	// ErrFieldTooLarge marks a field whose decoded size exceeded
	// Config.MaxFieldSize. The error range spans from the field start to
	// the byte that crossed the limit.
	ErrFieldTooLarge
	// ErrUnterminatedQuote marks a quoted field left open at EOF. The
	// error range covers the opening quote byte.
	ErrUnterminatedQuote
)

func (k ErrorKind) String() string {
	switch k {
	case ErrIllegalQuote:
		return "illegal quote"
	case ErrFieldTooLarge:
		return "field too large"
	case ErrUnterminatedQuote:
		return "unterminated quote"
	}
	return "unknown error"
}

// ParseError describes a recoverable parse error. Start and End are
// absolute byte offsets into the input stream; Line is the 1-based
// physical line on which the error was detected.
type ParseError struct {
	Kind  ErrorKind
	Start int64
	End   int64
	Line  int
}

// Field is one parsed field. Start and End are absolute byte offsets
// delimiting the raw field text as it appeared in the stream, including
// surrounding quotes and escape sequences. Value is the decoded field
// content; it is nil when the field exceeded the configured size limit.
type Field struct {
	Start int64
	End   int64
	Value []byte
}

// Record is one logical record. Start and End are absolute byte offsets
// covering the whole record including its line terminator. Err is the
// first error encountered while parsing the record, or nil.
type Record struct {
	Start  int64
	End    int64
	Fields []Field
	Err    *ParseError
}

// Config tunes a Parser. The zero value parses comma-delimited,
// double-quoted text with no field size limit.
type Config struct {
	Delim byte // field delimiter; defaults to ','
	Quote byte // quote character; defaults to '"'
	// MaxFieldSize caps the decoded size of a single field in bytes.
	// Zero means unlimited. When a field exceeds the cap its Value is
	// reported as nil and parsing continues to the end of the record.
	MaxFieldSize int
}

type state int

const (
	stFieldStart state = iota
	stFieldStartCR
	stUnquoted
	stUnquotedCR
	stQuoted
	stQuoteClose
	stQuoteCloseCR
	stSkipLine
)

// Parser is a resumable delimited-text parser. Feed it chunks of input
// in order; completed records are returned as soon as they are known.
// A Parser must not be copied after first use.
type Parser struct {
	delim byte
	quote byte
	max   int

	off  int64 // absolute offset of the next byte to process
	line int   // 1-based physical line of the next byte

	state    state
	recStart int64
	fields   []Field
	recErr   *ParseError

	fieldStart int64
	quotePos   int64 // offset of the quote that opened stQuoteClose
	value      []byte
	oversize   bool
}

// New returns a Parser configured by cfg.
func New(cfg Config) *Parser {
	p := &Parser{
		delim: cfg.Delim,
		quote: cfg.Quote,
		max:   cfg.MaxFieldSize,
		line:  1,
		state: stFieldStart,
	}
	if p.delim == 0 {
		p.delim = ','
	}
	if p.quote == 0 {
		p.quote = '"'
	}
	return p
}

// Parse is a convenience one-shot wrapper around New, Feed and Close.
func Parse(data []byte, cfg Config) []Record {
	p := New(cfg)
	out := p.Feed(data)
	return append(out, p.Close()...)
}

// Feed consumes one chunk of input and returns every record completed by
// it. Chunks may split the input anywhere, including inside quoted
// sections and escape sequences.
func (p *Parser) Feed(data []byte) []Record {
	var out []Record
	for _, c := range data {
		if rec, ok := p.step(c); ok {
			out = append(out, rec)
		}
	}
	return out
}

// Close signals end of input and returns any final record, including a
// last record without a trailing newline.
func (p *Parser) Close() []Record {
	var out []Record
	switch p.state {
	case stFieldStart:
		if len(p.fields) > 0 || p.recErr != nil {
			p.endField(p.off)
			out = append(out, p.endRecord(p.off))
		}
	case stFieldStartCR:
		if len(p.fields) > 0 || p.recErr != nil {
			p.endField(p.off - 1) // exclude the pending CR
			out = append(out, p.endRecord(p.off))
		}
	case stUnquoted:
		p.endField(p.off)
		out = append(out, p.endRecord(p.off))
	case stUnquotedCR:
		p.endField(p.off - 1)
		out = append(out, p.endRecord(p.off))
	case stQuoted:
		p.setErr(ErrUnterminatedQuote, p.fieldStart, p.fieldStart+1)
		p.endField(p.off)
		out = append(out, p.endRecord(p.off))
	case stQuoteClose:
		p.endField(p.off)
		out = append(out, p.endRecord(p.off))
	case stQuoteCloseCR:
		p.endField(p.off - 1)
		out = append(out, p.endRecord(p.off))
	case stSkipLine:
		out = append(out, p.endRecord(p.off))
	}
	return out
}

// step consumes a single byte and returns a record when one completes.
func (p *Parser) step(c byte) (rec Record, emit bool) {
	o := p.off
	p.off++
	defer func() {
		if c == '\n' {
			p.line++
		}
	}()

	switch p.state {
	case stFieldStart:
		switch {
		case c == p.quote:
			p.state = stQuoted
		case c == p.delim:
			p.endField(o)
			p.fieldStart = o + 1
		case c == '\n':
			if len(p.fields) > 0 || p.recErr != nil {
				// The line ends right after a delimiter: emit the
				// record with a trailing empty field.
				p.endField(o)
				return p.endRecord(o + 1), true
			}
			// Blank physical line: skip without emitting a record.
			p.recStart = o + 1
			p.fieldStart = o + 1
		case c == '\r':
			p.state = stFieldStartCR
		default:
			p.appendByte(c, o)
			p.state = stUnquoted
		}

	case stFieldStartCR:
		if c == '\n' {
			if len(p.fields) > 0 || p.recErr != nil {
				p.endField(o - 1)
				return p.endRecord(o + 1), true
			}
			p.recStart = o + 1
			p.fieldStart = o + 1
			p.state = stFieldStart
			return rec, false
		}
		// The CR was field content after all.
		p.appendByte('\r', o-1)
		p.state = stUnquoted
		return p.stepUnquoted(c, o)

	case stUnquoted:
		return p.stepUnquoted(c, o)

	case stUnquotedCR:
		switch {
		case c == '\n':
			p.endField(o - 1)
			return p.endRecord(o + 1), true
		case c == p.delim:
			p.endField(o - 1)
			p.fieldStart = o + 1
			p.state = stFieldStart
		case c == p.quote:
			p.appendByte('\r', o-1)
			p.setErr(ErrIllegalQuote, o, o+1)
			p.endField(o)
			p.state = stSkipLine
		case c == '\r':
			p.appendByte('\r', o-1)
		default:
			p.appendByte('\r', o-1)
			p.appendByte(c, o)
			p.state = stUnquoted
		}

	case stQuoted:
		if c == p.quote {
			p.quotePos = o
			p.state = stQuoteClose
		} else {
			p.appendByte(c, o)
		}

	case stQuoteClose:
		switch {
		case c == p.quote:
			// Doubled quote: one literal quote in the value.
			p.appendByte(p.quote, o)
			p.state = stQuoted
		case c == p.delim:
			p.endField(o)
			p.fieldStart = o + 1
			p.state = stFieldStart
		case c == '\n':
			p.endField(o)
			return p.endRecord(o + 1), true
		case c == '\r':
			p.state = stQuoteCloseCR
		default:
			p.setErr(ErrIllegalQuote, p.quotePos, p.quotePos+1)
			p.endField(o)
			p.state = stSkipLine
		}

	case stQuoteCloseCR:
		switch {
		case c == '\n':
			p.endField(o - 1)
			return p.endRecord(o + 1), true
		case c == p.delim:
			p.endField(o - 1)
			p.fieldStart = o + 1
			p.state = stFieldStart
		case c == '\r':
			// stay
		default:
			p.setErr(ErrIllegalQuote, p.quotePos, p.quotePos+1)
			p.endField(o - 1)
			p.state = stSkipLine
		}

	case stSkipLine:
		if c == '\n' {
			return p.endRecord(o + 1), true
		}
	}
	return rec, false
}

// stepUnquoted handles a byte while inside an unquoted field.
func (p *Parser) stepUnquoted(c byte, o int64) (rec Record, emit bool) {
	switch {
	case c == p.delim:
		p.endField(o)
		p.fieldStart = o + 1
		p.state = stFieldStart
	case c == '\n':
		p.endField(o)
		return p.endRecord(o + 1), true
	case c == p.quote:
		p.setErr(ErrIllegalQuote, o, o+1)
		p.endField(o)
		p.state = stSkipLine
	case c == '\r':
		p.state = stUnquotedCR
	default:
		p.appendByte(c, o)
	}
	return rec, false
}

// appendByte adds c to the current field value, enforcing the size cap.
// Once the cap is exceeded the byte (and all further bytes of the field)
// are dropped, but the caller keeps stepping the state machine so the
// record end is still located correctly.
func (p *Parser) appendByte(c byte, o int64) {
	if p.oversize {
		return
	}
	if p.max > 0 && len(p.value)+1 > p.max {
		p.oversize = true
		p.setErr(ErrFieldTooLarge, p.fieldStart, o+1)
		return
	}
	p.value = append(p.value, c)
}

// setErr records the first error of the current record.
func (p *Parser) setErr(kind ErrorKind, start, end int64) {
	if p.recErr == nil {
		p.recErr = &ParseError{Kind: kind, Start: start, End: end, Line: p.line}
	}
}

// endField finalizes the current field with raw range [fieldStart, rawEnd).
func (p *Parser) endField(rawEnd int64) {
	f := Field{Start: p.fieldStart, End: rawEnd}
	if !p.oversize {
		f.Value = append([]byte{}, p.value...)
	}
	p.fields = append(p.fields, f)
	p.value = p.value[:0]
	p.oversize = false
}

// endRecord finalizes the current record ending at absolute offset end
// and resets the per-record state for the next one.
func (p *Parser) endRecord(end int64) Record {
	rec := Record{Start: p.recStart, End: end, Fields: p.fields, Err: p.recErr}
	p.fields = nil
	p.recErr = nil
	p.recStart = end
	p.fieldStart = end
	p.state = stFieldStart
	return rec
}
