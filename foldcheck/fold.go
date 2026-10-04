package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// FoldTable is an explicitly provided case-folding table. Only mappings
// present in the table are applied; everything else is left untouched.
// Keys may be multi-rune and values may map one input to many output
// characters (e.g. "ß" -> "ss").
type FoldTable struct {
	mapping map[string]string
	keys    []string // sorted longest-first for greedy matching
}

func LoadTable(r io.Reader) (*FoldTable, error) {
	var raw map[string]string
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse fold table: %w", err)
	}
	t := &FoldTable{mapping: make(map[string]string, len(raw))}
	for k, v := range raw {
		if k == "" {
			return nil, fmt.Errorf("fold table contains an empty key")
		}
		t.mapping[k] = v
		t.keys = append(t.keys, k)
	}
	sort.Slice(t.keys, func(i, j int) bool {
		if len(t.keys[i]) != len(t.keys[j]) {
			return len(t.keys[i]) > len(t.keys[j])
		}
		return t.keys[i] < t.keys[j]
	})
	return t, nil
}

// Fold applies the table with greedy longest-match semantics.
func (t *FoldTable) Fold(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		matched := false
		for _, k := range t.keys {
			if strings.HasPrefix(s[i:], k) {
				b.WriteString(t.mapping[k])
				i += len(k)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}
