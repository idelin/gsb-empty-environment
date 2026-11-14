package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	maxAttributesJSONBytes = 1 << 20
	maxAttributeCount      = 64
)

// ParseAttributesJSON parses a flat JSON object of string attributes and then
// applies the same whitelist as ParseAttributes.
//
// Unlike a plain json.Unmarshal into a map, this function rejects duplicate
// member names. Without that check, an attacker could send the same binding
// key twice and rely on the decoder's last-value-wins behavior to smuggle a
// privileged value past request validation.
func ParseAttributesJSON(data []byte) (Attributes, error) {
	if len(data) == 0 || len(data) > maxAttributesJSONBytes {
		return Attributes{}, fmt.Errorf("%w: invalid JSON attribute size", ErrInvalidAttribute)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil {
		return Attributes{}, fmt.Errorf("%w: invalid JSON: %v", ErrInvalidAttribute, err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return Attributes{}, fmt.Errorf("%w: attributes must be a JSON object", ErrInvalidAttribute)
	}

	raw := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Attributes{}, fmt.Errorf("%w: invalid JSON object key: %v", ErrInvalidAttribute, err)
		}
		key, ok := token.(string)
		if !ok {
			return Attributes{}, fmt.Errorf("%w: JSON attribute name is not a string", ErrInvalidAttribute)
		}
		if _, duplicate := raw[key]; duplicate {
			return Attributes{}, fmt.Errorf("%w: duplicate JSON member %q", ErrDuplicateAttribute, key)
		}
		if len(raw) >= maxAttributeCount {
			return Attributes{}, fmt.Errorf("%w: too many JSON attributes", ErrInvalidAttribute)
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return Attributes{}, fmt.Errorf("%w: attribute %q is not a string", ErrInvalidAttribute, key)
		}
		raw[key] = value
	}

	token, err = decoder.Token()
	if err != nil {
		return Attributes{}, fmt.Errorf("%w: unterminated JSON object: %v", ErrInvalidAttribute, err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return Attributes{}, fmt.Errorf("%w: invalid JSON object terminator", ErrInvalidAttribute)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return Attributes{}, fmt.Errorf("%w: trailing JSON data: %v", ErrInvalidAttribute, err)
		}
		return Attributes{}, fmt.Errorf("%w: trailing JSON value %v", ErrInvalidAttribute, token)
	}
	return ParseAttributes(raw)
}
