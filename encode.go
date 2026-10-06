package qstr

import (
	"context"
	"io"
	"sync"
	"unicode/utf8"
)

const hexdig = "0123456789abcdef"

// escapeByte appends the \u00XX escape for a control byte.
func appendEscapeByte(dst []byte, c byte) []byte {
	return append(dst, '\\', 'u', '0', '0', hexdig[c>>4], hexdig[c&0x0f])
}

var replacementEscape = []byte(`\ufffd`)

// encodeToken returns the encoded form of the first character in p and the
// number of input bytes it consumes. If p ends with an incomplete UTF-8
// encoding it reports incomplete=true so streaming callers can wait for
// more input. Invalid (complete but malformed) bytes encode as U+FFFD.
func encodeToken(p []byte) (tok []byte, size int, incomplete bool) {
	c := p[0]
	switch {
	case c == '\\':
		return []byte(`\\`), 1, false
	case c == '"':
		return []byte(`\"`), 1, false
	case c < 0x20 || c == 0x7f:
		return appendEscapeByte(nil, c), 1, false
	case c < 0x80:
		return []byte{c}, 1, false
	default:
		if !utf8.FullRune(p) {
			return nil, 0, true
		}
		r, size := utf8.DecodeRune(p)
		if r == utf8.RuneError && size == 1 {
			return replacementEscape, 1, false
		}
		return p[:size], size, false
	}
}

// AppendEncode appends the quoted, escaped form of s to dst and returns the
// extended slice. s must be valid UTF-8; malformed bytes are replaced with
// U+FFFD.
func AppendEncode(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		tok, size, incomplete := encodeToken([]byte(s[i:]))
		if incomplete {
			// Truncated UTF-8 at the end of the input: replace each
			// leftover byte with U+FFFD, mirroring DecodeRune.
			tok, size = replacementEscape, 1
		}
		dst = append(dst, tok...)
		i += size
	}
	return append(dst, '"')
}

// Encode returns s embedded in a custom double-quoted string.
func Encode(s string) string {
	return string(AppendEncode(nil, s))
}

// StreamEncoder incrementally encodes input into the quoted wire format.
// Encoded output accumulates in a bounded internal buffer and is drained
// with Read; when the buffer is full Write blocks (backpressure) until the
// reader catches up or the encoder's context is cancelled.
//
// A Write may end in the middle of a multi-byte UTF-8 character; the
// partial encoding is held and combined with the next Write. Close writes
// the closing quote; after the buffered output is drained Read returns
// io.EOF.
//
// StreamEncoder is safe for concurrent use by one writer and one reader.
type StreamEncoder struct {
	ctx  context.Context
	cap  int
	mu   sync.Mutex
	cond *sync.Cond

	buf       []byte // encoded output pending Read
	hold      []byte // incomplete UTF-8 tail awaiting more input
	wroteHead bool
	closed    bool
	cancelErr error
}

// NewStreamEncoder returns an encoder whose output buffer holds at most
// capacity bytes (raised to 8 if smaller, so one escape always fits).
// Cancelling ctx unblocks a Write waiting for buffer space.
func NewStreamEncoder(ctx context.Context, capacity int) *StreamEncoder {
	if capacity < 8 {
		capacity = 8
	}
	e := &StreamEncoder{ctx: ctx, cap: capacity}
	e.cond = sync.NewCond(&e.mu)
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			e.mu.Lock()
			e.cancelErr = ctx.Err()
			e.mu.Unlock()
			e.cond.Broadcast()
		}()
	}
	return e
}

// pushLocked appends one encoded token, blocking while the buffer is full.
func (e *StreamEncoder) pushLocked(tok []byte) error {
	for len(e.buf)+len(tok) > e.cap {
		if e.cancelErr != nil {
			return e.cancelErr
		}
		e.cond.Wait()
	}
	if e.cancelErr != nil {
		return e.cancelErr
	}
	e.buf = append(e.buf, tok...)
	e.cond.Broadcast()
	return nil
}

// Write encodes p into the stream. It returns the number of bytes of p
// consumed; a short count is returned only together with a non-nil error.
// After a cancelled Write the encoder should be abandoned.
func (e *StreamEncoder) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, ErrClosed
	}
	if e.cancelErr != nil {
		return 0, e.cancelErr
	}
	oldHold := len(e.hold)
	data := make([]byte, 0, oldHold+len(p))
	data = append(data, e.hold...)
	data = append(data, p...)
	e.hold = e.hold[:0]

	if !e.wroteHead {
		if err := e.pushLocked([]byte{'"'}); err != nil {
			return 0, err
		}
		e.wroteHead = true
	}
	pos := 0
	for pos < len(data) {
		tok, size, incomplete := encodeToken(data[pos:])
		if incomplete {
			e.hold = append(e.hold, data[pos:]...)
			pos = len(data)
			break
		}
		if err := e.pushLocked(tok); err != nil {
			n := pos - oldHold
			if n < 0 {
				n = 0
			}
			return n, err
		}
		pos += size
	}
	return len(p), nil
}

// Read drains encoded output. It blocks until data is available, the
// encoder is closed and drained (io.EOF), or the context is cancelled.
func (e *StreamEncoder) Read(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.buf) == 0 && !e.closed && e.cancelErr == nil {
		e.cond.Wait()
	}
	n := copy(p, e.buf)
	e.buf = e.buf[n:]
	e.cond.Broadcast()
	if n > 0 {
		return n, nil
	}
	if e.cancelErr != nil && !e.closed {
		return 0, e.cancelErr
	}
	return 0, io.EOF
}

// Close writes the closing quote and marks the stream finished. It may
// block on backpressure like Write.
func (e *StreamEncoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	if !e.wroteHead {
		if err := e.pushLocked([]byte{'"'}); err != nil {
			return err
		}
		e.wroteHead = true
	}
	// Flush any held incomplete UTF-8 bytes as U+FFFD, matching Encode.
	for range e.hold {
		if err := e.pushLocked(replacementEscape); err != nil {
			return err
		}
	}
	e.hold = nil
	if err := e.pushLocked([]byte{'"'}); err != nil {
		return err
	}
	e.closed = true
	e.cond.Broadcast()
	return nil
}
