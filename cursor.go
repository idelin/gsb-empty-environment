package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// cursor is an opaque pagination token bound to a graph version.
type cursor struct {
	Version string `json:"v"`
	Offset  int    `json:"o"`
}

func encodeCursor(version string, offset int) string {
	raw, err := json.Marshal(cursor{Version: version, Offset: offset})
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (cursor, error) {
	var c cursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, fmt.Errorf("invalid cursor encoding")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("invalid cursor payload")
	}
	if c.Offset < 0 {
		return c, fmt.Errorf("invalid cursor offset %d", c.Offset)
	}
	return c, nil
}
