package canonkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrTrailingData reports extra bytes after a complete JSON value.
var ErrTrailingData = errors.New("canonkey: trailing data after JSON value")

// ParseParams decodes one JSON value from r under the given budgets.
// Numbers decode as json.Number to preserve their exact representation.
// A top-level JSON null decodes to Null so it stays distinct from an
// absent parameter set.
func ParseParams(r io.Reader, lim Limits) (any, error) {
	lim = lim.withDefaults()
	raw, err := io.ReadAll(io.LimitReader(r, int64(lim.MaxInputBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > lim.MaxInputBytes {
		return nil, fmt.Errorf("%w: input bytes > %d", ErrBudget, lim.MaxInputBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, ErrTrailingData
	}
	if v == nil {
		return Null, nil
	}
	return v, nil
}
