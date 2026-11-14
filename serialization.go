package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// JSONPContentType is the only content type emitted for a successful JSONP
// response. Keeping it fixed prevents content-type confusion and script
// execution through attacker-selected media types.
const JSONPContentType = "application/javascript; charset=utf-8"

// MarshalJSONP validates callback and returns callback(json); as script bytes.
// The JSON is HTML-escaped (including U+2028 and U+2029) so it cannot close a
// surrounding HTML script element or terminate the JavaScript statement.
func MarshalJSONP(callback string, data any, maxLen ...int) ([]byte, error) {
	name, err := NormalizeCallback(callback, maxLen...)
	if err != nil {
		return nil, err
	}
	return marshalJSONPName(name, data)
}

// WrapJSONP is an alias for MarshalJSONP.
func WrapJSONP(callback string, data any, maxLen ...int) ([]byte, error) {
	return MarshalJSONP(callback, data, maxLen...)
}

// JSONPString returns the validated script wrapper as a string.
func JSONPString(callback string, data any, maxLen ...int) (string, error) {
	body, err := MarshalJSONP(callback, data, maxLen...)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func marshalJSONPName(name string, data any) ([]byte, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("netsec: marshal JSONP payload: %w", err)
	}
	payload = escapeJSONForScript(payload)

	body := make([]byte, 0, len(name)+len(payload)+3)
	body = append(body, name...)
	body = append(body, '(')
	body = append(body, payload...)
	body = append(body, ')', ';')
	return body, nil
}

var errInvalidMarshaledJSON = errors.New("netsec: invalid marshaled JSON")

func marshalWith(marshal func(any) ([]byte, error), data any) ([]byte, error) {
	if marshal == nil {
		return json.Marshal(data)
	}
	payload, err := marshal(data)
	if err != nil {
		return nil, err
	}
	if !json.Valid(payload) {
		return nil, errInvalidMarshaledJSON
	}
	return payload, nil
}

// escapeJSONForScript ensures that even custom json.Marshaler output cannot
// introduce raw HTML-significant characters or JavaScript line separators.
func escapeJSONForScript(payload []byte) []byte {
	if !bytes.ContainsAny(payload, "<>&\u2028\u2029") {
		return payload
	}

	var out bytes.Buffer
	out.Grow(len(payload) + 16)
	for i := 0; i < len(payload); {
		switch payload[i] {
		case '<':
			out.WriteString(`\u003c`)
			i++
		case '>':
			out.WriteString(`\u003e`)
			i++
		case '&':
			out.WriteString(`\u0026`)
			i++
		case 0xe2:
			if i+2 < len(payload) && payload[i+1] == 0x80 &&
				(payload[i+2] == 0xa8 || payload[i+2] == 0xa9) {
				if payload[i+2] == 0xa8 {
					out.WriteString(`\u2028`)
				} else {
					out.WriteString(`\u2029`)
				}
				i += 3
				continue
			}
			out.WriteByte(payload[i])
			i++
		default:
			out.WriteByte(payload[i])
			i++
		}
	}
	return out.Bytes()
}
