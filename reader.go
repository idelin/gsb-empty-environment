package netsec

import (
	"bytes"
	"fmt"
	"io"
)

// NextPart returns the next part. io.EOF is returned once the closing
// boundary delimiter has been consumed.
func (r *Reader) NextPart() (*Part, error) {
	if r.current != nil {
		if err := r.current.Close(); err != nil {
			return nil, err
		}
		r.current = nil
	}
	if r.closed {
		return nil, io.EOF
	}

	for {
		line, err := r.br.ReadBytes('\n')
		if err == io.EOF {
			if r.isClosingBoundary(line) {
				r.closed = true
				return nil, io.EOF
			}
			if len(line) == 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("%w: %q", ErrMalformedBoundary, line)
		}
		if err != nil {
			return nil, err
		}

		delim, final, bad := r.classifyBoundaryLine(line)
		if bad {
			return nil, fmt.Errorf("%w: %q", ErrMalformedBoundary, line)
		}
		if final {
			r.closed = true
			return nil, io.EOF
		}
		if delim {
			r.partsRead++
			return r.newPart()
		}

		if r.partsRead > 0 {
			// The CRLF terminating the previous body is read separately;
			// anything else here means the stream is out of sync.
			if bytes.Equal(line, crlf) {
				continue
			}
			return nil, fmt.Errorf("netsec: expected boundary delimiter, got %q", line)
		}
		// Before the first delimiter, any line is preamble and is skipped.
	}
}

// classifyBoundaryLine inspects a complete line including its trailing LF
// and reports whether it is a part delimiter, a closing delimiter, or
// neither / malformed.
func (r *Reader) classifyBoundaryLine(line []byte) (delim, final, bad bool) {
	if !bytes.HasPrefix(line, r.dashB) {
		return false, false, false
	}

	rest := line[len(r.dashB):]
	closing := false
	if bytes.HasPrefix(rest, []byte("--")) {
		closing = true
		rest = rest[2:]
	} else if len(rest) == 0 ||
		(rest[0] != ' ' && rest[0] != '\t' && rest[0] != '\r') {
		// A longer token sharing the boundary prefix (e.g. "--boundaryX")
		// is an ordinary line, not a delimiter.
		return false, false, false
	}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case ' ', '\t':
			continue
		case '\r':
			if i+1 < len(rest) && rest[i+1] == '\n' && i+1 == len(rest)-1 {
				return !closing, closing, false
			}
			return false, false, true
		case '\n':
			// A bare LF means the required CRLF terminator is missing.
			return false, false, true
		default:
			return false, false, true
		}
	}

	// ReadBytes guarantees a trailing LF; an unterminated line is malformed.
	return false, false, true
}

// isClosingBoundary handles a trailing line read without an LF: only a
// complete closing delimiter (optionally SP/HT padded) is accepted.
func (r *Reader) isClosingBoundary(line []byte) bool {
	prefix := append(append([]byte{}, r.dashB...), '-', '-')
	if !bytes.HasPrefix(line, prefix) {
		return false
	}
	rest := line[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] != ' ' && rest[i] != '\t' {
			return false
		}
	}
	return true
}

// newPart reads the header block following a delimiter line and wires up
// the streaming body reader.
func (r *Reader) newPart() (*Part, error) {
	header, err := readMIMEHeader(r.br)
	if err != nil {
		return nil, err
	}
	p := &Part{Header: header, mr: r}
	p.pr = &partReader{p: p}
	r.current = p
	return p, nil
}
