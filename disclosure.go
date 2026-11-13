package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// DisclosureKind identifies the shape of a disclosure.
type DisclosureKind string

const (
	// ObjectDisclosure is a [salt, claim name, claim value] disclosure.
	ObjectDisclosure DisclosureKind = "object"

	// ArrayDisclosure is a [salt, value] disclosure for an array element.
	ArrayDisclosure DisclosureKind = "array"
)

// Disclosure is a parsed SD-JWT disclosure. Raw is the exact base64url string
// whose UTF-8 bytes are hashed; it is deliberately not re-serialized.
type Disclosure struct {
	Raw    string
	Salt   string
	Name   string
	Value  any
	Digest string
	Kind   DisclosureKind
}

// ParseDisclosure decodes and validates one SD-JWT disclosure. Both padded
// and unpadded base64url encodings are accepted.
func ParseDisclosure(raw string) (*Disclosure, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty disclosure", ErrMalformedDisclosure)
	}

	decoded, err := decodeBase64URL(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64url: %v", ErrMalformedDisclosure, err)
	}

	var parts []json.RawMessage
	if err := json.Unmarshal(decoded, &parts); err != nil {
		return nil, fmt.Errorf("%w: disclosure is not a JSON array: %v", ErrMalformedDisclosure, err)
	}
	if len(parts) != 2 && len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 2 or 3 elements, got %d", ErrMalformedDisclosure, len(parts))
	}

	var saltValue any
	if err := json.Unmarshal(parts[0], &saltValue); err != nil {
		return nil, fmt.Errorf("%w: salt must be a string", ErrMalformedDisclosure)
	}
	salt, ok := saltValue.(string)
	if !ok {
		return nil, fmt.Errorf("%w: salt must be a string", ErrMalformedDisclosure)
	}

	d := &Disclosure{
		Raw:    raw,
		Salt:   salt,
		Digest: DisclosureDigest(raw),
	}

	if len(parts) == 3 {
		var nameValue any
		if err := json.Unmarshal(parts[1], &nameValue); err != nil {
			return nil, fmt.Errorf("%w: claim name must be a string", ErrMalformedDisclosure)
		}
		name, ok := nameValue.(string)
		if !ok {
			return nil, fmt.Errorf("%w: claim name must be a string", ErrMalformedDisclosure)
		}
		d.Name = name
		if d.Name == "" {
			return nil, fmt.Errorf("%w: claim name is empty", ErrMalformedDisclosure)
		}
		if isReservedClaim(d.Name) {
			return nil, fmt.Errorf("%w: %q", ErrReservedClaim, d.Name)
		}
		d.Kind = ObjectDisclosure
		if err := json.Unmarshal(parts[2], &d.Value); err != nil {
			return nil, fmt.Errorf("%w: invalid claim value: %v", ErrMalformedDisclosure, err)
		}
	} else {
		d.Kind = ArrayDisclosure
		if err := json.Unmarshal(parts[1], &d.Value); err != nil {
			return nil, fmt.Errorf("%w: invalid array value: %v", ErrMalformedDisclosure, err)
		}
	}

	return d, nil
}

// DisclosureDigest returns the base64url encoded SHA-256 digest of the exact
// disclosure string as defined by SD-JWT.
func DisclosureDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// CreateDisclosure builds an object-property disclosure for claim name and
// value.
func CreateDisclosure(salt, name string, value any) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: claim name is empty", ErrMalformedDisclosure)
	}
	if isReservedClaim(name) {
		return "", fmt.Errorf("%w: %q", ErrReservedClaim, name)
	}
	return encodeDisclosure([]any{salt, name, value})
}

// CreateArrayDisclosure builds an array-element disclosure.
func CreateArrayDisclosure(salt string, value any) (string, error) {
	return encodeDisclosure([]any{salt, value})
}

func encodeDisclosure(parts []any) (string, error) {
	data, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("%w: cannot encode disclosure: %v", ErrMalformedDisclosure, err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeBase64URL(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func isReservedClaim(name string) bool {
	return name == "_sd" || name == "_sd_alg" || name == "..."
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}
