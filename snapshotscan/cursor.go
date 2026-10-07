package snapshotscan

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// ErrInvalidCursor is returned for malformed cursor tokens.
var ErrInvalidCursor = errors.New("snapshotscan: invalid cursor")

// Cursor marks a resumable scan position.
//
// It pins the DATA snapshot (Snapshot) and records the last SCANNED
// key — deliberately not the last delivered key. Permission filtering
// may skip scanned items, so a delivered-key cursor would misplace the
// scan position; a scanned-key cursor gives every snapshot item exactly
// one delivery opportunity, in key order.
type Cursor struct {
	Snapshot    uint64 `json:"s"`
	LastScanned string `json:"k"`
}

// EncodeCursor serializes a cursor into an opaque token.
func EncodeCursor(c Cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses a token produced by EncodeCursor.
func DecodeCursor(token string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return c, nil
}
