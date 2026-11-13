package netsec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Header is a decoded JWS header. Algorithm and KeyID are populated only from
// the protected header. For an unprotected header they are informative and are
// never used for verification.
type Header struct {
	Algorithm string                     `json:"alg,omitempty"`
	KeyID     string                     `json:"kid,omitempty"`
	Raw       map[string]json.RawMessage `json:"-"`
}

// Alg returns the protected alg value, or the informational unprotected value
// for a Header obtained from an unprotected header.
func (h Header) Alg() string {
	return h.Algorithm
}

// Kid returns the protected kid value, or the informational unprotected value
// for a Header obtained from an unprotected header.
func (h Header) Kid() string {
	return h.KeyID
}

// Has reports whether the header contains the named parameter.
func (h Header) Has(name string) bool {
	_, ok := h.Raw[name]
	return ok
}

// Value returns the raw JSON value of the named header parameter.
func (h Header) Value(name string) (json.RawMessage, bool) {
	raw, ok := h.Raw[name]
	return raw, ok
}

func emptyHeader() Header {
	return Header{Raw: map[string]json.RawMessage{}}
}

func parseHeaderSegment(segment string) (Header, error) {
	data, err := decodeBase64URL(segment)
	if err != nil {
		return Header{}, err
	}
	return parseHeaderJSON(data)
}

func parseHeaderJSON(data []byte) (Header, error) {
	obj, err := decodeJSONObject(data)
	if err != nil {
		return Header{}, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}
	return parseHeaderObject(obj)
}

func parseHeaderObject(obj map[string]json.RawMessage) (Header, error) {
	h := Header{Raw: obj}

	if raw, ok := obj["alg"]; ok {
		alg, err := decodeJSONString(raw, "alg")
		if err != nil {
			return Header{}, err
		}
		h.Algorithm = alg
	}

	if raw, ok := obj["kid"]; ok {
		kid, err := decodeJSONString(raw, "kid")
		if err != nil {
			return Header{}, err
		}
		h.KeyID = kid
	}

	if raw, ok := obj["crit"]; ok {
		var crit []string
		if isJSONNull(raw) || json.Unmarshal(raw, &crit) != nil {
			return Header{}, fmt.Errorf("%w: crit must be an array of strings", ErrMalformedToken)
		}
	}

	if raw, ok := obj["b64"]; ok {
		var encoded bool
		if isJSONNull(raw) || json.Unmarshal(raw, &encoded) != nil {
			return Header{}, fmt.Errorf("%w: b64 must be a boolean", ErrMalformedToken)
		}
	}

	return h, nil
}

func decodeJSONString(raw json.RawMessage, name string) (string, error) {
	if isJSONNull(raw) {
		return "", fmt.Errorf("%w: %s must be a string", ErrMalformedToken, name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: %s must be a string", ErrMalformedToken, name)
	}
	return value, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func ensureProtectedExtensions(h Header) error {
	if raw, ok := h.Raw["crit"]; ok {
		var crit []string
		if json.Unmarshal(raw, &crit) != nil {
			return fmt.Errorf("%w: crit must be an array of strings", ErrMalformedToken)
		}
		if len(crit) != 0 {
			return fmt.Errorf("%w: crit=%v", ErrUnsupportedCritical, crit)
		}
	}

	if raw, ok := h.Raw["b64"]; ok {
		var encoded bool
		if json.Unmarshal(raw, &encoded) != nil {
			return fmt.Errorf("%w: b64 must be a boolean", ErrMalformedToken)
		}
		if !encoded {
			return fmt.Errorf("%w: unencoded payloads are not supported", ErrUnsupportedCritical)
		}
	}

	return nil
}

func checkHeaderConsistency(protected, unprotected Header) error {
	for name, protectedValue := range protected.Raw {
		unprotectedValue, ok := unprotected.Raw[name]
		if !ok {
			continue
		}
		equal, err := jsonValuesEqual(protectedValue, unprotectedValue)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformedToken, err)
		}
		if !equal {
			return fmt.Errorf("%w: %q", ErrUnprotectedHeaderConflict, name)
		}
	}
	return nil
}

func jsonValuesEqual(a, b json.RawMessage) (bool, error) {
	if bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) {
		return true, nil
	}
	av, err := decodeJSONValue(a)
	if err != nil {
		return false, err
	}
	bv, err := decodeJSONValue(b)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(av, bv), nil
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// decodeJSONObject decodes a JSON object while rejecting duplicate member
// names at every nesting level. Duplicate parameters are ambiguous and can
// produce parser differentials between the signer and the verifier.
func decodeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]json.RawMessage{}, nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("header must be a JSON object")
	}

	obj := make(map[string]json.RawMessage)
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("object member name must be a string")
		}
		if _, exists := obj[key]; exists {
			return nil, fmt.Errorf("duplicate JSON member %q", key)
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if err := checkNoDuplicateKeys(raw); err != nil {
			return nil, err
		}
		obj[key] = raw
	}

	tok, err = dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '}' {
		return nil, fmt.Errorf("header is not a closed JSON object")
	}

	if tok, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON token %v", tok)
		}
		return nil, err
	}

	return obj, nil
}

func checkNoDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := scanJSONValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func scanJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}

	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object member name must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON member %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := tok.(json.Delim); !ok || delim != '}' {
			return fmt.Errorf("object is not closed")
		}
	case '[':
		for dec.More() {
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := tok.(json.Delim); !ok || delim != ']' {
			return fmt.Errorf("array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}

	return nil
}

func decodeBase64URL(segment string) ([]byte, error) {
	if strings.ContainsAny(segment, " \t\r\n") {
		return nil, fmt.Errorf("%w: invalid base64url segment", ErrMalformedToken)
	}
	if segment == "" {
		return []byte{}, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(segment); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(segment); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("%w: invalid base64url segment", ErrMalformedToken)
}

func encodeBase64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
