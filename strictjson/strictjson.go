// Package strictjson provides a JSON decoder that rejects object literals
// containing duplicate keys at any nesting depth.
//
// The standard library encoding/json package silently accepts duplicate
// keys and keeps the last occurrence. This package instead fails the whole
// decode with an error wrapping ErrDuplicateKey, so callers can distinguish
// ambiguous inputs from valid ones.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrDuplicateKey is returned (wrapped) when a JSON object contains the same
// key more than once.
var ErrDuplicateKey = errors.New("strictjson: duplicate object key")

// DuplicateKeyError reports the offending key. Call errors.Is(err,
// ErrDuplicateKey) to detect this class of error.
type DuplicateKeyError struct {
	Key string
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("%s: %q", ErrDuplicateKey.Error(), e.Key)
}

func (e *DuplicateKeyError) Unwrap() error {
	return ErrDuplicateKey
}

// Unmarshal parses JSON data into v and fails if any object, at any nesting
// level, repeats a key. Exactly one JSON value is accepted; trailing data is
// an error.
func Unmarshal(data []byte, v any) error {
	if err := validate(bytes.NewReader(data)); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Decoder is an io.Reader-based strict JSON decoder, mirroring the shape of
// json.Decoder.
type Decoder struct {
	r io.Reader
}

// NewDecoder returns a Decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// Decode reads the next JSON-encoded value from its input and stores it in v,
// rejecting duplicate object keys.
func (d *Decoder) Decode(v any) error {
	data, err := io.ReadAll(d.r)
	if err != nil {
		return err
	}
	return Unmarshal(data, v)
}

// validate streams the JSON tokens and verifies that no object repeats a key.
// It also guarantees the input holds exactly one JSON value.
func validate(r io.Reader) error {
	dec := json.NewDecoder(r)
	if err := validateValue(dec); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("strictjson: multiple JSON values")
		}
		return err
	}
	return nil
}

// validateValue consumes one JSON value from dec, tracking object keys.
func validateValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}

	delim, ok := tok.(json.Delim)
	if !ok {
		// null, bool, string, number: a single token, nothing to recurse into.
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("strictjson: expected object key, got %v", keyTok)
			}
			if _, dup := seen[key]; dup {
				return &DuplicateKeyError{Key: key}
			}
			seen[key] = struct{}{}
			if err := validateValue(dec); err != nil {
				return err
			}
		}
		// Consume the closing '}'.
		if _, err := dec.Token(); err != nil {
			return err
		}
	case '[':
		for dec.More() {
			if err := validateValue(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("strictjson: unexpected delimiter %q", delim)
	}
	return nil
}
