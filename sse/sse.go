// Package sse provides an injection-safe Server-Sent Events writer.
//
// The WHATWG HTML "Server-sent events" framing uses raw line breaks as its
// only record separator, so a newline carried inside caller-controlled text
// can smuggle extra "event:", "id:" or "retry:" lines into the stream. Every
// method of this package therefore treats its inputs as untrusted data:
//
//   - Event names are checked byte-for-byte against an allowlist that was
//     registered when the writer was constructed.
//   - Data is split on every CR, LF and CRLF sequence and emitted as one
//     "data:" field per resulting line, so no line can ever terminate the
//     field it belongs to.
//   - IDs are rejected when they contain a NUL byte, CR or LF.
//   - The reconnect interval is chosen by the server via Config.Retry
//     (DefaultRetry when unset); it can never be influenced by callers of
//     WriteEvent.
//
// The emitted framing is a strict subset of the WHATWG wire format. Feeding
// it back through a conforming WHATWG parser (the reference parser in
// sse_test.go is a direct transcription of the specification) reproduces the
// original message, with the one normalization the specification itself
// mandates: CR and CRLF inside data are delivered as LF. All other bytes,
// including NUL, U+FEFF and leading spaces or colons, round-trip unchanged.
package sse

import (
	"errors"
	"io"
	"strconv"
	"time"
)

// DefaultRetry is the reconnect interval used when Config.Retry is zero.
const DefaultRetry = 30_000 * time.Millisecond

// DefaultEventNameCharset is the event-name allowlist used when
// Config.AllowedEventNames is nil. It covers lowercase and uppercase ASCII
// letters, digits and the punctuation commonly used in dotted event names;
// whitespace and control bytes (which include CR and LF) are absent.
var DefaultEventNameCharset = []byte(
	"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-:",
)

var (
	// ErrEventNameNotAllowed indicates that an event name contains a byte
	// that is not in the registered allowlist.
	ErrEventNameNotAllowed = errors.New("sse: event name contains a character outside the registered charset")

	// ErrInvalidID indicates that an event ID contains a NUL byte or a
	// line break, either of which would break or hijack the "id:" field.
	ErrInvalidID = errors.New("sse: event id must not contain NUL, CR or LF")

	// ErrInvalidCharset indicates that a registered event-name charset is
	// itself unsafe, for example because it contains a line break.
	ErrInvalidCharset = errors.New("sse: registered event-name charset must not contain NUL, CR or LF")
)

// Config configures a Writer.
type Config struct {
	// Retry is the server-controlled reconnect interval advertised to
	// clients in a "retry:" field. Zero selects DefaultRetry.
	Retry time.Duration

	// AllowedEventNames is the byte allowlist for event names. Every byte
	// of an event name must appear in this slice; nil selects
	// DefaultEventNameCharset. The charset itself must not contain NUL,
	// CR or LF.
	AllowedEventNames []byte
}

// Writer serializes SSE events to an io.Writer. It is safe for no more than
// one goroutine at a time, matching the usual contract of http.ResponseWriter.
type Writer struct {
	w        io.Writer
	retry    time.Duration
	nameBits [256]bool
	preamble bool
}

// NewWriter validates cfg and returns a Writer bound to w. The "retry:"
// field is emitted lazily, immediately before the first event.
func NewWriter(w io.Writer, cfg Config) (*Writer, error) {
	if w == nil {
		return nil, errors.New("sse: writer must not be nil")
	}
	if cfg.Retry < 0 {
		return nil, errors.New("sse: retry interval must not be negative")
	}

	retry := cfg.Retry
	if retry == 0 {
		retry = DefaultRetry
	}

	charset := cfg.AllowedEventNames
	if charset == nil {
		charset = DefaultEventNameCharset
	}

	sw := &Writer{w: w, retry: retry}
	for _, b := range charset {
		if b == 0 || b == '\n' || b == '\r' {
			return nil, ErrInvalidCharset
		}
		sw.nameBits[b] = true
	}
	return sw, nil
}

// RetryInterval reports the server-controlled reconnect interval the writer
// advertises to clients.
func (s *Writer) RetryInterval() time.Duration { return s.retry }

// WriteEvent serializes one event. The empty event name is allowed and
// produces no "event:" line; any other name must use only registered bytes.
// id is emitted only when non-empty and must not contain NUL, CR or LF. data
// is split on every CR, LF and CRLF sequence into separate "data:" fields;
// empty data still emits a single empty "data:" field so the event keeps a
// payload and is dispatchable by a conforming parser.
//
// All validation happens before any byte reaches the underlying writer, so
// a rejected event never leaves a partially written frame behind.
func (s *Writer) WriteEvent(name, id, data string) error {
	if err := s.validateName(name); err != nil {
		return err
	}
	if !validID(id) {
		return ErrInvalidID
	}

	var frame []byte
	if !s.preamble {
		frame = appendRetry(frame, s.retry)
	}
	if name != "" {
		frame = appendField(frame, "event", name)
	}
	if id != "" {
		frame = appendField(frame, "id", id)
	}
	for _, line := range splitLines(data) {
		frame = appendField(frame, "data", line)
	}
	frame = append(frame, '\n')

	if _, err := s.w.Write(frame); err != nil {
		return err
	}
	s.preamble = true
	return nil
}

// Flush flushes the underlying writer when it implements http.Flusher or an
// equivalent Flush method. It is a no-op otherwise.
func (s *Writer) Flush() {
	if f, ok := s.w.(interface{ Flush() }); ok {
		f.Flush()
	}
}

func (s *Writer) validateName(name string) error {
	if name == "" {
		return nil
	}
	for i := 0; i < len(name); i++ {
		if !s.nameBits[name[i]] {
			return ErrEventNameNotAllowed
		}
	}
	return nil
}

func validID(id string) bool {
	for i := 0; i < len(id); i++ {
		switch id[i] {
		case 0, '\n', '\r':
			return false
		}
	}
	return true
}

// appendField appends one "field: value\n" line. value is emitted verbatim
// and must already be guaranteed free of CR and LF; the single space after
// the colon is the optional WHATWG padding that every conforming parser
// strips, which keeps a value beginning with a space or colon intact.
func appendField(dst []byte, field, value string) []byte {
	dst = append(dst, field...)
	dst = append(dst, ':', ' ')
	dst = append(dst, value...)
	dst = append(dst, '\n')
	return dst
}

func appendRetry(dst []byte, retry time.Duration) []byte {
	dst = append(dst, "retry: "...)
	dst = strconv.AppendInt(dst, int64(retry/time.Millisecond), 10)
	return append(dst, '\n')
}

// splitLines splits on LF, CR and CRLF. A trailing line break yields a final
// empty line, matching the WHATWG "process the field" behavior for data:
// splitLines("a\n") == ["a", ""].
func splitLines(s string) []string {
	lines := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			lines = append(lines, s[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, s[start:i])
			i++
			if i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
			i--
		}
	}
	lines = append(lines, s[start:])
	return lines
}
