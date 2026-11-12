package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// decodeJSONObject decodes exactly one JSON object and rejects duplicate
// member names at every nesting level. Duplicate member names are especially
// dangerous for security-sensitive fields such as alg, client_id, iss, and aud.
func decodeJSONObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder)
	if err != nil {
		if errors.Is(err, ErrDuplicateJSONKey) {
			return nil, err
		}
		return nil, errorf(ErrInvalidRequestObject, err.Error())
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errorf(ErrInvalidRequestObject, "trailing JSON data")
		}
		return nil, errorf(ErrInvalidRequestObject, err.Error())
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errorf(ErrInvalidRequestObject, "top-level JSON value is not an object")
	}
	return object, nil
}

func decodeJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object member name is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, errorf(ErrDuplicateJSONKey, key)
			}
			value, err := decodeJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case json.Delim('['):
		var array []any
		for decoder.More() {
			value, err := decodeJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return token, nil
	}
}
