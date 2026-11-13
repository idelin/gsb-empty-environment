package netsec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Header is the protected JWS header. Raw contains the complete decoded
// header object, including extension parameters not represented by the
// named fields.
type Header struct {
	Alg  string   `json:"alg"`
	Typ  string   `json:"typ,omitempty"`
	Cty  string   `json:"cty,omitempty"`
	Kid  string   `json:"kid,omitempty"`
	Crit []string `json:"crit,omitempty"`
	B64  *bool    `json:"b64,omitempty"`

	Raw map[string]json.RawMessage `json:"-"`
}

// B64Encoded reports whether the payload is base64url encoded as part of the
// JWS signing input. RFC 7515 defaults b64 to true.
func (h Header) B64Encoded() bool {
	return h.B64 == nil || *h.B64
}

// TypeHint returns the header parameter that describes the payload type.
// The standard cty parameter takes precedence. Some producers use a
// non-standard payload/type parameter instead; those are accepted as hints.
func (h Header) TypeHint() string {
	if h.Cty != "" {
		return h.Cty
	}

	for _, name := range []string{
		"payload_type",
		"payloadType",
		"content_type",
		"contentType",
		"type",
	} {
		if value, ok := rawString(h.Raw, name); ok && value != "" {
			return value
		}
	}
	return h.Typ
}

// Compact returns the compact serialization represented by the token. The
// original protected and payload segments are preserved.
func (t *Token) Compact() string {
	if t == nil {
		return ""
	}
	signature := t.SignatureSegment
	if signature == "" {
		signature = base64.RawURLEncoding.EncodeToString(t.Signature)
	}
	return t.Protected + "." + t.PayloadSegment + "." + signature
}

// Token is a parsed and, after verification, authenticated JWS.
type Token struct {
	Header Header

	// Payload is the authenticated payload. In detached mode it is the
	// caller-supplied payload; otherwise it is the token's own payload.
	Payload   []byte
	Signature []byte
	Detached  bool

	// ContentType is the payload type hint from the authenticated header.
	ContentType string
	// PayloadType is an alias for ContentType.
	PayloadType string
	// Type is the JWS typ header, when present.
	Type string

	// Protected, PayloadSegment, and SignatureSegment retain the original
	// compact segments.
	Protected        string
	PayloadSegment   string
	SignatureSegment string
}

// JWS is an alias for Token.
type JWS = Token

// CompactJWS is an alias for Token.
type CompactJWS = Token

// DOT is an alias for Token.
type DOT = Token

// Parse parses a compact JWS without verifying its signature. A token whose
// payload segment is empty is reported as detached-shaped, but Parse never
// supplies an external payload.
func Parse(compact string) (*Token, error) {
	return parseCompact(compact)
}

// ParseCompact is an alias for Parse.
func ParseCompact(compact string) (*Token, error) {
	return parseCompact(compact)
}

// ParseCompactJWS is an alias for Parse.
func ParseCompactJWS(compact string) (*Token, error) {
	return parseCompact(compact)
}

// ParseJWS is an alias for Parse.
func ParseJWS(compact string) (*Token, error) {
	return parseCompact(compact)
}

// ParseDetached parses a detached compact JWS and attaches the external
// payload without verifying the signature. It rejects a token that carries a
// non-empty payload segment. Use Token.VerifyDetached (or Token.Verify after
// this call) to authenticate the result.
func ParseDetached(compact string, args ...any) (*Token, error) {
	token, err := parseCompact(compact)
	if err != nil {
		return nil, err
	}
	if token.PayloadSegment != "" {
		return nil, fmt.Errorf("%w: payload segment must be empty", ErrEmbeddedPayload)
	}

	var payload []byte
	var contentType string
	for _, arg := range args {
		if value, ok := asPayloadBytes(arg); ok {
			if payload == nil {
				payload = value
			}
			continue
		}
		switch value := arg.(type) {
		case nil:
			continue
		case string:
			if contentType == "" {
				contentType = value
			}
		default:
			return nil, fmt.Errorf("%w: unsupported detached parser argument %T", ErrMalformed, value)
		}
	}
	if payload == nil {
		payload = []byte{}
	}
	if contentType != "" {
		if err := checkContentType(token.Header, contentType, true); err != nil {
			return nil, err
		}
	}

	token.Payload = payload
	token.Detached = true
	return token, nil
}

// ParseDetachedCompact is an alias for ParseDetached.
func ParseDetachedCompact(compact string, args ...any) (*Token, error) {
	return ParseDetached(compact, args...)
}

// ParseDetachedJWS is an alias for ParseDetached.
func ParseDetachedJWS(compact string, args ...any) (*Token, error) {
	return ParseDetached(compact, args...)
}

// Decode is an alias for Parse.
func Decode(compact string) (*Token, error) {
	return parseCompact(compact)
}

func parseCompact(compact string) (*Token, error) {
	if compact == "" {
		return nil, fmt.Errorf("%w: empty token", ErrMalformed)
	}

	firstDot := strings.IndexByte(compact, '.')
	lastDot := strings.LastIndexByte(compact, '.')
	if firstDot < 0 || lastDot < 0 || firstDot == lastDot {
		return nil, fmt.Errorf("%w: compact JWS must have header, payload, and signature segments", ErrMalformed)
	}

	headerSegment := compact[:firstDot]
	payloadSegment := compact[firstDot+1 : lastDot]
	signatureSegment := compact[lastDot+1:]

	if headerSegment == "" {
		return nil, fmt.Errorf("%w: empty protected header segment", ErrMalformed)
	}
	if signatureSegment == "" {
		return nil, fmt.Errorf("%w: empty signature segment", ErrMalformed)
	}

	headerBytes, err := decodeBase64URL(headerSegment)
	if err != nil {
		return nil, fmt.Errorf("%w: protected header: %w", ErrMalformed, err)
	}

	header, err := parseHeader(headerBytes)
	if err != nil {
		return nil, err
	}
	if header.Alg == "" {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, ErrMissingAlgorithm)
	}

	var payload []byte
	if header.B64Encoded() {
		payload, err = decodeBase64URL(payloadSegment)
		if err != nil {
			return nil, fmt.Errorf("%w: payload: %w", ErrMalformed, err)
		}
	} else {
		// RFC 7797 serializes the payload without base64url encoding.
		payload = []byte(payloadSegment)
	}

	signature, err := decodeBase64URL(signatureSegment)
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %w", ErrMalformed, err)
	}

	return &Token{
		Header:           header,
		Payload:          payload,
		Signature:        signature,
		Detached:         payloadSegment == "",
		ContentType:      header.TypeHint(),
		PayloadType:      header.TypeHint(),
		Type:             header.Typ,
		Protected:        headerSegment,
		PayloadSegment:   payloadSegment,
		SignatureSegment: signatureSegment,
	}, nil
}

func parseHeader(data []byte) (Header, error) {
	var header Header

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return header, fmt.Errorf("%w: protected header is not a JSON object", ErrMalformed)
	}
	if err := rejectDuplicateKeys(trimmed); err != nil {
		return header, err
	}
	if err := json.Unmarshal(trimmed, &header); err != nil {
		return header, fmt.Errorf("%w: protected header: %v", ErrMalformed, err)
	}
	if err := json.Unmarshal(trimmed, &header.Raw); err != nil {
		return header, fmt.Errorf("%w: protected header: %v", ErrMalformed, err)
	}

	if header.Crit != nil {
		seen := make(map[string]struct{}, len(header.Crit))
		for _, name := range header.Crit {
			if name == "" {
				return header, fmt.Errorf("%w: empty critical header name", ErrMalformed)
			}
			if _, ok := seen[name]; ok {
				return header, fmt.Errorf("%w: duplicate critical header %q", ErrMalformed, name)
			}
			seen[name] = struct{}{}
		}
	}

	return header, nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))

	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: protected header: %v", ErrMalformed, err)
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("%w: protected header is not a JSON object", ErrMalformed)
	}

	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: protected header: %v", ErrMalformed, err)
		}
		name, ok := token.(string)
		if !ok {
			return fmt.Errorf("%w: protected header key is not a string", ErrMalformed)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: duplicate protected header parameter %q", ErrMalformed, name)
		}
		seen[name] = struct{}{}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("%w: protected header: %v", ErrMalformed, err)
		}
	}

	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("%w: protected header: %v", ErrMalformed, err)
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("%w: trailing data after protected header", ErrMalformed)
	}
	return fmt.Errorf("%w: trailing data after protected header", ErrMalformed)
}

func decodeBase64URL(segment string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(segment); err == nil {
		if base64.RawURLEncoding.EncodeToString(decoded) == segment {
			return decoded, nil
		}
	}
	if decoded, err := base64.URLEncoding.DecodeString(segment); err == nil {
		if base64.URLEncoding.EncodeToString(decoded) == segment {
			return decoded, nil
		}
	}
	return nil, ErrNonCanonicalBase64
}

func rawString(raw map[string]json.RawMessage, name string) (string, bool) {
	value, ok := raw[name]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return "", false
	}
	return s, true
}

func asPayloadBytes(value any) ([]byte, bool) {
	switch typed := value.(type) {
	case []byte:
		return typed, true
	case json.RawMessage:
		return []byte(typed), true
	default:
		return nil, false
	}
}
