package starttls

import (
	"errors"
	"io"
)

// lineReader is the Engine-owned buffered reader. Every byte consumed from
// the connection in a phase passes through it, and this buffer is the only
// place pre-upgrade plaintext can accumulate. reset scrubs it.
type lineReader struct {
	conn   DuplexConn
	maxLen int
	buf    []byte
}

func newLineReader(conn DuplexConn, maxLen int) *lineReader {
	return &lineReader{
		conn:   conn,
		maxLen: maxLen,
		buf:    make([]byte, 0, 256),
	}
}

// buffered reports unconsumed bytes held by the parser. A positive value at
// upgrade time means data was pipelined across the STARTTLS boundary.
func (r *lineReader) buffered() int {
	return len(r.buf)
}

// reset zeroes and empties the buffer so plaintext content cannot be reused
// after the upgrade boundary.
func (r *lineReader) reset() {
	for i := range r.buf {
		r.buf[i] = 0
	}
	r.buf = r.buf[:0]
}

// readLine returns one CRLF-terminated line without its terminator.
//
// Framing is strict: the line must end in CRLF, must contain no other CR or
// LF bytes, and every byte must be printable ASCII (0x20..0x7e). A bare LF,
// an embedded CR or any non-printable byte yields ErrBadLine. If EOF arrives
// before the terminating CRLF the partial line is reported with ErrBadLine
// (io.EOF alone is returned when no bytes are pending).
func (r *lineReader) readLine() (string, error) {
	for {
		// Check framing first: malformed bytes must be rejected even when a
		// later, valid CRLF exists in the same buffer (e.g. "a\nb\r\n").
		if bad := malformedTerminator(r.buf); bad {
			return "", ErrBadLine
		}
		if idx := indexCRLF(r.buf); idx >= 0 {
			if idx > r.maxLen {
				return "", ErrLineTooLong
			}
			line := r.buf[:idx]
			if err := validateLine(line); err != nil {
				return "", err
			}
			out := string(line)
			// Keep trailing bytes (pipelined lines) in the buffer; they are
			// exactly what BeginUpgrade must be able to detect.
			r.buf = shiftOut(r.buf, idx+2)
			return out, nil
		}

		if len(r.buf) > r.maxLen {
			return "", ErrLineTooLong
		}

		tmp := make([]byte, 256)
		n, err := r.conn.Read(tmp)
		if n > 0 {
			r.buf = append(r.buf, tmp[:n]...)
		}
		if err != nil {
			if len(r.buf) == 0 {
				// Normalize any EOF-like indication (including a queued
				// zero-byte read) to the io.EOF sentinel.
				if errors.Is(err, io.EOF) {
					return "", io.EOF
				}
				return "", err
			}
			if errors.Is(err, io.EOF) {
				// Partial line at EOF (or read error): never treat buffered
				// plaintext as a complete command.
				return "", ErrBadLine
			}
			// A non-EOF read error with buffered bytes keeps its cause.
			return "", err
		}
	}
}

// malformedTerminator reports whether b contains an unavoidable framing
// error before any complete CRLF line: an LF whose predecessor is not CR
// (bare LF), or a CR not followed by LF at the end of the current buffer
// (which could only be rescued by a following LF, checked on the next loop;
// here it is only fatal when followed by another byte).
func malformedTerminator(b []byte) bool {
	for i, c := range b {
		switch c {
		case '\n':
			if i == 0 || b[i-1] != '\r' {
				return true
			}
		case '\r':
			if i == len(b)-1 {
				// May become CRLF once the next byte arrives.
				continue
			}
			if b[i+1] != '\n' {
				return true
			}
		}
	}
	return false
}

// indexCRLF returns the index of the first CRLF sequence or -1.
func indexCRLF(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\r' && b[i+1] == '\n' {
			return i
		}
	}
	return -1
}

// validateLine enforces printable-ASCII content without embedded line
// endings; the CRLF terminator has already been stripped by the caller.
func validateLine(line []byte) error {
	if len(line) == 0 {
		return ErrBadLine
	}
	for _, c := range line {
		if c < 0x20 || c > 0x7e {
			return ErrBadLine
		}
	}
	return nil
}

// shiftOut removes the first n bytes while preserving any pipelined tail.
func shiftOut(buf []byte, n int) []byte {
	remaining := copy(buf, buf[n:])
	for i := remaining; i < len(buf); i++ {
		buf[i] = 0
	}
	return buf[:remaining]
}
