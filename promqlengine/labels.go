package promqlengine

import (
	"bytes"
	"sort"
)

// Labels is an immutable-ish set of label name/value pairs.
//
// The pair at index i is (names[i], values[i]). Instances produced by this
// package are always sorted by name and contain no duplicate names, which lets
// identity comparisons rely on the byte-wise Fingerprint alone.
type Labels struct {
	names  []string
	values []string
}

// NewLabels builds a label set from name/value pairs. Empty-valued labels are
// dropped (they never select a series in Prometheus) and the result is sorted
// and de-duplicated.
func NewLabels(pairs ...string) Labels {
	if len(pairs)%2 != 0 {
		panic("promqlengine: NewLabels requires name/value pairs")
	}
	type pair struct{ name, value string }
	ps := make([]pair, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			continue
		}
		ps = append(ps, pair{pairs[i], pairs[i+1]})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].name < ps[j].name })
	ls := Labels{
		names:  make([]string, 0, len(ps)),
		values: make([]string, 0, len(ps)),
	}
	for _, p := range ps {
		if n := len(ls.names); n > 0 && ls.names[n-1] == p.name {
			ls.values[n-1] = p.value
			continue
		}
		ls.names = append(ls.names, p.name)
		ls.values = append(ls.values, p.value)
	}
	return ls
}

// Len returns the number of label pairs.
func (ls Labels) Len() int { return len(ls.names) }

// Get returns the value of name, or "" if absent.
func (ls Labels) Get(name string) string {
	i := sort.SearchStrings(ls.names, name)
	if i < len(ls.names) && ls.names[i] == name {
		return ls.values[i]
	}
	return ""
}

// Has reports whether name is part of the set.
func (ls Labels) Has(name string) bool {
	i := sort.SearchStrings(ls.names, name)
	return i < len(ls.names) && ls.names[i] == name
}

// Range calls f for every pair in sorted order. Iteration stops if f returns
// false.
func (ls Labels) Range(f func(name, value string) bool) {
	for i := range ls.names {
		if !f(ls.names[i], ls.values[i]) {
			return
		}
	}
}

// Equal reports whether two label sets contain identical pairs.
func (ls Labels) Equal(other Labels) bool {
	if len(ls.names) != len(other.names) {
		return false
	}
	for i := range ls.names {
		if ls.names[i] != other.names[i] || ls.values[i] != other.values[i] {
			return false
		}
	}
	return true
}

// Fingerprint returns a deterministic byte-wise fingerprint, mirroring the
// ordering semantics of Prometheus label fingerprints (without hashing: the
// raw encoding is unique for every distinct sorted label set).
func (ls Labels) Fingerprint() string {
	var buf bytes.Buffer
	for i := range ls.names {
		buf.WriteString(ls.names[i])
		buf.WriteByte(0xff)
		buf.WriteString(ls.values[i])
		buf.WriteByte(0xfe)
	}
	return buf.String()
}

// String renders the set in Prometheus `{name="value"}` style.
func (ls Labels) String() string {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i := range ls.names {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(ls.names[i])
		buf.WriteString(`="`)
		buf.WriteString(ls.values[i])
		buf.WriteByte('"')
	}
	buf.WriteByte('}')
	return buf.String()
}

// copy returns an independent label set; used when handing sets to callers
// that might retain them.
func (ls Labels) copy() Labels {
	names := make([]string, len(ls.names))
	values := make([]string, len(ls.values))
	copy(names, ls.names)
	copy(values, ls.values)
	return Labels{names: names, values: values}
}
