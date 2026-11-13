package netsec

import (
	"errors"
	"net/http"
	"strings"
)

// TrailerField is a single trailer block field as it arrives after the
// request/response body. Name keeps its on-the-wire spelling and is matched
// case-insensitively; Value is the field value and is not used for the
// routing-safety decision.
type TrailerField struct {
	Name  string
	Value string
}

// StreamError marks a trailer block as unsafe to forward. A gateway that
// receives one of these errors must fail the HTTP/2 (or HTTP/3) stream rather
// than propagate the offending field.
type StreamError struct {
	// Field is the offending trailer name in its original spelling.
	Field string
	// Reason explains why the field is rejected.
	Reason error
}

func (e *StreamError) Error() string {
	return "netsec: trailer field " + strconvQuote(e.Field) + " rejected: " + e.Reason.Error()
}

// Unwrap exposes the underlying sentinel so callers can use errors.Is.
func (e *StreamError) Unwrap() error { return e.Reason }

func strconvQuote(s string) string {
	return "\"" + s + "\""
}

var (
	// ErrForbiddenTrailer is returned when a trailer carries a field that may
	// only be trusted on the message head: host, content-length,
	// transfer-encoding or any pseudo-header (":..."). Because trailers arrive
	// after routing has completed, honoring such a field would let a peer
	// re-target the route or smuggle framing, so the stream must fail.
	ErrForbiddenTrailer = errors.New("forbidden trailer field")

	// ErrUndeclaredTrailer is returned in strict mode when a trailer was not
	// announced ahead of the body in the Trailer header (RFC 7230 section 4.4).
	ErrUndeclaredTrailer = errors.New("undeclared trailer field")
)

// forbiddenTrailers is the fixed set of ordinary header fields that are never
// legal in a trailer block. Matching is done on lowercased, trimmed names.
var forbiddenTrailers = map[string]struct{}{
	"host":              {},
	"content-length":    {},
	"transfer-encoding": {},
}

// canonicalTrailer normalizes a trailer field name for comparison: surrounding
// whitespace is removed and the result is lowercased, mirroring HTTP's
// case-insensitive field naming.
func canonicalTrailer(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ForbiddenTrailer reports whether name is prohibited in a trailer block.
//
// The forbidden set is:
//   - host
//   - content-length
//   - transfer-encoding
//   - every pseudo-header, i.e. a name whose trimmed form starts with ":"
//     (for example ":path", ":authority", ":method", ":scheme", ":status")
func ForbiddenTrailer(name string) bool {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, ":") {
		return true
	}
	_, ok := forbiddenTrailers[canonicalTrailer(name)]
	return ok
}

// Option configures a TrailerValidator.
type Option func(*TrailerValidator)

// WithRequireDeclared turns on RFC 7230 declaration enforcement: only trailer
// fields previously announced in the message's Trailer header are accepted.
// Forbidden fields are still rejected first regardless of this setting. The
// default accepts any non-forbidden trailer.
func WithRequireDeclared(require bool) Option {
	return func(v *TrailerValidator) {
		v.requireDeclared = require
	}
}

// TrailerValidator adjudicates the trailing fields of one HTTP exchange. It is
// safe for concurrent use because its configuration is immutable after
// construction.
//
// The validator enforces the gateway rule that trailers can never participate
// in routing or framing: host, content-length, transfer-encoding and
// pseudo-headers fail the stream, while declared custom trailers are allowed to
// arrive once the body is complete.
type TrailerValidator struct {
	declared        map[string]struct{}
	requireDeclared bool
}

// NewTrailerValidator builds a TrailerValidator. declared lists the custom
// trailer names announced in the request's Trailer header; spelling and case do
// not matter. WithRequireDeclared optionally rejects anything not announced.
func NewTrailerValidator(declared []string, opts ...Option) *TrailerValidator {
	v := &TrailerValidator{
		declared: make(map[string]struct{}, len(declared)),
	}
	for _, name := range declared {
		if canonical := canonicalTrailer(name); canonical != "" {
			v.declared[canonical] = struct{}{}
		}
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// ValidateFields adjudicates a raw trailer block given as ordered name/value
// pairs. It returns a *StreamError describing the first offending field, or nil
// when the whole block is safe to forward.
func (v *TrailerValidator) ValidateFields(fields []TrailerField) error {
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if ForbiddenTrailer(name) {
			return &StreamError{Field: field.Name, Reason: ErrForbiddenTrailer}
		}
		if v.requireDeclared {
			if _, ok := v.declared[canonicalTrailer(name)]; !ok {
				return &StreamError{Field: field.Name, Reason: ErrUndeclaredTrailer}
			}
		}
	}
	return nil
}

// Validate adjudicates an already-parsed http.Header trailer block. Header
// values do not affect the decision and all values of a repeated field are
// considered once per field name.
func (v *TrailerValidator) Validate(header http.Header) error {
	return v.ValidateFields(headerToFields(header))
}

// ValidateTrailers is the package-level one-shot form for callers that do not
// need a reusable, declaration-aware validator. It rejects any forbidden
// trailer field and otherwise accepts the block.
func ValidateTrailers(header http.Header) error {
	return NewTrailerValidator(nil).Validate(header)
}

// ValidateTrailerFields is the package-level one-shot form for raw name/value
// pairs. It rejects any forbidden trailer field and otherwise accepts the
// block.
func ValidateTrailerFields(fields []TrailerField) error {
	return NewTrailerValidator(nil).ValidateFields(fields)
}

func headerToFields(header http.Header) []TrailerField {
	fields := make([]TrailerField, 0, len(header))
	for name, values := range header {
		for _, value := range values {
			fields = append(fields, TrailerField{Name: name, Value: value})
		}
	}
	return fields
}
