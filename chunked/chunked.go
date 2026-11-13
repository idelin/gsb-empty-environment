// Package chunked implements the HTTP/1.1 chunked transfer coding with a
// deliberately strict grammar for use at a reverse-proxy trust boundary.
//
// The parser rejects bare LF line endings, non-hexadecimal chunk sizes, and
// CR or LF characters inside quoted chunk extensions. It also provides a
// request framing check that rejects messages carrying both Content-Length
// and Transfer-Encoding.
package chunked

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var (
	// ErrMalformed reports a chunked body that does not conform to the
	// strict grammar enforced by this package.
	ErrMalformed = errors.New("chunked: malformed encoding")

	// ErrUnexpectedEOF reports a chunked body that ended before its final
	// chunk and trailer section were complete.
	ErrUnexpectedEOF = errors.New("chunked: unexpected EOF")

	// ErrConflictingFraming reports a request containing both Content-Length
	// and Transfer-Encoding.
	ErrConflictingFraming = errors.New("chunked: Content-Length and Transfer-Encoding are both present")

	// ErrUnsupportedTransferEncoding reports a Transfer-Encoding whose final
	// coding is not chunked.
	ErrUnsupportedTransferEncoding = errors.New("chunked: unsupported transfer encoding")
)

// ErrInvalidChunkSize is retained as a descriptive alias for callers that
// want to check specifically for chunk-size failures. All such errors also
// match ErrMalformed.
var ErrInvalidChunkSize = ErrMalformed

// ErrCLTEConflict is an alias for ErrConflictingFraming.
var ErrCLTEConflict = ErrConflictingFraming

// Decode reads a complete chunked body from src and returns its decoded
// payload. Trailers are validated but are not returned; use NewReader when
// they are needed.
func Decode(src io.Reader) ([]byte, error) {
	body, _, err := DecodeWithTrailers(src)
	return body, err
}

// DecodeWithTrailers reads a complete chunked body from src and returns both
// the decoded payload and the validated trailer fields.
func DecodeWithTrailers(src io.Reader) ([]byte, http.Header, error) {
	reader := NewReader(src)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, err
	}
	return body, reader.Trailers(), nil
}

// DecodeBytes decodes src as a complete chunked body.
func DecodeBytes(src []byte) ([]byte, error) {
	return Decode(bytes.NewReader(src))
}

// Parse is an alias for Decode.
func Parse(src io.Reader) ([]byte, error) {
	return Decode(src)
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrMalformed}, args...)...)
}

func unexpectedEOF(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrUnexpectedEOF}, args...)...)
}
