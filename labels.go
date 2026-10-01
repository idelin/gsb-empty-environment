package main

import (
	"sort"
	"strings"
)

// LabelSet is an unordered set of label name/value pairs.
// Equality is decided by content, never by textual representation.
type LabelSet map[string]string

// Key returns a canonical, order-independent encoding of the label set.
func (ls LabelSet) Key() string {
	names := make([]string, 0, len(ls))
	for name := range ls {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(0)
		b.WriteString(ls[name])
		b.WriteByte(1)
	}
	return b.String()
}

// Clone returns a deep copy.
func (ls LabelSet) Clone() LabelSet {
	out := make(LabelSet, len(ls))
	for k, v := range ls {
		out[k] = v
	}
	return out
}

// Equal reports content equality between two label sets.
func (ls LabelSet) Equal(other LabelSet) bool {
	if len(ls) != len(other) {
		return false
	}
	for k, v := range ls {
		if ov, ok := other[k]; !ok || ov != v {
			return false
		}
	}
	return true
}

func (ls LabelSet) String() string {
	names := make([]string, 0, len(ls))
	for name := range ls {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+ls[name])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// Series is one input time series.
type Series struct {
	Name   string
	Labels LabelSet
	Value  float64
}

// Key uniquely identifies an input series by name + labels.
func (s Series) Key() string {
	return s.Name + "\x00" + s.Labels.Key()
}

// PrettySeriesKey renders a canonical series key in human-readable form.
func PrettySeriesKey(key string) string {
	idx := -1
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return key
	}
	name, rest := key[:idx], key[idx+1:]
	var pairs []string
	for _, tok := range strings.Split(rest, "\x01") {
		if tok == "" {
			continue
		}
		kv := strings.SplitN(tok, "\x00", 2)
		if len(kv) == 2 {
			pairs = append(pairs, kv[0]+"="+kv[1])
		}
	}
	return name + "{" + strings.Join(pairs, ",") + "}"
}
