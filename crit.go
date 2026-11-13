package netsec

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors returned by the crit validators. Use errors.Is to match
// them after unwrapping the named-extension errors.
var (
	// ErrCritInUnprotected means the "crit" header parameter was present in
	// the unprotected JOSE header. RFC 7515 only permits it in the protected
	// header.
	ErrCritInUnprotected = errors.New("netsec: crit header parameter must not appear in the unprotected header")

	// ErrCritNotArray means the protected "crit" value is not a JSON array.
	ErrCritNotArray = errors.New("netsec: crit header parameter must be a JSON array of strings")

	// ErrCritEmpty means the protected "crit" array contains no entries.
	ErrCritEmpty = errors.New("netsec: crit header parameter must not be an empty array")

	// ErrCritEntryNotString means one of the "crit" array entries is not a
	// JSON string.
	ErrCritEntryNotString = errors.New("netsec: every crit array entry must be a string")

	// ErrCritExtensionMissing means a name listed in "crit" is absent from
	// the protected header.
	ErrCritExtensionMissing = errors.New("netsec: crit extension is missing from the protected header")

	// ErrCritExtensionUnprotected means a name listed in "crit" is carried
	// only in the unprotected header instead of the protected header.
	ErrCritExtensionUnprotected = errors.New("netsec: crit extension must be in the protected header, not the unprotected header")

	// ErrCritExtensionUnknown means a name listed in "crit" is not
	// understood by this implementation (absent from knownExtensions).
	ErrCritExtensionUnknown = errors.New("netsec: crit extension is not understood by this implementation")
)

// ValidateHeaders validates the JWS "crit" (critical) header parameter against
// the rules of RFC 7515, section 4.1.11.
//
// protectedHeader is the decoded JSON object of the JWS protected header and
// unprotectedHeader is the decoded JSON object of the JWS unprotected header
// (pass nil for compact serialization, which has no unprotected header).
//
// knownExtensions is the set of extension header parameter names this resource
// server understands. Every name listed in "crit" must:
//
//   - be understood by this implementation (present in knownExtensions), and
//   - be present in the protected header.
//
// A JWS without a "crit" parameter is accepted. The function only performs
// crit validation; signature verification is the caller's responsibility.
func ValidateHeaders(protectedHeader, unprotectedHeader map[string]any, knownExtensions map[string]bool) error {
	if _, exists := unprotectedHeader["crit"]; exists {
		return ErrCritInUnprotected
	}

	rawCrit, present := protectedHeader["crit"]
	if !present {
		return nil
	}

	entries, ok := rawCrit.([]any)
	if !ok {
		return ErrCritNotArray
	}
	if len(entries) == 0 {
		return ErrCritEmpty
	}

	for _, entry := range entries {
		name, ok := entry.(string)
		if !ok {
			return ErrCritEntryNotString
		}

		if _, inProtected := protectedHeader[name]; !inProtected {
			if _, inUnprotected := unprotectedHeader[name]; inUnprotected {
				return fmt.Errorf("%w: %q", ErrCritExtensionUnprotected, name)
			}
			return fmt.Errorf("%w: %q", ErrCritExtensionMissing, name)
		}
		if !knownExtensions[name] {
			return fmt.Errorf("%w: %q", ErrCritExtensionUnknown, name)
		}
	}

	return nil
}

// ValidateCompact validates the "crit" parameter of a JWS compact
// serialization (the three dot-separated base64url parts of RFC 7515
// section 7.1). Compact serialization carries no unprotected header, so a
// "crit" placed anywhere other than the encoded protected header cannot be
// expressed here.
func ValidateCompact(compactJWS string, knownExtensions map[string]bool) error {
	parts := strings.Split(compactJWS, ".")
	if len(parts) != 3 {
		return fmt.Errorf("netsec: JWS compact serialization must have 3 parts, got %d", len(parts))
	}

	protectedHeader, err := decodeHeader(parts[0])
	if err != nil {
		return err
	}

	return ValidateHeaders(protectedHeader, nil, knownExtensions)
}

func decodeHeader(encoded string) (map[string]any, error) {
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		// Tolerate producers that incorrectly apply padding while still
		// requiring the URL-safe alphabet.
		if padded, padErr := base64.URLEncoding.DecodeString(encoded); padErr == nil {
			payload = padded
		} else {
			return nil, fmt.Errorf("netsec: protected header is not valid base64url: %w", err)
		}
	}

	header := make(map[string]any)
	if err := json.Unmarshal(payload, &header); err != nil {
		return nil, fmt.Errorf("netsec: protected header is not valid JSON: %w", err)
	}
	return header, nil
}
