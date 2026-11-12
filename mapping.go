// Package netsec provides privacy-preserving trimming of generated-to-original
// source mappings for a Go symbol service.
//
// A mapping binds every position in generated code to the most recent entry at
// or before that position (the standard source-map predecessor, or "range",
// lookup rule). Publishing only entries whose original source is in an allowed
// set is not enough on its own: removing the first disallowed entry would let
// the preceding allowed entry remain the predecessor for the whole disallowed
// run, leaking source locations for generated positions that must be hidden.
// Trim therefore replaces each maximal run of disallowed entries with a single
// nil-original boundary at the run's first generated position, so predecessor
// lookups inside the forbidden region resolve to "no original location" rather
// than to a prior allowed entry, while allowed regions keep their original
// range semantics. A leading disallowed run is replaced by a boundary too,
// preserving the original mapping's coverage start.
package netsec

import (
	"errors"
	"slices"
)

// GenPos is a zero-based position in generated code: Line is the line number
// and Col is the column (or VLQ-relative offset) on that line.
type GenPos struct {
	Line int
	Col  int
}

// CompareGenPos orders generated positions lexicographically by line then
// column. It returns -1, 0, or 1 when a is respectively before, equal to, or
// after b.
func CompareGenPos(a, b GenPos) int {
	switch {
	case a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col):
		return -1
	case a.Line == b.Line && a.Col == b.Col:
		return 0
	default:
		return 1
	}
}

// OrigPos is a position in an original source file.
type OrigPos struct {
	Source string
	Line   int
	Col    int
}

// Entry is one mapping record. Gen is the generated position and Orig points at
// the original source location. A nil Orig denotes a generated-only boundary:
// it is a valid predecessor that carries no publishable original location.
type Entry struct {
	Gen  GenPos
	Orig *OrigPos
}

// Mapping is an immutable, ordered sequence of entries supporting predecessor
// range queries. Entries are sorted by strictly increasing generated position.
type Mapping struct {
	entries []Entry
}

// NewMapping validates and stores entries, returning an error if any entry's
// generated position is not strictly greater than the previous one. The input
// slice is copied, so later mutation by the caller does not affect the mapping.
func NewMapping(entries []Entry) (*Mapping, error) {
	for i := 1; i < len(entries); i++ {
		if CompareGenPos(entries[i-1].Gen, entries[i].Gen) >= 0 {
			return nil, errors.New("netsec: mapping entries must be strictly increasing by generated position")
		}
	}
	copied := slices.Clone(entries)
	return &Mapping{entries: copied}, nil
}

// Len returns the number of entries in the mapping.
func (m *Mapping) Len() int {
	return len(m.entries)
}

// Entries returns a copy of the mapping's entries in generated-position order.
func (m *Mapping) Entries() []Entry {
	return slices.Clone(m.entries)
}

// Lookup implements the range query: it returns the last entry whose generated
// position is at or before pos. ok is false when pos precedes every entry;
// otherwise entry is the predecessor and a nil Orig means no original location
// is published for that generated range.
func (m *Mapping) Lookup(pos GenPos) (entry Entry, ok bool) {
	i, found := slices.BinarySearchFunc(m.entries, pos, func(e Entry, target GenPos) int {
		return CompareGenPos(e.Gen, target)
	})
	if found {
		return m.entries[i], true
	}
	if i == 0 {
		return Entry{}, false
	}
	return m.entries[i-1], true
}

// AllowedSources is the set of original source names permitted in a published
// mapping. The zero value is an empty set that disallows every source.
type AllowedSources struct {
	names map[string]struct{}
}

// NewAllowedSources builds an allowed-source set from names.
func NewAllowedSources(names ...string) *AllowedSources {
	set := &AllowedSources{names: make(map[string]struct{}, len(names))}
	for _, name := range names {
		set.names[name] = struct{}{}
	}
	return set
}

// Add marks name as allowed and returns the set for chaining.
func (s *AllowedSources) Add(name string) *AllowedSources {
	if s.names == nil {
		s.names = make(map[string]struct{})
	}
	s.names[name] = struct{}{}
	return s
}

// Allows reports whether name may appear in a published mapping.
func (s *AllowedSources) Allows(name string) bool {
	_, ok := s.names[name]
	return ok
}

// Trim returns a mapping in which every original source is allowed by sources,
// while predecessor range-query semantics are preserved exactly.
//
// Allowed entries and generated-only (nil Orig) entries are kept verbatim.
// Each maximal run of disallowed entries is replaced by one nil-Orig boundary
// at the run's first generated position. The boundary occupies the same
// generated position as the run's first entry, so every position in the run
// resolves to "no original location" instead of a preceding allowed entry,
// including when the run is the leading run of the mapping.
//
// The returned mapping never references a disallowed source, and lookups over
// allowed regions are unchanged relative to the input mapping.
func (m *Mapping) Trim(sources *AllowedSources) *Mapping {
	kept := make([]Entry, 0, len(m.entries))
	inForbidden := false
	for _, entry := range m.entries {
		if entry.Orig == nil {
			kept = append(kept, entry)
			inForbidden = false
			continue
		}
		if sources.Allows(entry.Orig.Source) {
			kept = append(kept, entry)
			inForbidden = false
			continue
		}
		if !inForbidden {
			kept = append(kept, Entry{Gen: entry.Gen})
		}
		inForbidden = true
	}
	return &Mapping{entries: kept}
}

// Trim is the package-level convenience wrapper around Mapping.Trim.
func Trim(m *Mapping, sources *AllowedSources) *Mapping {
	return m.Trim(sources)
}
