package snapshotpage

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Cursor validation errors. Use errors.Is to match them.
var (
	// ErrCursorMalformed means the token is not a decodable cursor at all.
	ErrCursorMalformed = errors.New("snapshotpage: malformed cursor")
	// ErrSnapshotMismatch means the cursor was issued for a different
	// snapshot (e.g. the store was reset).
	ErrSnapshotMismatch = errors.New("snapshotpage: cursor belongs to a different snapshot")
	// ErrSortMismatch means the cursor was created under a different sort
	// specification than the one requested.
	ErrSortMismatch = errors.New("snapshotpage: cursor sort order does not match the request")
)

// cursorPayload is the serialized form of a cursor. It binds the boundary
// key to a snapshot ID and a sort-spec hash; it never stores row positions.
type cursorPayload struct {
	Snapshot string       `json:"s"`
	SortHash string       `json:"h"`
	Values   []typedValue `json:"k"`
	ID       string       `json:"i"`
}

// typedValue keeps the exact type of a key value across JSON round-trips
// (JSON alone would collapse all numbers to float64).
type typedValue struct {
	T string `json:"t"` // "n" null, "s" string, "i" int64, "f" float64, "b" bool
	V string `json:"v"`
}

func encodeTyped(v any) (typedValue, error) {
	switch tv := v.(type) {
	case nil:
		return typedValue{T: "n"}, nil
	case string:
		return typedValue{T: "s", V: tv}, nil
	case int64:
		return typedValue{T: "i", V: strconv.FormatInt(tv, 10)}, nil
	case float64:
		return typedValue{T: "f", V: strconv.FormatFloat(tv, 'g', -1, 64)}, nil
	case bool:
		return typedValue{T: "b", V: strconv.FormatBool(tv)}, nil
	default:
		return typedValue{}, fmt.Errorf("snapshotpage: unsupported cursor value type %T", v)
	}
}

func decodeTyped(tv typedValue) (any, error) {
	switch tv.T {
	case "n":
		return nil, nil
	case "s":
		return tv.V, nil
	case "i":
		n, err := strconv.ParseInt(tv.V, 10, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case "f":
		f, err := strconv.ParseFloat(tv.V, 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	case "b":
		b, err := strconv.ParseBool(tv.V)
		if err != nil {
			return nil, err
		}
		return b, nil
	default:
		return nil, fmt.Errorf("snapshotpage: unknown cursor value tag %q", tv.T)
	}
}

// encodeCursor serializes a boundary key into an opaque token.
func encodeCursor(snapshotID, sortHash string, k key) (string, error) {
	p := cursorPayload{
		Snapshot: snapshotID,
		SortHash: sortHash,
		ID:       k.ID,
		Values:   make([]typedValue, len(k.Values)),
	}
	for i, v := range k.Values {
		tv, err := encodeTyped(v)
		if err != nil {
			return "", err
		}
		p.Values[i] = tv
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor parses a token and validates it against the snapshot and sort
// spec of the request. It returns a sentinel-wrapped error on any mismatch.
func decodeCursor(token, snapshotID string, spec SortSpec) (key, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return key{}, fmt.Errorf("%w: %v", ErrCursorMalformed, err)
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return key{}, fmt.Errorf("%w: %v", ErrCursorMalformed, err)
	}
	if p.Snapshot == "" || p.SortHash == "" || p.ID == "" || p.Values == nil {
		return key{}, fmt.Errorf("%w: incomplete payload", ErrCursorMalformed)
	}
	if p.Snapshot != snapshotID {
		return key{}, fmt.Errorf("%w: cursor snapshot %q, current %q", ErrSnapshotMismatch, p.Snapshot, snapshotID)
	}
	if p.SortHash != spec.hash() {
		return key{}, fmt.Errorf("%w", ErrSortMismatch)
	}
	if len(p.Values) != len(spec) {
		return key{}, fmt.Errorf("%w: key has %d values, sort spec has %d fields", ErrCursorMalformed, len(p.Values), len(spec))
	}
	k := key{ID: p.ID, Values: make([]any, len(p.Values))}
	for i, tv := range p.Values {
		v, err := decodeTyped(tv)
		if err != nil {
			return key{}, fmt.Errorf("%w: %v", ErrCursorMalformed, err)
		}
		k.Values[i] = v
	}
	return k, nil
}
