package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// Envelope field names. Every received message must contain exactly this set.
const (
	FieldVersion = "version"
	FieldLength  = "length"
	FieldPayload = "payload"
	FieldDigest  = "digest"
)

// fieldOrder is the single key order used for canonical encoding. Input
// messages may present the fields in any order.
var fieldOrder = []string{FieldVersion, FieldLength, FieldPayload, FieldDigest}

var knownFields = map[string]struct{}{
	FieldVersion: {},
	FieldLength:  {},
	FieldPayload: {},
	FieldDigest:  {},
}

// Sentinel errors. Concrete failures wrap one of these so callers can use
// errors.Is to classify a rejection.
var (
	// ErrMalformed covers grammar, type, missing-field and number-shape errors.
	ErrMalformed = errors.New("malformed update message")
	// ErrDuplicateKey is returned when an object repeats a key.
	ErrDuplicateKey = errors.New("duplicate object key")
	// ErrUnknownField is returned when an object carries a non-specified key.
	ErrUnknownField = errors.New("unknown object field")
	// ErrOutOfRange is returned when an integer exceeds its declared width.
	ErrOutOfRange = errors.New("integer out of range")
	// ErrDigestMismatch is returned when the digest field does not verify.
	ErrDigestMismatch = errors.New("digest verification failed")
	// ErrParserDivergence means the two independent parsing paths accepted the
	// same input but derived different semantics from it.
	ErrParserDivergence = errors.New("parser paths disagree on message semantics")
)

// Message is the validated, semantic form of an update envelope.
type Message struct {
	// Version is a canonical uint32 decimal integer.
	Version uint32
	// Length is a canonical uint64 decimal integer and equals len(Payload).
	Length uint64
	// Payload is the base64-decoded update payload.
	Payload []byte
	// Digest is the lowercase hex SHA-256 over the canonical core encoding.
	Digest string
}

// valueKind tags the intermediate values produced by both parsing paths.
type valueKind int

const (
	kindNull valueKind = iota
	kindBool
	kindInteger
	kindString
	kindArray
	kindObject
)

// rawValue is parser-neutral intermediate representation. For kindInteger it
// holds the exact source number text; range and shape are validated later by
// shared code so that both parsers apply identical number rules.
type rawValue struct {
	kind valueKind
	text any
}

// rawField preserves source occurrence order, which the duplicate-key checks
// rely on.
type rawField struct {
	name string
	val  rawValue
}

// Receive validates raw as a canonical update envelope. The input is parsed by
// two independent parser implementations; the message is accepted only when
// both paths agree on its semantics, the canonical encodings match and the
// digest verifies. Any malformed or mutated sample is rejected.
func Receive(raw []byte) (*Message, error) {
	handFields, err := parseHand(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	stdFields, err := parseStd(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if !reflect.DeepEqual(handFields, stdFields) {
		return nil, ErrParserDivergence
	}

	handMsg, err := buildMessage(handFields)
	if err != nil {
		return nil, err
	}
	stdMsg, err := buildMessage(stdFields)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(handMsg, stdMsg) {
		return nil, ErrParserDivergence
	}
	if !bytes.Equal(Canonical(handMsg), Canonical(stdMsg)) {
		return nil, ErrParserDivergence
	}
	return handMsg, nil
}

// buildMessage applies the schema, number and digest rules to the shared
// intermediate representation produced by either parser.
func buildMessage(fields []rawField) (*Message, error) {
	index := make(map[string]rawValue, len(fieldOrder))
	for _, field := range fields {
		if _, ok := knownFields[field.name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownField, field.name)
		}
		if _, dup := index[field.name]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, field.name)
		}
		index[field.name] = field.val
	}

	msg := &Message{}

	version, ok := index[FieldVersion]
	if !ok {
		return nil, fmt.Errorf("%w: missing field %q", ErrMalformed, FieldVersion)
	}
	if version.kind != kindInteger {
		return nil, fmt.Errorf("%w: field %q must be an integer", ErrMalformed, FieldVersion)
	}
	versionText, ok := version.text.(string)
	if !ok {
		return nil, fmt.Errorf("%w: field %q has corrupted value", ErrMalformed, FieldVersion)
	}
	versionN, err := parseCanonicalUint(versionText, 32)
	if err != nil {
		return nil, err
	}
	msg.Version = uint32(versionN)

	length, ok := index[FieldLength]
	if !ok {
		return nil, fmt.Errorf("%w: missing field %q", ErrMalformed, FieldLength)
	}
	if length.kind != kindInteger {
		return nil, fmt.Errorf("%w: field %q must be an integer", ErrMalformed, FieldLength)
	}
	lengthText, ok := length.text.(string)
	if !ok {
		return nil, fmt.Errorf("%w: field %q has corrupted value", ErrMalformed, FieldLength)
	}
	msg.Length, err = parseCanonicalUint(lengthText, 64)
	if err != nil {
		return nil, err
	}

	payload, ok := index[FieldPayload]
	if !ok {
		return nil, fmt.Errorf("%w: missing field %q", ErrMalformed, FieldPayload)
	}
	if payload.kind != kindString {
		return nil, fmt.Errorf("%w: field %q must be a string", ErrMalformed, FieldPayload)
	}
	payloadText, ok := payload.text.(string)
	if !ok {
		return nil, fmt.Errorf("%w: field %q has corrupted value", ErrMalformed, FieldPayload)
	}
	msg.Payload, err = base64.StdEncoding.DecodeString(payloadText)
	if err != nil {
		return nil, fmt.Errorf("%w: field %q is not valid base64: %v", ErrMalformed, FieldPayload, err)
	}
	if uint64(len(msg.Payload)) != msg.Length {
		return nil, fmt.Errorf("%w: field %q=%d does not match payload length %d",
			ErrMalformed, FieldLength, msg.Length, len(msg.Payload))
	}

	digest, ok := index[FieldDigest]
	if !ok {
		return nil, fmt.Errorf("%w: missing field %q", ErrMalformed, FieldDigest)
	}
	digestText, ok := digest.text.(string)
	if digest.kind != kindString || !ok || !isLowerHex(digestText, sha256.Size*2) {
		return nil, fmt.Errorf("%w: field %q must be %d lowercase hex chars",
			ErrMalformed, FieldDigest, sha256.Size*2)
	}
	msg.Digest = digestText

	if computed := Digest(msg); computed != msg.Digest {
		return nil, fmt.Errorf("%w: computed %s, got %s", ErrDigestMismatch, computed, msg.Digest)
	}
	return msg, nil
}

// parseCanonicalUint accepts only canonical decimal integers: an optional
// single "0", otherwise a leading digit 1-9 followed by digits. Signs,
// fractions, exponents and leading zeros are rejected, and the value must fit
// in the requested unsigned width.
func parseCanonicalUint(text string, bits int) (uint64, error) {
	if text == "" {
		return 0, fmt.Errorf("%w: empty number", ErrMalformed)
	}
	if text[0] == '-' || text[0] == '+' {
		return 0, fmt.Errorf("%w: sign not allowed: %q", ErrMalformed, text)
	}
	if len(text) > 1 && text[0] == '0' {
		return 0, fmt.Errorf("%w: leading zero: %q", ErrMalformed, text)
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, fmt.Errorf("%w: non-integer number: %q", ErrMalformed, text)
		}
	}

	var max uint64
	switch bits {
	case 32:
		max = uint64(^uint32(0))
	case 64:
		max = ^uint64(0)
	default:
		return 0, fmt.Errorf("unsupported integer width %d", bits)
	}

	var n uint64
	for i := 0; i < len(text); i++ {
		digit := uint64(text[i] - '0')
		if n > (max-digit)/10 {
			return 0, fmt.Errorf("%w: %q", ErrOutOfRange, text)
		}
		n = n*10 + digit
	}
	return n, nil
}

func isLowerHex(s string, wantLen int) bool {
	if len(s) != wantLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// encode renders the message in the canonical wire form: fixed key order,
// compact separators, canonical decimal integers and base64-normalized
// payload. withDigest toggles the digest field; the core form feeds the digest.
func encode(msg *Message, withDigest bool) []byte {
	var b []byte
	b = append(b, '{')
	b = appendJSONString(b, FieldVersion)
	b = append(b, ':')
	b = strconv.AppendUint(b, uint64(msg.Version), 10)
	b = append(b, ',')
	b = appendJSONString(b, FieldLength)
	b = append(b, ':')
	b = strconv.AppendUint(b, msg.Length, 10)
	b = append(b, ',')
	b = appendJSONString(b, FieldPayload)
	b = append(b, ':')
	b = appendJSONString(b, base64.StdEncoding.EncodeToString(msg.Payload))
	if withDigest {
		b = append(b, ',')
		b = appendJSONString(b, FieldDigest)
		b = append(b, ':')
		b = appendJSONString(b, msg.Digest)
	}
	b = append(b, '}')
	return b
}

// Canonical returns the full canonical encoding of msg, including digest.
func Canonical(msg *Message) []byte {
	return encode(msg, true)
}

// Digest returns the lowercase hex SHA-256 over the canonical core encoding
// (the envelope without its digest field). Both communicating sides compute
// the same bytes regardless of input field order.
func Digest(msg *Message) string {
	sum := sha256.Sum256(encode(msg, false))
	return hex.EncodeToString(sum[:])
}

const hexChars = "0123456789abcdef"

// appendJSONString appends a minimally escaped JSON string. Only the mandated
// escapes and \u00xx for control bytes are emitted, keeping canonical bytes
// independent of any framework-specific escaping such as HTML escaping.
func appendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\b':
			b = append(b, '\\', 'b')
		case '\f':
			b = append(b, '\\', 'f')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hexChars[byte(r)>>4], hexChars[byte(r)&0x0f])
			} else {
				b = append(b, string(r)...)
			}
		}
	}
	return append(b, '"')
}
