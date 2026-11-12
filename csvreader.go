package taskpkg

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrFieldTooLong marks a record that was discarded because one of its
	// fields exceeded Options.MaxFieldBytes.
	ErrFieldTooLong = errors.New("taskpkg: field exceeds MaxFieldBytes")

	// ErrTruncatedQuote marks a record whose quoted field is still open at EOF.
	ErrTruncatedQuote = errors.New("taskpkg: quoted field is not terminated")

	// ErrMalformedRecord marks a syntactically invalid record. The reader
	// consumes the rest of that physical record before reporting the error.
	ErrMalformedRecord = errors.New("taskpkg: malformed CSV record")

	// ErrInvalidOptions marks an unusable delimiter/quote configuration.
	ErrInvalidOptions = errors.New("taskpkg: invalid reader options")
)

// Options controls the byte-level CSV grammar.
//
// Delimiter and Quote are each exactly one byte. A zero value selects the
// default (',' and '"', respectively). MaxFieldBytes is the maximum raw size
// of a field, including its surrounding quotes. A value <= 0 means unlimited.
type Options struct {
	Delimiter     byte
	Quote         byte
	MaxFieldBytes int64
}

// Config is an alias for Options.
type Config = Options

// DefaultOptions returns comma-separated, double-quoted CSV options.
func DefaultOptions() Options {
	return Options{
		Delimiter: ',',
		Quote:     '"',
	}
}

// Span is a half-open byte interval [Start, End) in the input stream.
type Span struct {
	Start int64
	End   int64
}

// Len returns the number of bytes in the span.
func (s Span) Len() int64 {
	if s.End < s.Start {
		return 0
	}
	return s.End - s.Start
}

// LineSpan is an inclusive, one-based physical line interval.
type LineSpan struct {
	Start int64
	End   int64
}

// Lines returns the number of physical lines in the span.
func (s LineSpan) Lines() int64 {
	if s.End < s.Start {
		return 0
	}
	return s.End - s.Start + 1
}

// Field is one decoded CSV field.
//
// Raw is the original field interval. It includes the surrounding quotes for
// a quoted field, but never includes the field delimiter or line terminator.
// Value excludes the surrounding quotes and has doubled quote bytes collapsed
// to one quote. Embedded line endings are preserved byte-for-byte.
type Field struct {
	Index int
	Raw   Span
	Value string
}

// Record is one physical CSV record.
//
// Span covers the record bytes without its terminating LF or CRLF. LineSpan
// covers the physical lines occupied by the record, including lines crossed by
// a newline inside a quoted field.
type Record struct {
	Index    int64
	Span     Span
	LineSpan LineSpan
	Fields   []Field
}

// RecordError describes a discarded record. No fields from the rejected
// record are exposed.
type RecordError struct {
	Index      int64
	Kind       error
	RecordSpan Span
	LineSpan   LineSpan
	FieldIndex int
	Message    string
}

func (e *RecordError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s (record %d, field %d): %s", e.Kind, e.Index, e.FieldIndex, e.Message)
	}
	return fmt.Sprintf("%s (record %d, field %d)", e.Kind, e.Index, e.FieldIndex)
}

// Unwrap makes errors.Is(err, ErrFieldTooLong) work for a RecordError.
func (e *RecordError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

type fieldState uint8

const (
	stateFieldStart fieldState = iota
	stateUnquoted
	stateQuoted
	stateAfterQuote
)

// Reader incrementally parses CSV records from an io.Reader.
//
// The parser remembers quote state across Read calls. Consequently, a doubled
// quote, a CRLF, or any other token may be split at any byte boundary without
// changing the result.
type Reader struct {
	br   *bufio.Reader
	opts Options

	offset    int64
	line      int64
	nextIndex int64
	configErr error

	active bool

	recStart     int64
	recStartLine int64
	fieldStart   int64
	fieldRawLen  int64
	fieldValue   []byte
	fieldIndex   int
	fields       []Field
	state        fieldState

	discard       bool
	recordKind    error
	recordField   int
	recordMessage string
}

// NewReader creates a streaming CSV reader. With no Options argument the
// defaults are comma, quote, and unlimited field size. At most one Options
// value may be supplied.
func NewReader(r io.Reader, options ...Options) *Reader {
	opts, err := resolveOptions(options)
	if err != nil {
		return &Reader{configErr: err, line: 1}
	}
	if r == nil {
		return &Reader{configErr: fmt.Errorf("%w: nil reader", ErrInvalidOptions), line: 1}
	}
	return &Reader{
		br:   bufio.NewReader(r),
		opts: opts,
		line: 1,
	}
}

// NewScanner is an alias for NewReader.
func NewScanner(r io.Reader, options ...Options) *Reader {
	return NewReader(r, options...)
}

// NewReaderWithOptions is like NewReader but reports option errors directly.
func NewReaderWithOptions(r io.Reader, opts Options) (*Reader, error) {
	resolved, err := resolveOptions([]Options{opts})
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrInvalidOptions)
	}
	return &Reader{
		br:   bufio.NewReader(r),
		opts: resolved,
		line: 1,
	}, nil
}

// NewReaderWithConfig is an alias for NewReaderWithOptions.
func NewReaderWithConfig(r io.Reader, opts Config) (*Reader, error) {
	return NewReaderWithOptions(r, opts)
}

func resolveOptions(given []Options) (Options, error) {
	if len(given) > 1 {
		return Options{}, fmt.Errorf("%w: expected at most one Options value", ErrInvalidOptions)
	}

	opts := DefaultOptions()
	if len(given) == 1 {
		opts = given[0]
		if opts.Delimiter == 0 {
			opts.Delimiter = ','
		}
		if opts.Quote == 0 {
			opts.Quote = '"'
		}
	}

	if opts.Delimiter == opts.Quote {
		return Options{}, fmt.Errorf("%w: delimiter and quote must differ", ErrInvalidOptions)
	}
	if opts.Delimiter == '\r' || opts.Delimiter == '\n' {
		return Options{}, fmt.Errorf("%w: delimiter cannot be a line-ending byte", ErrInvalidOptions)
	}
	if opts.Quote == '\r' || opts.Quote == '\n' {
		return Options{}, fmt.Errorf("%w: quote cannot be a line-ending byte", ErrInvalidOptions)
	}
	return opts, nil
}

// Next returns the next non-empty record. It returns nil, io.EOF after the
// input is exhausted.
//
// A record error consumes the entire rejected record. A later call to Next
// resumes at the following record boundary. A RecordError never contains
// partial fields.
func (r *Reader) Next() (*Record, error) {
	if r.configErr != nil {
		return nil, r.configErr
	}
	r.beginRecord()

	for {
		b, pos, ok, err := r.readByte()
		if err != nil {
			return nil, fmt.Errorf("taskpkg: read at byte %d: %w", r.offset, err)
		}
		if !ok {
			return r.finishAtEOF()
		}
		r.active = true

		if b == '\r' && r.state != stateQuoted {
			next, ok, err := r.peekByte()
			if err != nil {
				return nil, fmt.Errorf("taskpkg: read at byte %d: %w", r.offset, err)
			}
			if ok && next == '\n' {
				_, _, _, err := r.readByte()
				if err != nil {
					return nil, fmt.Errorf("taskpkg: read at byte %d: %w", r.offset, err)
				}
				rec, recErr := r.finishRecord(pos)
				r.line++
				return rec, recErr
			}

			// A bare CR is data. CRLF is the only line ending that starts
			// with CR.
			if r.state == stateAfterQuote {
				r.markMalformed("byte after closing quote is not a delimiter or line ending")
				r.state = stateUnquoted
			}
			if r.state == stateFieldStart {
				r.state = stateUnquoted
			}
			r.addFieldBytes(1, '\r')
			continue
		}

		if b == '\n' {
			if r.state == stateQuoted {
				r.addFieldBytes(1, '\n')
				r.line++
				continue
			}
			rec, recErr := r.finishRecord(pos)
			r.line++
			return rec, recErr
		}

		if b == r.opts.Delimiter {
			if r.state == stateQuoted {
				r.addFieldBytes(1, b)
				continue
			}
			r.completeField(pos)
			r.startField(pos + 1)
			continue
		}

		if b == r.opts.Quote {
			switch r.state {
			case stateFieldStart:
				r.state = stateQuoted
				r.addFieldBytes(1)
			case stateUnquoted:
				r.addFieldBytes(1, b)
			case stateQuoted:
				next, ok, err := r.peekByte()
				if err != nil {
					return nil, fmt.Errorf("taskpkg: read at byte %d: %w", r.offset, err)
				}
				if ok && next == r.opts.Quote {
					_, _, _, err := r.readByte()
					if err != nil {
						return nil, fmt.Errorf("taskpkg: read at byte %d: %w", r.offset, err)
					}
					r.addFieldBytes(2, r.opts.Quote)
					continue
				}
				r.state = stateAfterQuote
				r.addFieldBytes(1)
			case stateAfterQuote:
				r.markMalformed("quote appears after a closing quote")
				r.state = stateUnquoted
				r.addFieldBytes(1, b)
			}
			continue
		}

		switch r.state {
		case stateFieldStart:
			r.state = stateUnquoted
		case stateAfterQuote:
			r.markMalformed("byte after closing quote is not a delimiter or line ending")
			r.state = stateUnquoted
		}
		r.addFieldBytes(1, b)
	}
}

// NextRecord is an alias for Next.
func (r *Reader) NextRecord() (*Record, error) {
	return r.Next()
}

// ReadRecord is an alias for Next.
func (r *Reader) ReadRecord() (*Record, error) {
	return r.Next()
}

// Read is an alias for Next.
func (r *Reader) Read() (*Record, error) {
	return r.Next()
}

// ReadAll reads records until EOF or the first rejected record.
func (r *Reader) ReadAll() ([]*Record, error) {
	var records []*Record
	for {
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return records, err
		}
		records = append(records, rec)
	}
}

// ParseAll reads all records. It stops at the first malformed, truncated, or
// oversized record and returns the records successfully read before it.
func ParseAll(r io.Reader, options ...Options) ([]*Record, error) {
	reader := NewReader(r, options...)
	var records []*Record
	for {
		rec, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return records, err
		}
		records = append(records, rec)
	}
}

// ParseBytes is the in-memory convenience form of ParseAll.
func ParseBytes(data []byte, options ...Options) ([]*Record, error) {
	return ParseAll(newSliceReader(data), options...)
}

func (r *Reader) beginRecord() {
	r.active = false
	r.recStart = r.offset
	r.recStartLine = r.line
	r.fieldStart = r.offset
	r.fieldRawLen = 0
	r.fieldValue = nil
	r.fieldIndex = 0
	r.fields = nil
	r.state = stateFieldStart
	r.discard = false
	r.recordKind = nil
	r.recordField = 0
	r.recordMessage = ""
}

func (r *Reader) readByte() (byte, int64, bool, error) {
	b, err := r.br.ReadByte()
	if errors.Is(err, io.EOF) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	pos := r.offset
	r.offset++
	return b, pos, true, nil
}

func (r *Reader) peekByte() (byte, bool, error) {
	b, err := r.br.Peek(1)
	if errors.Is(err, io.EOF) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(b) == 0 {
		return 0, false, nil
	}
	return b[0], true, nil
}

func (r *Reader) addFieldBytes(rawBytes int64, value ...byte) {
	r.fieldRawLen += rawBytes
	if r.opts.MaxFieldBytes > 0 && r.fieldRawLen > r.opts.MaxFieldBytes {
		r.markRecordError(ErrFieldTooLong, "field raw size exceeds MaxFieldBytes")
		return
	}
	if r.discard {
		return
	}
	r.fieldValue = append(r.fieldValue, value...)
}

func (r *Reader) completeField(rawEnd int64) {
	if !r.discard {
		r.fields = append(r.fields, Field{
			Index: r.fieldIndex,
			Raw: Span{
				Start: r.fieldStart,
				End:   rawEnd,
			},
			Value: string(r.fieldValue),
		})
	}
	r.fieldValue = nil
	r.fieldRawLen = 0
}

func (r *Reader) startField(start int64) {
	r.fieldIndex++
	r.fieldStart = start
	r.fieldRawLen = 0
	r.fieldValue = nil
	r.state = stateFieldStart
}

func (r *Reader) markMalformed(message string) {
	r.markRecordError(ErrMalformedRecord, message)
}

func (r *Reader) markRecordError(kind error, message string) {
	if r.recordKind == nil {
		r.recordKind = kind
		r.recordField = r.fieldIndex
		r.recordMessage = message
	}
	r.discard = true
	r.fields = nil
	r.fieldValue = nil
}

func (r *Reader) finishAtEOF() (*Record, error) {
	if !r.active {
		return nil, io.EOF
	}
	if r.state == stateQuoted && r.recordKind == nil {
		r.markRecordError(ErrTruncatedQuote, "EOF reached inside a quoted field")
	}
	return r.finishRecord(r.offset)
}

func (r *Reader) finishRecord(contentEnd int64) (*Record, error) {
	if !r.active {
		return nil, io.EOF
	}

	if r.state != stateQuoted {
		r.completeField(contentEnd)
	}

	index := r.nextIndex
	r.nextIndex++
	recordSpan := Span{Start: r.recStart, End: contentEnd}
	lineSpan := LineSpan{Start: r.recStartLine, End: r.line}

	if r.recordKind != nil {
		return nil, &RecordError{
			Index:      index,
			Kind:       r.recordKind,
			RecordSpan: recordSpan,
			LineSpan:   lineSpan,
			FieldIndex: r.recordField,
			Message:    r.recordMessage,
		}
	}

	record := &Record{
		Index:    index,
		Span:     recordSpan,
		LineSpan: lineSpan,
		Fields:   r.fields,
	}
	r.fields = nil
	r.fieldValue = nil
	r.active = false
	return record, nil
}

func newSliceReader(data []byte) io.Reader {
	return &sliceReader{data: data}
}

type sliceReader struct {
	data []byte
	off  int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
