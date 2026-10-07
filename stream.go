package stresc

import (
	"context"
	"errors"
	"io"
	"unicode/utf8"
)

// Record is a decoded record together with its byte range in the
// original input stream: [Start, End) covers both quotes.
type Record struct {
	Value string
	Start int
	End   int
}

// Decoder incrementally decodes a stream of quoted records separated
// by a caller-specified separator byte. Escape sequences and surrogate
// pairs may span Feed boundaries. After a decode error the decoder
// resynchronizes at the next separator that is not part of a legal
// escape sequence, then continues with the following record.
type Decoder struct {
	sep        byte
	buf        []byte // unprocessed input
	base       int    // absolute offset of buf[0]
	needSep    bool   // a record just ended; the next byte must be sep
	recovering bool   // scanning for a recovery boundary
}

// NewDecoder returns a Decoder for streams whose records are separated
// by sep.
func NewDecoder(sep byte) *Decoder { return &Decoder{sep: sep} }

// Processed returns the absolute byte offset up to which all fed input
// has been consumed. It is a checkpoint: the parser state at this
// offset is a clean record or recovery boundary.
func (d *Decoder) Processed() int { return d.base }

// Feed appends p to the input stream and processes every complete
// record it contains, returning decoded records and errors with
// absolute byte offsets.
func (d *Decoder) Feed(p []byte) ([]Record, []Error) {
	d.buf = append(d.buf, p...)
	return d.process(false)
}

// Finish processes any remaining input, reporting truncated records as
// errors instead of waiting for more data.
func (d *Decoder) Finish() ([]Record, []Error) {
	return d.process(true)
}

func (d *Decoder) process(final bool) (recs []Record, errs []Error) {
	pos := 0
	for pos < len(d.buf) {
		if d.recovering {
			next, ok := scanRecovery(d.buf, pos, d.sep)
			if ok {
				pos = next
				d.recovering = false
				continue
			}
			if final {
				pos = len(d.buf)
			} else {
				pos = next // drop scanned garbage, keep scanning later
			}
			break
		}
		if d.needSep {
			if d.buf[pos] == d.sep {
				pos++
				d.needSep = false
				continue
			}
			errs = append(errs, Error{Offset: d.base + pos, Msg: "expected record separator"})
			d.needSep = false
			next, ok := scanRecovery(d.buf, pos, d.sep)
			if ok {
				pos = next
				continue
			}
			if final {
				pos = len(d.buf)
			} else {
				pos = next
				d.recovering = true
			}
			break
		}
		if d.buf[pos] == d.sep {
			pos++
			continue
		}
		v, n, err := parseQuoted(d.buf[pos:])
		if err == nil {
			recs = append(recs, Record{Value: v, Start: d.base + pos, End: d.base + pos + n})
			pos += n
			d.needSep = true
			continue
		}
		if err == errIncomplete {
			if final {
				errs = append(errs, Error{Offset: d.base + pos, Msg: "truncated record"})
				pos = len(d.buf)
				continue
			}
			break // wait for more input
		}
		e := err.(*Error)
		errs = append(errs, Error{Offset: d.base + pos + e.Offset, Msg: e.Msg})
		next, ok := scanRecovery(d.buf, pos+e.Offset, d.sep)
		if ok {
			pos = next
			continue
		}
		if final {
			pos = len(d.buf)
			continue
		}
		pos = next
		d.recovering = true
		break
	}
	if pos > 0 {
		d.buf = append([]byte(nil), d.buf[pos:]...)
		d.base += pos
	}
	return recs, errs
}

// scanRecovery scans buf[from:] for sep, skipping over legal escape
// sequences: a sep inside a legal escape is not a record boundary. It
// returns the index just past the boundary separator. If no boundary
// is found, ok is false and next is a safe trim point: buf[from:next]
// is guaranteed to contain no boundary.
func scanRecovery(buf []byte, from int, sep byte) (next int, ok bool) {
	i := from
	for i < len(buf) {
		c := buf[i]
		if c == sep {
			return i + 1, true
		}
		if c == '\\' {
			n, ok := escapeLen(buf, i)
			if !ok {
				return i, false // buffer ends inside a possible escape
			}
			if n > 0 {
				i += n
				continue
			}
		}
		i++
	}
	return len(buf), false
}

// Encoder incrementally encodes text and writes the quoted output to
// an io.Writer. It honors backpressure: Write stops consuming input
// when the internal buffer reaches its cap and reports a short count.
// All operations abort promptly when the encoder's context is
// canceled. Multi-byte UTF-8 sequences may span Write calls.
type Encoder struct {
	ctx     context.Context
	w       io.Writer
	pending []byte // encoded output not yet flushed
	tail    []byte // buffered partial UTF-8 sequence
	maxBuf  int
	inOff   int // absolute offset of the next unconsumed input byte
	opened  bool
	closed  bool
}

var errClosed = errors.New("stresc: encoder closed")

// NewEncoder returns an Encoder writing to w. Operations check ctx
// before every underlying write and fail with the context's error once
// it is canceled. A nil ctx means context.Background().
func NewEncoder(ctx context.Context, w io.Writer) *Encoder {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Encoder{ctx: ctx, w: w, maxBuf: 4096}
}

// SetMaxBuffer sets the soft cap in bytes on the internal output
// buffer. When the cap is reached Write stops consuming input,
// signalling backpressure to the caller.
func (e *Encoder) SetMaxBuffer(n int) {
	if n < 8 {
		n = 8
	}
	e.maxBuf = n
}

// Consumed returns the total number of input bytes consumed so far.
func (e *Encoder) Consumed() int { return e.inOff }

// Write encodes p and flushes pending output. It returns the number of
// input bytes consumed; a count below len(p) with a nil error signals
// backpressure and the caller should retry the remainder. After an
// error the encoder must not be reused.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errClosed
	}
	e.open()
	n, err := e.encode(p)
	e.inOff += n
	if ferr := e.flush(); ferr != nil && err == nil {
		err = ferr
	}
	return n, err
}

// Close writes the closing quote and flushes remaining output.
func (e *Encoder) Close() error {
	if e.closed {
		return errClosed
	}
	e.open()
	e.closed = true
	if len(e.tail) > 0 {
		return &Error{Offset: e.inOff, Msg: "truncated UTF-8 in input"}
	}
	e.pending = append(e.pending, '"')
	return e.flush()
}

func (e *Encoder) open() {
	if !e.opened {
		e.pending = append(e.pending, '"')
		e.opened = true
	}
}

// encode consumes as much of p as the buffer cap allows and returns
// the number of input bytes consumed.
func (e *Encoder) encode(p []byte) (int, error) {
	i := 0
	if len(e.tail) > 0 {
		full := runeLen(e.tail[0])
		if full == 0 {
			return 0, &Error{Offset: e.inOff, Msg: "invalid UTF-8"}
		}
		k := full - len(e.tail)
		if k > len(p) {
			k = len(p)
		}
		e.tail = append(e.tail, p[:k]...)
		i = k
		if len(e.tail) < full {
			return i, nil // all of p buffered in the tail
		}
		r, size := utf8.DecodeRune(e.tail)
		if r == utf8.RuneError && size == 1 {
			return 0, &Error{Offset: e.inOff, Msg: "invalid UTF-8"}
		}
		e.pending = encodeRune(e.pending, r)
		e.tail = e.tail[:0]
	}
	for i < len(p) {
		if len(e.pending) >= e.maxBuf {
			return i, nil // backpressure
		}
		c := p[i]
		if c < 0x80 {
			e.pending = encodeRune(e.pending, rune(c))
			i++
			continue
		}
		if !utf8.FullRune(p[i:]) {
			e.tail = append(e.tail[:0], p[i:]...)
			return len(p), nil
		}
		r, size := utf8.DecodeRune(p[i:])
		if r == utf8.RuneError && size == 1 {
			return i, &Error{Offset: e.inOff + i, Msg: "invalid UTF-8"}
		}
		e.pending = encodeRune(e.pending, r)
		i += size
	}
	return i, nil
}

// flush writes pending output to the underlying writer, handling short
// writes and checking for cancellation before every call.
func (e *Encoder) flush() error {
	for len(e.pending) > 0 {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		n, err := e.w.Write(e.pending)
		e.pending = e.pending[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// runeLen returns the expected length of the UTF-8 sequence starting
// with b, or 0 if b cannot start a sequence.
func runeLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b < 0xE0:
		return 2
	case b >= 0xE0 && b < 0xF0:
		return 3
	case b >= 0xF0 && b < 0xF5:
		return 4
	}
	return 0
}
