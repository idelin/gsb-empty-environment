// Package fd implements functional-dependency normalization:
// attribute closure, canonical (minimal) cover with a fixed,
// deterministic elimination order, and equivalence checking
// between dependency sets. Every deletion produces derivation
// evidence so the result is auditable.
package fd

import (
	"fmt"
	"sort"
	"strings"
)

// FD is a functional dependency LHS -> RHS. LHS and RHS are each
// held as sorted, de-duplicated attribute name lists. RHS may hold
// several attributes before normalization; the canonical cover
// always carries a single-attribute RHS.
type FD struct {
	LHS []string
	RHS []string
}

// NewFD builds an FD, normalizing both sides (sort + dedupe).
func NewFD(lhs, rhs []string) FD {
	return FD{LHS: normalizeAttrs(lhs), RHS: normalizeAttrs(rhs)}
}

func normalizeAttrs(attrs []string) []string {
	seen := make(map[string]bool, len(attrs))
	out := make([]string, 0, len(attrs))
	for _, a := range attrs {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Key returns a canonical string form, used for deterministic
// ordering and for de-duplicating dependencies.
func (f FD) Key() string {
	return strings.Join(f.LHS, ",") + "->" + strings.Join(f.RHS, ",")
}

func (f FD) String() string {
	lhs := strings.Join(f.LHS, ",")
	if lhs == "" {
		lhs = "{}"
	}
	return lhs + " -> " + strings.Join(f.RHS, ",")
}

// SortFDs orders dependencies deterministically: first by LHS key,
// then by RHS key. All elimination passes iterate in this order so
// the resulting cover is stable regardless of input permutation.
func SortFDs(fds []FD) {
	sort.Slice(fds, func(i, j int) bool {
		li, lj := strings.Join(fds[i].LHS, ","), strings.Join(fds[j].LHS, ",")
		if li != lj {
			return li < lj
		}
		return strings.Join(fds[i].RHS, ",") < strings.Join(fds[j].RHS, ",")
	})
}

// ParseFDs parses one dependency per line. Format:
//
//	A,B -> C        comma/space separated attribute names
//	AB -> C         bare run of single uppercase letters (textbook style)
//	{} -> A         empty left-hand side (also: "-> A" or "∅ -> A")
//
// Blank lines and lines starting with '#' are ignored.
func ParseFDs(input string) ([]FD, error) {
	var fds []FD
	lines := strings.Split(input, "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		arrow := "->"
		if strings.Contains(line, "→") {
			arrow = "→"
		}
		parts := strings.SplitN(line, arrow, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d: missing '%s' in %q", i+1, arrow, line)
		}
		lhs, err := parseSide(parts[0])
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		rhs, err := parseSide(parts[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		if len(rhs) == 0 {
			return nil, fmt.Errorf("line %d: right-hand side must not be empty", i+1)
		}
		fds = append(fds, NewFD(lhs, rhs))
	}
	return fds, nil
}

func parseSide(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" || s == "∅" || s == "Ø" {
		return nil, nil
	}
	if strings.ContainsAny(s, ", \t") {
		return normalizeAttrs(strings.FieldsFunc(s, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})), nil
	}
	if isUpperRun(s) {
		attrs := make([]string, 0, len(s))
		for _, r := range s {
			attrs = append(attrs, string(r))
		}
		return normalizeAttrs(attrs), nil
	}
	return []string{s}, nil
}

// isUpperRun reports whether s consists solely of uppercase ASCII
// letters and digits, in which case each character is treated as a
// separate attribute (classic textbook notation, e.g. AB -> C).
func isUpperRun(s string) bool {
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return len(s) > 1
}

// Contains reports whether the attribute set holds a.
func Contains(attrs []string, a string) bool {
	i := sort.SearchStrings(attrs, a)
	return i < len(attrs) && attrs[i] == a
}

// SubsetOf reports whether sub ⊆ super; both must be sorted.
func SubsetOf(sub, super []string) bool {
	for _, a := range sub {
		if !Contains(super, a) {
			return false
		}
	}
	return true
}

// Remove returns the set with a deleted (nil-safe, sorted result).
func Remove(attrs []string, a string) []string {
	out := make([]string, 0, len(attrs))
	for _, x := range attrs {
		if x != a {
			out = append(out, x)
		}
	}
	return out
}
