package netsec

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"strings"
)

// Field is one ordered header/trailer field instance. Unlike http.Header, a
// slice of Fields preserves both transmission order and repeated instances so
// that both can be bound by the digest.
type Field struct {
	Name  string
	Value string
}

// Sentinel errors returned by Verify. Callers can use errors.Is to distinguish
// the failure class without ever receiving a merged, potentially unauthenticated
// message.
var (
	ErrMalformedField    = errors.New("netsec: malformed header/trailer field")
	ErrForbiddenTrailer  = errors.New("netsec: forbidden trailer field")
	ErrUndeclaredTrailer = errors.New("netsec: trailer field was not declared by the Trailer header")
	ErrTrailerConflict   = errors.New("netsec: trailer field collides with an authenticated header field")
	ErrTruncated         = errors.New("netsec: truncated message: declared trailer field is missing")
	ErrDigestMismatch    = errors.New("netsec: digest verification failed")
	ErrMissingCallback   = errors.New("netsec: digest callback is not configured")
)

// DigestFunc computes the digest over the canonical message bytes. Callers
// supply a real implementation (for example SHA-256 over the input) and choose
// the wire encoding of the returned string.
type DigestFunc func(canonical []byte) (string, error)

// Verifier enforces the gateway trailer policy and verifies the trailer-carried
// digest. The zero value is not usable; construct one with NewVerifier.
type Verifier struct {
	// DigestField is the trailer field name that carries the digest.
	// Defaults to "Digest".
	DigestField string

	// Compute is the real digest callback invoked over the canonical
	// representation of the header and trailer sections.
	Compute DigestFunc

	// Compare optionally overrides digest comparison. When nil a constant-time
	// comparison is used.
	Compare func(want, got string) bool
}

// NewVerifier returns a Verifier using digestField (or "Digest" when empty) and
// the supplied digest callback.
func NewVerifier(digestField string, compute DigestFunc) *Verifier {
	if digestField == "" {
		digestField = "Digest"
	}
	return &Verifier{DigestField: digestField, Compute: compute}
}

func (v *Verifier) init() error {
	if v.DigestField == "" {
		v.DigestField = "Digest"
	}
	if v.Compute == nil {
		return ErrMissingCallback
	}
	if v.Compare == nil {
		v.Compare = func(want, got string) bool {
			return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
		}
	}
	return nil
}

// Message carries the two framed sections of one HTTP message. Both slices
// must be in transmission order and retain duplicate field instances.
type Message struct {
	Headers  []Field
	Trailers []Field
}

// MergedMessage is released to downstream code only after every policy and
// digest check has passed. It never contains the digest field itself.
type MergedMessage struct {
	// Headers is the authenticated header section in original order.
	Headers []Field
	// Trailers is the authenticated trailer section (digest field removed) in
	// original order.
	Trailers []Field
	// Fields is Headers followed by Trailers, giving a single ordered view of
	// the authenticated message.
	Fields []Field
}

// Get returns the first value for the canonical form of name and whether it
// was present.
func (m *MergedMessage) Get(name string) (string, bool) {
	key := textproto.CanonicalMIMEHeaderKey(name)
	for _, f := range m.Fields {
		if f.Name == key {
			return f.Value, true
		}
	}
	return "", false
}

// Values returns every value for the canonical form of name, in order.
func (m *MergedMessage) Values(name string) []string {
	key := textproto.CanonicalMIMEHeaderKey(name)
	var out []string
	for _, f := range m.Fields {
		if f.Name == key {
			out = append(out, f.Value)
		}
	}
	return out
}

// HTTPHeader copies the authenticated fields into an http.Header. Duplicate
// instances are retained as multiple values.
func (m *MergedMessage) HTTPHeader() http.Header {
	h := make(http.Header, len(m.Fields))
	for _, f := range m.Fields {
		h.Add(f.Name, f.Value)
	}
	return h
}

// forbiddenTrailerNames lists authentication, routing and length/framing fields
// that must never be carried in the trailer section, even when predeclared.
var forbiddenTrailerNames = map[string]struct{}{
	// Authentication and credentials.
	"Authorization":       {},
	"Proxy-Authorization": {},
	"Www-Authenticate":    {},
	"Proxy-Authenticate":  {},
	"Cookie":              {},
	"Set-Cookie":          {},
	// Routing and hop-by-hop connection control.
	"Host":              {},
	"Connection":        {},
	"Forwarded":         {},
	"X-Forwarded-For":   {},
	"X-Forwarded-Host":  {},
	"X-Forwarded-Proto": {},
	"Via":               {},
	"Te":                {},
	"Trailer":           {},
	"Upgrade":           {},
	"Keep-Alive":        {},
	// Message length and transfer framing.
	"Content-Length":    {},
	"Transfer-Encoding": {},
}

// isTokenChar reports whether c is an RFC 7230 tchar.
func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// validateSection copies fields with canonicalized names and OWS-trimmed
// values, rejecting names that are not valid tokens (which also excludes
// HTTP/2 pseudo-headers such as ":path") and values that contain framing or
// NUL bytes.
func validateSection(fields []Field, section string) ([]Field, error) {
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		if f.Name == "" {
			return nil, fmt.Errorf("%w: empty field name in %s", ErrMalformedField, section)
		}
		for i := 0; i < len(f.Name); i++ {
			if !isTokenChar(f.Name[i]) {
				return nil, fmt.Errorf("%w: invalid field name %q in %s", ErrMalformedField, f.Name, section)
			}
		}
		for i := 0; i < len(f.Value); i++ {
			switch f.Value[i] {
			case '\r', '\n', 0x00:
				return nil, fmt.Errorf("%w: illegal byte in value of %q in %s", ErrMalformedField, f.Name, section)
			}
		}
		out = append(out, Field{
			Name:  textproto.CanonicalMIMEHeaderKey(f.Name),
			Value: strings.Trim(f.Value, " \t"),
		})
	}
	return out, nil
}

// declaredTrailers parses every Trailer header field into the allowlist of
// trailer names the header section pre-declares.
func declaredTrailers(headers []Field) (map[string]struct{}, error) {
	declared := make(map[string]struct{})
	for _, f := range headers {
		if f.Name != "Trailer" {
			continue
		}
		for _, part := range strings.Split(f.Value, ",") {
			name := strings.Trim(part, " \t")
			if name == "" {
				continue
			}
			for i := 0; i < len(name); i++ {
				if !isTokenChar(name[i]) {
					return nil, fmt.Errorf("%w: invalid Trailer declaration %q", ErrMalformedField, name)
				}
			}
			key := textproto.CanonicalMIMEHeaderKey(name)
			if _, bad := forbiddenTrailerNames[key]; bad {
				return nil, fmt.Errorf("%w: %q must not be declared as a trailer", ErrForbiddenTrailer, key)
			}
			declared[key] = struct{}{}
		}
	}
	return declared, nil
}

// prepare validates both sections, parses the Trailer allowlist, enforces the
// trailer policy and splits out digest-field instances.
func (v *Verifier) prepare(headers, trailers []Field) (head, tail []Field, digestValues []string, err error) {
	if err = v.init(); err != nil {
		return nil, nil, nil, err
	}
	digestKey := textproto.CanonicalMIMEHeaderKey(v.DigestField)

	head, err = validateSection(headers, "header section")
	if err != nil {
		return nil, nil, nil, err
	}
	rawTail, err := validateSection(trailers, "trailer section")
	if err != nil {
		return nil, nil, nil, err
	}

	declared, err := declaredTrailers(head)
	if err != nil {
		return nil, nil, nil, err
	}

	headerNames := make(map[string]struct{}, len(head))
	for _, f := range head {
		headerNames[f.Name] = struct{}{}
	}

	tail = make([]Field, 0, len(rawTail))
	for _, f := range rawTail {
		if f.Name == digestKey {
			digestValues = append(digestValues, f.Value)
			continue
		}
		if _, forbidden := forbiddenTrailerNames[f.Name]; forbidden {
			return nil, nil, nil, fmt.Errorf("%w: %q", ErrForbiddenTrailer, f.Name)
		}
		if _, ok := headerNames[f.Name]; ok {
			return nil, nil, nil, fmt.Errorf("%w: %q", ErrTrailerConflict, f.Name)
		}
		if _, ok := declared[f.Name]; !ok {
			return nil, nil, nil, fmt.Errorf("%w: %q", ErrUndeclaredTrailer, f.Name)
		}
		tail = append(tail, f)
	}
	return head, tail, digestValues, nil
}

// canonicalInput renders both sections into an unambiguous, length-prefixed
// encoding. Each field keeps its position within its own section and repeated
// instances are emitted as repeated lines, so reordering or duplicating any
// field changes the digest. Names and values have already been validated not to
// contain CR/LF, making the line framing unambiguous.
func canonicalInput(headers, trailers []Field) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "headers:%d\r\n", len(headers))
	for _, f := range headers {
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(f.Value)
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "trailers:%d\r\n", len(trailers))
	for _, f := range trailers {
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(f.Value)
		b.WriteString("\r\n")
	}
	return b.Bytes()
}

// Sign computes the digest for a message whose trailers must not already carry
// the digest field. The same policy enforced by Verify applies here, so a
// signed message always verifies when it arrives unchanged.
func (v *Verifier) Sign(headers, trailers []Field) (string, error) {
	head, tail, digestValues, err := v.prepare(headers, trailers)
	if err != nil {
		return "", err
	}
	if len(digestValues) != 0 {
		return "", fmt.Errorf("%w: digest field must not be present when signing", ErrMalformedField)
	}
	digest, err := v.Compute(canonicalInput(head, tail))
	if err != nil {
		return "", fmt.Errorf("netsec: digest computation failed: %w", err)
	}
	return digest, nil
}

// Verify validates the trailer policy and digest of msg. On any failure it
// returns a nil message together with a sentinel error, so downstream code can
// never obtain merged fields without successful authentication.
func (v *Verifier) Verify(msg Message) (*MergedMessage, error) {
	head, tail, digestValues, err := v.prepare(msg.Headers, msg.Trailers)
	if err != nil {
		return nil, err
	}

	digestKey := textproto.CanonicalMIMEHeaderKey(v.DigestField)
	switch {
	case len(digestValues) == 0:
		return nil, fmt.Errorf("%w: %s trailer", ErrTruncated, digestKey)
	case len(digestValues) > 1:
		return nil, fmt.Errorf("%w: repeated %s trailer", ErrMalformedField, digestKey)
	}

	declared, err := declaredTrailers(head)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(tail))
	for _, f := range tail {
		seen[f.Name] = struct{}{}
	}
	for name := range declared {
		if name == digestKey {
			continue
		}
		if _, ok := seen[name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrTruncated, name)
		}
	}

	want, err := v.Compute(canonicalInput(head, tail))
	if err != nil {
		return nil, fmt.Errorf("netsec: digest computation failed: %w", err)
	}
	if !v.Compare(want, digestValues[0]) {
		return nil, ErrDigestMismatch
	}

	merged := &MergedMessage{
		Headers:  append([]Field(nil), head...),
		Trailers: append([]Field(nil), tail...),
	}
	merged.Fields = append(append(make([]Field, 0, len(head)+len(tail)), head...), tail...)
	return merged, nil
}
