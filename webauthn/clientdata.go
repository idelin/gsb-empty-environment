package webauthn

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

const expectedClientDataType = "webauthn.create"

// clientDataJSON subset defined by W3C WebAuthn section 5.8.1.
type clientData struct {
	Type         string          `json:"type"`
	Challenge    string          `json:"challenge"`
	Origin       string          `json:"origin"`
	CrossOrigin  bool            `json:"crossOrigin"`
	TokenBinding json.RawMessage `json:"tokenBinding,omitempty"`
	TopOrigin    json.RawMessage `json:"topOrigin,omitempty"`
	Raw          []byte
}

// parseClientData decodes clientDataJSON while rejecting duplicate keys and
// enforcing the exact registration contract:
//
//   - type must be "webauthn.create";
//   - challenge must equal the base64url encoding of the issued challenge;
//   - origin must be byte-for-byte equal to the expected origin;
//   - crossOrigin, when present, must not be true.
func parseClientData(raw, expectedChallenge []byte, expectedOrigin string) (*clientData, error) {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrClientData, err)
	}

	var cd clientData
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&cd); err != nil {
		return nil, fmt.Errorf("%w: clientDataJSON is not valid JSON: %v", ErrClientData, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: clientDataJSON contains multiple JSON values", ErrClientData)
	}
	cd.Raw = append([]byte(nil), raw...)

	if cd.Type != expectedClientDataType {
		return nil, fmt.Errorf("%w: type %q is not %q", ErrClientData, cd.Type, expectedClientDataType)
	}

	decodedChallenge, err := decodeBase64URL(cd.Challenge)
	if err != nil {
		return nil, fmt.Errorf("%w: challenge is not valid base64url: %v", ErrClientData, err)
	}
	if !bytes.Equal(decodedChallenge, expectedChallenge) {
		return nil, fmt.Errorf("%w: challenge does not match the issued challenge", ErrChallenge)
	}

	if cd.Origin != expectedOrigin {
		return nil, fmt.Errorf("%w: origin %q does not match expected %q", ErrOrigin, cd.Origin, expectedOrigin)
	}

	// Absent crossOrigin defaults to false per spec; an explicit true is a
	// cross-origin registration and must be rejected.
	if cd.CrossOrigin {
		return nil, fmt.Errorf("%w: cross-origin registration (crossOrigin=true) is forbidden", ErrClientData)
	}
	return &cd, nil
}

// decodeBase64URL accepts both unpadded and padded base64url as produced by
// browsers, but never standard base64 with '+' or '/'.
func decodeBase64URL(value string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

// rejectDuplicateJSONKeys walks the raw JSON and fails when an object contains
// the same member name more than once, preventing parser-differential attacks.
func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		if err == io.EOF {
			return fmt.Errorf("%w: empty clientDataJSON", ErrMalformed)
		}
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("%w: clientDataJSON must be a JSON object", ErrClientData)
	}
	return scanJSONObject(decoder)
}

// scanJSONObject assumes the opening '{' has already been consumed.
func scanJSONObject(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("%w: JSON object key is not a string", ErrMalformed)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate JSON key %q", ErrMalformed, key)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return fmt.Errorf("%w: malformed JSON object", ErrMalformed)
	}
	return nil
}

func scanJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim(']') {
		return fmt.Errorf("%w: malformed JSON array", ErrMalformed)
	}
	return nil
}

// scanJSONValue consumes exactly one JSON value, recursing into composites.
func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil // scalar value
	}
	switch delim {
	case '{':
		return scanJSONObject(decoder)
	case '[':
		return scanJSONArray(decoder)
	default:
		return fmt.Errorf("%w: unexpected JSON delimiter %q", ErrMalformed, delim)
	}
}
