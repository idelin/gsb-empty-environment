package gdict

import (
	"fmt"
	"sort"
	"strings"
)

// Kind distinguishes the two key values that have no ordinary text
// representation in the input dictionaries.
type Kind uint8

const (
	// KindText is an ordinary string key. The empty string is a valid text
	// key and is deliberately distinct from KindNull.
	KindText Kind = iota
	// KindNull is the SQL NULL key. Its position in every collation must be
	// stated by the corresponding Preorder.
	KindNull
)

// Key is a dictionary key. Keys are comparable and can therefore be used as
// map keys.
type Key struct {
	Kind Kind   `json:"kind"`
	Text string `json:"text,omitempty"`
}

// Null returns the NULL key.
func Null() Key { return Key{Kind: KindNull} }

// Text returns a text key. Text("") is the empty string, not NULL.
func Text(s string) Key { return Key{Kind: KindText, Text: s} }

func (k Key) String() string {
	if k.Kind == KindNull {
		return "NULL"
	}
	return fmt.Sprintf("%q", k.Text)
}

// Less is a total, deterministic order used only for presentation and for
// choosing canonical representatives. It is not a collation.
func (k Key) Less(other Key) bool {
	if k.Kind != other.Kind {
		return k.Kind < other.Kind
	}
	return k.Text < other.Text
}

// sortKeys sorts a slice in the deterministic presentation order.
func sortKeys(keys []Key) {
	sort.Slice(keys, func(i, j int) bool { return keys[i].Less(keys[j]) })
}

func keySliceString(keys []Key) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return "[" + strings.Join(parts, ",") + "]"
}
