package antismuggle

import "io"

// Config controls the strict framing policy and resource limits. The zero
// value is not usable; call DefaultConfig and tune the fields they need.
type Config struct {
	// MaxHeaderLine caps the length of the request line and each raw header
	// line (excluding the terminating CRLF).
	MaxHeaderLine int

	// MaxHeaderCount caps the number of header fields per request.
	MaxHeaderCount int

	// MaxBody caps the number of decoded body bytes accepted per request,
	// for both identity and chunked transfer coding.
	MaxBody int64

	// MaxChunkSize caps a single chunk-size value. A larger chunk is rejected
	// before any of its data is read.
	MaxChunkSize int64

	// MaxChunkExtension caps the raw byte length of a chunk extension (the
	// text after the first ';').
	MaxChunkExtension int

	// RejectDuplicateContentLength rejects every request carrying more than
	// one Content-Length field, even when the values are identical. This is
	// the strict default recommended for smuggling defence.
	RejectDuplicateContentLength bool

	// AllowChunkedCaseInsensitive permits "Transfer-Encoding: Chunked" and
	// other case variants. The default (false) demands the exact lowercase
	// token "chunked" so that casing-based TE.TE obfuscation is rejected.
	AllowChunkedCaseInsensitive bool

	// AllowedTrailers lists trailer field names (lowercase) that may appear
	// after the last chunk. An empty slice means trailers are forbidden
	// outright. Forbidden hop/security headers are always blocked on top.
	AllowedTrailers []string

	// DecodeChunkedToBody makes Normalize decode chunked requests into an
	// identity body with a single Content-Length and no TE/trailers. When
	// false the validated chunked wire frame is passed through unchanged.
	DecodeChunkedToBody bool
}

// DefaultConfig returns a strict, resource-bounded configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxHeaderLine:                8192,
		MaxHeaderCount:               100,
		MaxBody:                      16 << 20, // 16 MiB
		MaxChunkSize:                 16 << 20,
		MaxChunkExtension:            1024,
		RejectDuplicateContentLength: true,
		AllowChunkedCaseInsensitive:  false,
		AllowedTrailers:              nil,
		DecodeChunkedToBody:          true,
	}
}

// BodyTransfer classifies the framing of a request body.
type BodyTransfer string

const (
	// BodyNone means the request has no body framing (no CL, no TE).
	BodyNone BodyTransfer = "none"
	// BodyIdentity means a Content-Length delimited body.
	BodyIdentity BodyTransfer = "identity"
	// BodyChunked means a Transfer-Encoding: chunked body.
	BodyChunked BodyTransfer = "chunked"
)

// Field is one validated raw header field. Value excludes the separating
// colon and leading OWS; all bytes have passed the field-value check.
type Field struct {
	Name  string
	Value string
	// RawValue is the field value before OWS trimming. Framing headers
	// (Content-Length, Transfer-Encoding) must be validated against this so
	// that leading/trailing whitespace obfuscation cannot slip through.
	RawValue string
}

// Request is one strictly validated HTTP/1.1 request frame.
type Request struct {
	Method  string
	Target  string
	Version string // always "HTTP/1.0" or "HTTP/1.1"

	// Fields holds every header in wire order.
	Fields []Field

	// Transfer is the validated body framing.
	Transfer BodyTransfer

	// ContentLength is the parsed length when Transfer == BodyIdentity.
	ContentLength int64

	// KeepAlive reports whether the connection-level policy allows another
	// request after this body is fully consumed.
	KeepAlive bool

	// Body streams the raw (identity) or decoded (chunked) body bytes. It is
	// nil for BodyNone. It always ends with io.EOF exactly at the frame
	// boundary so the next request begins on the next byte.
	Body io.ReadCloser

	// rawChunked holds the validated chunked wire bytes once the body is fully
	// read; empty for identity/none transfers.
	rawChunked []byte

	cfg  *Config
	conn *Conn
}

// Get returns the first value for the given (case-insensitive) field name.
func (r *Request) Get(name string) (string, bool) {
	for _, f := range r.Fields {
		if equalFoldASCII(f.Name, name) {
			return f.Value, true
		}
	}
	return "", false
}

// All returns every value for the given (case-insensitive) field name.
func (r *Request) All(name string) []string {
	var out []string
	for _, f := range r.Fields {
		if equalFoldASCII(f.Name, name) {
			out = append(out, f.Value)
		}
	}
	return out
}
