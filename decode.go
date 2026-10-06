package qstr

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrClosed is returned by Write on a closed StreamDecoder or StreamEncoder.
var ErrClosed = errors.New("qstr: stream closed")

// errIncomplete is an internal sentinel: the buffer does not yet contain a
// full record and more input may arrive.
var errIncomplete = errors.New("qstr: incomplete input")

// Error describes a decode failure. Start and End are byte offsets into
// the original input; End is exclusive.
type Error struct {
	Start int
	End   int
	Msg   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("qstr: %s at byte %d", e.Msg, e.Start)
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func hexVal(c byte) uint16 {
	switch {
	case '0' <= c && c <= '9':
		return uint16(c - '0')
	case 'a' <= c && c <= 'f':
		return uint16(c-'a') + 10
	default:
		return uint16(c-'A') + 10
	}
}

// scanHex4 parses the four hex digits of a \u escape starting at buf[i]
// (buf[i] == '\\', buf[i+1] == 'u').
func scanHex4(buf []byte, i int, final bool) (uint16, error) {
	for k := 0; k < 4; k++ {
		p := i + 2 + k
		if p >= len(buf) {
			if final {
				return 0, &Error{i, len(buf), `truncated \u escape`}
			}
			return 0, errIncomplete
		}
		if !isHex(buf[p]) {
			return 0, &Error{p, p + 1, "invalid hex digit"}
		}
	}
	v := uint16(0)
	for k := 0; k < 4; k++ {
		v = v<<4 | hexVal(buf[i+2+k])
	}
	return v, nil
}

// scanEscape parses the escape sequence starting at buf[i] (buf[i] == '\\')
// and returns the decoded rune and the offset just past the escape.
func scanEscape(buf []byte, i int, final bool) (rune, int, error) {
	if i+1 >= len(buf) {
		if final {
			return 0, 0, &Error{i, len(buf), "truncated escape"}
		}
		return 0, 0, errIncomplete
	}
	switch buf[i+1] {
	case '\\':
		return '\\', i + 2, nil
	case '"':
		return '"', i + 2, nil
	case 'u':
	default:
		return 0, 0, &Error{i, i + 2, "unknown escape sequence"}
	}
	v, err := scanHex4(buf, i, final)
	if err != nil {
		return 0, 0, err
	}
	if !utf16.IsSurrogate(rune(v)) {
		return rune(v), i + 6, nil
	}
	if v >= 0xDC00 {
		return 0, 0, &Error{i, i + 6, "lone low surrogate"}
	}
	// High surrogate: a low surrogate escape must follow immediately.
	j := i + 6
	if j >= len(buf) {
		if final {
			return 0, 0, &Error{i, len(buf), "lone high surrogate"}
		}
		return 0, 0, errIncomplete
	}
	if buf[j] != '\\' {
		return 0, 0, &Error{i, j, "lone high surrogate"}
	}
	if j+1 >= len(buf) {
		if final {
			return 0, 0, &Error{i, len(buf), "lone high surrogate"}
		}
		return 0, 0, errIncomplete
	}
	if buf[j+1] != 'u' {
		return 0, 0, &Error{i, j + 2, "lone high surrogate"}
	}
	v2, err := scanHex4(buf, j, final)
	if err != nil {
		return 0, 0, err
	}
	if v2 < 0xDC00 || v2 > 0xDFFF {
		return 0, 0, &Error{j, j + 6, "invalid low surrogate"}
	}
	return utf16.DecodeRune(rune(v), rune(v2)), j + 6, nil
}

func appendRune(dst []byte, r rune) []byte {
	var tmp [utf8.UTFMax]byte
	n := utf8.EncodeRune(tmp[:], r)
	return append(dst, tmp[:n]...)
}

// scan parses one quoted record at the head of buf. final reports that no
// more input will arrive. It returns the decoded text and the number of
// input bytes consumed, including both quotes. When the record is
// incomplete and final is false it returns errIncomplete.
func scan(buf []byte, final bool) (string, int, error) {
	if len(buf) == 0 {
		if final {
			return "", 0, &Error{0, 0, "expected opening quote"}
		}
		return "", 0, errIncomplete
	}
	if buf[0] != '"' {
		return "", 0, &Error{0, 1, "expected opening quote"}
	}
	var out []byte
	i := 1
	for {
		if i >= len(buf) {
			if final {
				return "", 0, &Error{len(buf), len(buf), "unterminated string"}
			}
			return "", 0, errIncomplete
		}
		c := buf[i]
		switch {
		case c == '"':
			return string(out), i + 1, nil
		case c == '\\':
			r, next, err := scanEscape(buf, i, final)
			if err != nil {
				return "", 0, err
			}
			out = appendRune(out, r)
			i = next
		case c < 0x20 || c == 0x7f:
			return "", 0, &Error{i, i + 1, "unescaped control character"}
		case c < 0x80:
			out = append(out, c)
			i++
		default:
			if !utf8.FullRune(buf[i:]) {
				if final {
					return "", 0, &Error{i, len(buf), "invalid UTF-8"}
				}
				return "", 0, errIncomplete
			}
			r, size := utf8.DecodeRune(buf[i:])
			if r == utf8.RuneError && size == 1 {
				return "", 0, &Error{i, i + 1, "invalid UTF-8"}
			}
			out = append(out, buf[i:i+size]...)
			i += size
		}
	}
}

// Decode parses one quoted record from the head of s and returns the
// decoded text and the number of bytes consumed, including both quotes.
// Bytes after the closing quote are left for the caller; check the consumed
// count to detect trailing data. On failure the error is an *Error whose
// offsets are byte offsets into s.
func Decode(s string) (string, int, error) {
	out, n, err := scan([]byte(s), true)
	if err != nil {
		return "", 0, err
	}
	return out, n, nil
}

// escapeState classifies a would-be escape sequence during resync.
type escapeState int

const (
	escIllegal escapeState = iota
	escLegal
	escIncomplete // buffer ended mid-escape; more input may complete it
)

// escapeLen examines the escape starting at buf[i] (buf[i] == '\\') and
// returns its length in bytes when legal.
func escapeLen(buf []byte, i int) (int, escapeState) {
	if i+1 >= len(buf) {
		return 0, escIncomplete
	}
	switch buf[i+1] {
	case '\\', '"':
		return 2, escLegal
	case 'u':
		for k := 0; k < 4; k++ {
			p := i + 2 + k
			if p >= len(buf) {
				return 0, escIncomplete
			}
			if !isHex(buf[p]) {
				return 0, escIllegal
			}
		}
		return 6, escLegal
	default:
		return 0, escIllegal
	}
}

// StreamError is a decode failure reported by StreamDecoder. Start and End
// are absolute byte offsets into the whole stream; End is exclusive.
type StreamError struct {
	Start int
	End   int
	Msg   string
}

func (e StreamError) Error() string {
	return fmt.Sprintf("qstr: %s at byte %d", e.Msg, e.Start)
}

// StreamDecoder incrementally decodes a stream of quoted records separated
// by a caller-specified separator byte. Escape sequences and surrogate
// pairs may span Write boundaries: incomplete input is buffered until it
// can be resolved, and Checkpoint reports the absolute offset up to which
// the stream has been fully consumed.
//
// On a decode error the decoder records a StreamError and resynchronizes at
// the next separator that does not occur inside a legal escape sequence,
// then continues with the following record.
//
// A StreamDecoder is not safe for concurrent use.
type StreamDecoder struct {
	sep     byte
	buf     []byte
	base    int // absolute stream offset of buf[0]
	wantSep bool
	resync  bool
	closed  bool
	records []string
	errs    []StreamError
}

// NewStreamDecoder returns a decoder for streams whose records are
// separated by sep. The separator must not be '\\' or '"'.
func NewStreamDecoder(sep byte) *StreamDecoder {
	return &StreamDecoder{sep: sep}
}

// Write feeds more input and processes every complete record now available.
func (d *StreamDecoder) Write(p []byte) error {
	if d.closed {
		return ErrClosed
	}
	d.buf = append(d.buf, p...)
	d.process(false)
	return nil
}

// Close marks the stream finished; any truncated trailing record is
// reported as an error and unterminated resync garbage is discarded.
func (d *StreamDecoder) Close() error {
	if d.closed {
		return ErrClosed
	}
	d.closed = true
	d.process(true)
	return nil
}

// Records returns the records decoded so far, in order.
func (d *StreamDecoder) Records() []string { return d.records }

// Errors returns the decode errors encountered so far, in order, with
// absolute stream offsets.
func (d *StreamDecoder) Errors() []StreamError { return d.errs }

// Checkpoint returns the absolute stream offset up to which input has been
// fully consumed and may be discarded by the caller.
func (d *StreamDecoder) Checkpoint() int { return d.base }

func (d *StreamDecoder) drop(n int) {
	d.buf = d.buf[n:]
	d.base += n
}

func (d *StreamDecoder) process(final bool) {
	for {
		if d.resync {
			if !d.scanResync(final) {
				return
			}
			continue
		}
		if d.wantSep {
			if len(d.buf) == 0 {
				return
			}
			if d.buf[0] == d.sep {
				d.drop(1)
				d.wantSep = false
				continue
			}
			d.errs = append(d.errs, StreamError{d.base, d.base + 1, "expected record separator"})
			d.resync = true
			continue
		}
		if len(d.buf) == 0 {
			return
		}
		out, n, err := scan(d.buf, final)
		if err == errIncomplete {
			return
		}
		if err != nil {
			e := err.(*Error)
			d.errs = append(d.errs, StreamError{d.base + e.Start, d.base + e.End, e.Msg})
			d.resync = true
			continue
		}
		d.records = append(d.records, out)
		d.drop(n)
		d.wantSep = true
	}
}

// scanResync discards garbage up to and including the next separator that
// is not part of a legal escape sequence. It reports whether a boundary
// was found.
func (d *StreamDecoder) scanResync(final bool) bool {
	i := 0
	for i < len(d.buf) {
		c := d.buf[i]
		if c == '\\' {
			n, state := escapeLen(d.buf, i)
			switch state {
			case escLegal:
				i += n
				continue
			case escIncomplete:
				if !final {
					// Keep the tail: more input may complete the
					// escape and it might contain the separator.
					d.drop(i)
					return false
				}
				i++
				continue
			default:
				i++
				continue
			}
		}
		if c == d.sep {
			d.drop(i + 1)
			d.resync = false
			return true
		}
		i++
	}
	d.drop(i)
	return false
}
