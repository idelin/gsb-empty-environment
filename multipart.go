package netsec

import (
	"bufio"
	"errors"
	"io"
	"net/textproto"
)

// Errors returned by the multipart decoder.
var (
	// ErrInvalidBoundary is returned when a boundary parameter violates the
	// RFC 2046 bcharsnospace grammar: empty, longer than 70 characters, or
	// containing whitespace / illegal characters.
	ErrInvalidBoundary = errors.New("netsec: invalid multipart boundary")

	// ErrMalformedBoundary is returned when a boundary delimiter line carries
	// illegal transport padding or is otherwise not a valid delimiter.
	ErrMalformedBoundary = errors.New("netsec: malformed multipart boundary line")

	// ErrNotMultipart is returned when a Content-Type value is not a
	// multipart media type.
	ErrNotMultipart = errors.New("netsec: media type is not multipart")

	// ErrMissingBoundary is returned when a multipart Content-Type value has
	// no boundary parameter.
	ErrMissingBoundary = errors.New("netsec: multipart media type has no boundary parameter")

	// ErrMalformedHeader is returned when a part header block cannot be
	// parsed.
	ErrMalformedHeader = errors.New("netsec: malformed part header")
)

const (
	maxBoundaryLength = 70
	peekBufferSize    = 4096
)

var crlf = []byte("\r\n")

// ValidateBoundary reports whether boundary is a legal RFC 2046 boundary
// value. Boundaries use the bcharsnospace production only: linear whitespace
// (space, tab) and control characters are forbidden and the length must be
// between 1 and 70 characters.
func ValidateBoundary(boundary string) error {
	if len(boundary) == 0 || len(boundary) > maxBoundaryLength {
		return ErrInvalidBoundary
	}
	for i := 0; i < len(boundary); i++ {
		if !isBoundaryChar(boundary[i]) {
			return ErrInvalidBoundary
		}
	}
	return nil
}

// isBoundaryChar implements the RFC 2046 bcharsnospace production.
func isBoundaryChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	// '(' ')' '+' '_' ',' '-' '.' '/' ':' '=' '?' and apostrophe (0x27).
	switch c {
	case 0x27, '(', ')', '+', '_', ',', '-', '.', '/', ':', '=', '?':
		return true
	}
	return false
}

// Boundary extracts and validates the boundary parameter from a multipart
// Content-Type header value. Optional double quotes around the parameter
// value are stripped before the boundary is matched against delimiter
// lines, so a quoted Content-Type boundary matches unquoted delimiter
// lines in the body and vice versa.
func Boundary(contentType string) (string, error) {
	mediaType, params, err := ParseMediaType(contentType)
	if err != nil {
		return "", err
	}
	if len(mediaType) < 10 || mediaType[:10] != "multipart/" {
		return "", ErrNotMultipart
	}
	boundary, ok := params["boundary"]
	if !ok {
		return "", ErrMissingBoundary
	}
	if err := ValidateBoundary(boundary); err != nil {
		return "", err
	}
	return boundary, nil
}

// Reader is a streaming iterator over the parts of a MIME multipart body.
// Input is consumed on demand; the reader does not seek.
type Reader struct {
	br *bufio.Reader

	boundary string
	dashB    []byte // "--" + boundary
	nlDashB  []byte // "\r\n" + "--" + boundary

	current   *Part
	partsRead int
	closed    bool
}

// NewReader creates a multipart Reader decoding r with the given boundary.
// The boundary must pass ValidateBoundary. Use Boundary to obtain a ready
// boundary value from a Content-Type header.
func NewReader(r io.Reader, boundary string) (*Reader, error) {
	if err := ValidateBoundary(boundary); err != nil {
		return nil, err
	}
	return &Reader{
		br:       bufio.NewReaderSize(r, peekBufferSize),
		boundary: boundary,
		dashB:    append([]byte("--"), boundary...),
		nlDashB:  append([]byte("\r\n--"), boundary...),
	}, nil
}

// Part represents a single part of a multipart body.
type Part struct {
	// Header holds the part's MIME headers with canonicalized keys, in the
	// same fashion as net/textproto.
	Header textproto.MIMEHeader

	mr *Reader
	pr *partReader
}

// Read returns the body bytes of the part. It returns io.EOF once the
// following boundary delimiter line is reached.
func (p *Part) Read(d []byte) (int, error) {
	return p.pr.Read(d)
}

// Close discards any unread body bytes so that the following boundary
// delimiter can be read by NextPart.
func (p *Part) Close() error {
	_, err := io.Copy(io.Discard, p.pr)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// partReader streams a part body while watching for the next boundary
// delimiter line.
type partReader struct {
	p       *Part
	n       int   // body bytes currently available in the buffered peek
	total   int64 // body bytes already returned
	err     error // terminal error for this part
	readErr error // error observed while filling the buffer
}

func (pr *partReader) Read(d []byte) (int, error) {
	br := pr.p.mr.br

	for pr.n == 0 && pr.err == nil {
		peek, _ := br.Peek(br.Buffered())
		pr.n, pr.err = scanUntilBoundary(peek, pr.total, pr.readErr, pr.p.mr)
		if pr.n == 0 && pr.err == nil {
			// No decision possible from the buffered bytes; force a refill.
			_, pr.readErr = br.Peek(len(peek) + 1)
		}
	}

	if pr.n == 0 {
		return 0, pr.err
	}

	n := len(d)
	if n > pr.n {
		n = pr.n
	}
	n, _ = br.Read(d[:n])
	pr.total += int64(n)
	pr.n -= n
	if pr.n == 0 {
		return n, pr.err
	}
	return n, nil
}
