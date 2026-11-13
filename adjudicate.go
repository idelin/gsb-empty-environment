package netsec

import (
	"errors"
	"net/http"
	"strings"
)

// HeaderField is one decoded HTTP/2 header field in the order it appeared in
// the HEADERS block. The Name includes the leading ":" for pseudo-headers.
type HeaderField struct {
	Name  string
	Value string
}

// ErrorCode identifies the class of an HTTP/2 stream-level adjudication error.
type ErrorCode string

const (
	// ErrorForbiddenHeader means a connection-specific header field that is
	// illegal in HTTP/2 was found among the regular (non-pseudo) headers.
	ErrorForbiddenHeader ErrorCode = "forbidden_header"

	// ErrorPseudoAfterRegular means a pseudo-header field appeared after a
	// regular header field, which violates the HTTP/2 field ordering rule.
	ErrorPseudoAfterRegular ErrorCode = "pseudo_after_regular"
)

// StreamError is the error returned when a request must fail at the stream
// level. A protocol gateway maps it to RST_STREAM carrying PROTOCOL_ERROR.
type StreamError struct {
	Code       ErrorCode
	HeaderName string
}

func (e *StreamError) Error() string {
	switch e.Code {
	case ErrorForbiddenHeader:
		return "http2: forbidden connection-specific header field: " + e.HeaderName
	case ErrorPseudoAfterRegular:
		return "http2: pseudo-header field after regular header field: " + e.HeaderName
	default:
		return "http2: stream error: " + string(e.Code)
	}
}

// forbiddenRegularHeaders is the HTTP/1 connection-specific header field set
// that RFC 7540 section 8.1.2.2 forbids in HTTP/2. Header field names are
// matched case-insensitively after lowercasing, as required for HTTP/2.
var forbiddenRegularHeaders = map[string]struct{}{
	"connection":        {},
	"keep-alive":        {},
	"transfer-encoding": {},
	"upgrade":           {},
}

// IsForbiddenHeader reports whether name is one of the HTTP/2-forbidden
// connection-specific header fields. Pseudo-header names (":method", etc.)
// never match. The comparison is case-insensitive.
func IsForbiddenHeader(name string) bool {
	if strings.HasPrefix(name, ":") {
		return false
	}
	_, ok := forbiddenRegularHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// AdjudicateRequest validates a decoded HTTP/2 request header block.
//
// It returns nil for a legal request without mutating or removing any field:
// ordinary custom headers are preserved. It returns a *StreamError when:
//
//   - A regular (non-pseudo) field named connection, keep-alive,
//     transfer-encoding or upgrade is present (the request must be rejected,
//     not forwarded, to avoid HTTP/1 request smuggling on downgrade), or
//   - A pseudo-header field appears after a regular header field.
func AdjudicateRequest(fields []HeaderField) error {
	seenRegular := false
	for _, field := range fields {
		if strings.HasPrefix(field.Name, ":") {
			if seenRegular {
				return &StreamError{Code: ErrorPseudoAfterRegular, HeaderName: field.Name}
			}
			continue
		}
		seenRegular = true
		if IsForbiddenHeader(field.Name) {
			return &StreamError{Code: ErrorForbiddenHeader, HeaderName: strings.ToLower(strings.TrimSpace(field.Name))}
		}
	}
	return nil
}

// AdjudicateHTTPHeader applies the forbidden-header rule to a canonicalized
// http.Header as carried by net/http. Pseudo-headers are not representable in
// http.Header, so only the connection-specific field check is performed.
// Custom header fields are retained.
func AdjudicateHTTPHeader(header http.Header) error {
	for name := range header {
		if IsForbiddenHeader(name) {
			return &StreamError{Code: ErrorForbiddenHeader, HeaderName: strings.ToLower(strings.TrimSpace(name))}
		}
	}
	return nil
}

// AsStreamError unwraps err into a *StreamError. It returns nil when err is
// not an HTTP/2 stream-level adjudication error.
func AsStreamError(err error) *StreamError {
	var streamErr *StreamError
	if errors.As(err, &streamErr) {
		return streamErr
	}
	return nil
}
