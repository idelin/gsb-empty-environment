package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// A cursor is an opaque, graph-version-bound pagination token. It records
// the last emitted cut; enumeration resumes strictly after it in
// lexicographic order.
const cursorPrefix = "mce1."

type cursorData struct {
	V    int    `json:"v"`
	GV   string `json:"gv"`
	Last []int  `json:"last"`
}

func encodeCursor(graphVersion string, last []int) string {
	payload, _ := json.Marshal(cursorData{V: 1, GV: graphVersion, Last: last})
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(s string) (*cursorData, error) {
	if !strings.HasPrefix(s, cursorPrefix) {
		return nil, fmt.Errorf("malformed cursor: missing %q prefix", cursorPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, cursorPrefix))
	if err != nil {
		return nil, fmt.Errorf("malformed cursor: %v", err)
	}
	var c cursorData
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("malformed cursor: %v", err)
	}
	if c.V != 1 {
		return nil, fmt.Errorf("unsupported cursor version %d", c.V)
	}
	if len(c.Last) == 0 {
		return nil, fmt.Errorf("cursor carries an empty cut")
	}
	return &c, nil
}
