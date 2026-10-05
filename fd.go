// Package fd implements functional-dependency normalization:
// canonical cover (singleton RHS, extraneous LHS removal, redundant FD
// removal) with a fixed elimination order, attribute closure with a
// derivation trace, and equivalence checking between FD sets.
package fd

import (
	"fmt"
	"sort"
	"strings"
)

// AttrSet is a set of attribute names kept as a sorted slice.
type AttrSet []string

// FD is a functional dependency LHS -> RHS with a single-attribute RHS.
type FD struct {
	LHS AttrSet
	RHS string
}

func (f FD) String() string {
	if len(f.LHS) == 0 {
		return "{} -> " + f.RHS
	}
	return strings.Join(f.LHS, ",") + " -> " + f.RHS
}

// key uniquely identifies an FD for dedup/sorting.
func (f FD) key() string {
	return strings.Join(f.LHS, "\x00") + "\x01" + f.RHS
}

// NewAttrSet builds a normalized (sorted, deduped) attribute set.
func NewAttrSet(attrs ...string) AttrSet {
	set := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		if a != "" {
			set[a] = true
		}
	}
	out := make(AttrSet, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func (s AttrSet) Contains(a string) bool {
	i := sort.SearchStrings(s, a)
	return i < len(s) && s[i] == a
}

// SubsetOf reports whether s ⊆ t.
func (s AttrSet) SubsetOf(t AttrSet) bool {
	for _, a := range s {
		if !t.Contains(a) {
			return false
		}
	}
	return true
}

func (s AttrSet) union(o AttrSet) AttrSet {
	m := make(map[string]bool, len(s)+len(o))
	for _, a := range s {
		m[a] = true
	}
	for _, a := range o {
		m[a] = true
	}
	out := make(AttrSet, 0, len(m))
	for a := range m {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// minus returns s \ {a}.
func (s AttrSet) minus(a string) AttrSet {
	out := make(AttrSet, 0, len(s))
	for _, x := range s {
		if x != a {
			out = append(out, x)
		}
	}
	return out
}

func (s AttrSet) String() string {
	if len(s) == 0 {
		return "{}"
	}
	return "{" + strings.Join(s, ",") + "}"
}

// sortFDs orders FDs deterministically: by LHS (length, then
// lexicographic), then by RHS. This fixes the elimination order so the
// canonical cover output is stable.
func sortFDs(fds []FD) {
	sort.Slice(fds, func(i, j int) bool {
		a, b := fds[i], fds[j]
		if len(a.LHS) != len(b.LHS) {
			return len(a.LHS) < len(b.LHS)
		}
		for k := 0; k < len(a.LHS) && k < len(b.LHS); k++ {
			if a.LHS[k] != b.LHS[k] {
				return a.LHS[k] < b.LHS[k]
			}
		}
		return a.RHS < b.RHS
	})
}

// dedupFDs removes exact duplicates, preserving first occurrence.
func dedupFDs(fds []FD) []FD {
	seen := make(map[string]bool, len(fds))
	out := make([]FD, 0, len(fds))
	for _, f := range fds {
		if seen[f.key()] {
			continue
		}
		seen[f.key()] = true
		out = append(out, f)
	}
	return out
}

// ParseFDs parses dependencies separated by newlines or ';'.
// Accepted forms (LHS/RHS are comma/space separated tokens, or a run of
// single-character attributes when no separator is present):
//
//	AB -> C      A,B -> CD      {} -> A      -> A      ∅ -> A
//
// Multi-attribute RHS is allowed here; splitting happens in Normalize.
// Lines starting with '#' are comments.
func ParseFDs(input string) ([]FD, error) {
	var fds []FD
	input = strings.Replace(input, ";", "\n", -1)
	for ln, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "->", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d: missing '->': %q", ln+1, line)
		}
		lhs, err := parseAttrs(parts[0], true)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", ln+1, err)
		}
		rhs, err := parseAttrs(parts[1], false)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", ln+1, err)
		}
		if len(rhs) == 0 {
			return nil, fmt.Errorf("line %d: empty RHS in %q", ln+1, line)
		}
		for _, r := range rhs {
			fds = append(fds, FD{LHS: lhs, RHS: r})
		}
	}
	return fds, nil
}

// parseAttrs parses an attribute list. Empty (or "{}", "∅") is allowed
// only when allowEmpty is true (i.e. for a left-hand side).
func parseAttrs(s string, allowEmpty bool) (AttrSet, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" || s == "∅" || s == "Ø" {
		if allowEmpty {
			return AttrSet{}, nil
		}
		return nil, fmt.Errorf("empty attribute set not allowed here")
	}
	var raw []string
	if strings.ContainsAny(s, ", \t") {
		raw = strings.FieldsFunc(s, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
	} else {
		for _, r := range s {
			raw = append(raw, string(r))
		}
	}
	for _, a := range raw {
		if !validAttr(a) {
			return nil, fmt.Errorf("invalid attribute %q", a)
		}
	}
	return NewAttrSet(raw...), nil
}

func validAttr(a string) bool {
	for _, r := range a {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return len(a) > 0
}

// ParseAttrs parses a standalone attribute list (empty allowed), using
// the same syntax as an FD left-hand side.
func ParseAttrs(s string) (AttrSet, error) {
	return parseAttrs(s, true)
}
