package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// parseStd is the second, fully independent parsing path built solely on the
// standard library's encoding/json token stream. It keeps all top-level key
// occurrences instead of decoding into a struct or map, because those would
// silently drop duplicate keys; UseNumber preserves the source number text so
// the shared canonical-number validation decides validity.
func parseStd(raw []byte) ([]rawField, error) {
	// encoding/json silently replaces invalid UTF-8 with U+FFFD; reject it
	// up front so this path cannot diverge from the hand-written parser.
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("input is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	root, err := parseStdValue(dec)
	if err != nil {
		return nil, err
	}
	// A single value must occupy the whole stream; any further token or
	// syntactically invalid trailing byte is rejected.
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data")
	}
	if root.kind != kindObject {
		return nil, fmt.Errorf("top-level value must be a JSON object")
	}
	fields, ok := root.text.([]rawField)
	if !ok {
		return nil, fmt.Errorf("internal: corrupted object value")
	}
	return fields, nil
}
func parseStdValue(dec *json.Decoder) (rawValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return rawValue{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseStdObject(dec)
		case '[':
			return parseStdArray(dec)
		default:
			return rawValue{}, fmt.Errorf("unexpected closing delimiter %q", t)
		}
	case string:
		return rawValue{kind: kindString, text: t}, nil
	case json.Number:
		return rawValue{kind: kindInteger, text: t.String()}, nil
	case bool:
		if t {
			return rawValue{kind: kindBool, text: "true"}, nil
		}
		return rawValue{kind: kindBool, text: "false"}, nil
	case nil:
		return rawValue{kind: kindNull}, nil
	default:
		return rawValue{}, fmt.Errorf("unsupported token type %T", tok)
	}
}

func parseStdObject(dec *json.Decoder) (rawValue, error) {
	var fields []rawField
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return rawValue{}, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return rawValue{}, fmt.Errorf("object key is not a string")
		}
		val, err := parseStdValue(dec)
		if err != nil {
			return rawValue{}, err
		}
		fields = append(fields, rawField{name: key, val: val})
	}
	// Consume the closing '}'.
	if _, err := dec.Token(); err != nil {
		return rawValue{}, err
	}
	return rawValue{kind: kindObject, text: fields}, nil
}

func parseStdArray(dec *json.Decoder) (rawValue, error) {
	count := 0
	for dec.More() {
		if _, err := parseStdValue(dec); err != nil {
			return rawValue{}, err
		}
		count++
	}
	if _, err := dec.Token(); err != nil {
		return rawValue{}, err
	}
	return rawValue{kind: kindArray, text: count}, nil
}
