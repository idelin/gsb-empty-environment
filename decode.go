package canonicalkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

func DecodeJSON(data []byte, limits Limits) (Value, error) {
	limits = limits.withDefaults()
	if len(data) > limits.MaxInputBytes {
		return nil, ErrBudgetExceeded
	}
	if !utf8.Valid(data) {
		return nil, ErrInvalidUnicode
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder, 0, limits.MaxDepth)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing JSON data", ErrInvalidRequest)
		}
		return nil, err
	}
	return value, nil
}

func decodeJSONValue(decoder *json.Decoder, depth, maxDepth int) (Value, error) {
	if depth > maxDepth {
		return nil, ErrDepthExceeded
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '[':
			array := Array{}
			for decoder.More() {
				child, err := decodeJSONValue(decoder, depth+1, maxDepth)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return array, nil
		case '{':
			object := Object{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("%w: object key is not a string", ErrInvalidRequest)
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("%w: %q", ErrDuplicateField, key)
				}
				child, err := decodeJSONValue(decoder, depth+1, maxDepth)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		default:
			return nil, fmt.Errorf("%w: unexpected delimiter %q", ErrInvalidRequest, value)
		}
	case nil:
		return NullValue{}, nil
	case bool:
		return value, nil
	case string:
		return value, nil
	case json.Number:
		return Number(value), nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrInvalidValue, token)
	}
}

func DecodeRequest(data []byte, limits Limits) (Request, error) {
	value, err := DecodeJSON(data, limits)
	if err != nil {
		return Request{}, err
	}
	object, ok := value.(Object)
	if !ok {
		return Request{}, fmt.Errorf("%w: request must be a JSON object", ErrInvalidRequest)
	}

	request := Request{Params: AbsentParams()}
	for key, raw := range object {
		switch key {
		case "tenant":
			value, ok := raw.(string)
			if !ok {
				return Request{}, fmt.Errorf("%w: tenant must be a string", ErrInvalidFieldType)
			}
			request.Tenant = value
		case "parameter_type":
			value, ok := raw.(string)
			if !ok {
				return Request{}, fmt.Errorf("%w: parameter_type must be a string", ErrInvalidFieldType)
			}
			request.ParameterType = value
		case "config_version":
			switch value := raw.(type) {
			case string:
				request.ConfigVersion = PresentString(value)
			case NullValue:
				request.ConfigVersion = NullString()
			default:
				return Request{}, fmt.Errorf("%w: config_version must be a string or null", ErrInvalidFieldType)
			}
		case "params":
			if _, isNull := raw.(NullValue); isNull {
				request.Params = NullParams()
			} else {
				request.Params = PresentParams(raw)
			}
		default:
			return Request{}, fmt.Errorf("%w: %q", ErrUnknownField, key)
		}
	}
	return request, nil
}
