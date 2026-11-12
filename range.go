package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// ComparatorOp is one of the semver range comparison operators.
type ComparatorOp string

const (
	OpEq  ComparatorOp = "="
	OpLt  ComparatorOp = "<"
	OpLte ComparatorOp = "<="
	OpGt  ComparatorOp = ">"
	OpGte ComparatorOp = ">="
)

// ErrInvalidRange is returned when a semver range cannot be parsed.
var ErrInvalidRange = errors.New("netsec: invalid semver range")

// Comparator is a single bound such as ">=1.0.0".
type Comparator struct {
	Op      ComparatorOp
	Version Version
}

// Range is a conjunction of comparators, e.g. ">=1.0.0 <2.0.0".
// The special range "*" matches every version. A range is satisfied only
// when every comparator matches.
type Range struct {
	raw         string
	wildcard    bool
	comparators []Comparator
}

// ParseRange parses a semver range. Accepted forms are "*", a single
// version (treated as "="), or one or more whitespace separated
// comparators such as ">=1.0.0 <2.0.0". Comparators share a conjunction.
func ParseRange(s string) (Range, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return Range{}, fmt.Errorf("%w: %q", ErrInvalidRange, s)
	}
	if trimmed == "*" || trimmed == "x" || trimmed == "X" {
		return Range{raw: s, wildcard: true}, nil
	}

	fields := strings.Fields(trimmed)
	comps := make([]Comparator, 0, len(fields))
	for _, field := range fields {
		comp, err := parseComparator(field)
		if err != nil {
			return Range{}, err
		}
		comps = append(comps, comp)
	}
	return Range{raw: s, comparators: comps}, nil
}

func parseComparator(field string) (Comparator, error) {
	op := OpEq
	rest := field
	for _, candidate := range []ComparatorOp{OpLte, OpGte, OpLt, OpGt, OpEq} {
		if strings.HasPrefix(field, string(candidate)) {
			op = candidate
			rest = field[len(candidate):]
			break
		}
	}
	if rest == "" || strings.ContainsAny(rest, "<>= ") {
		return Comparator{}, fmt.Errorf("%w: %q", ErrInvalidRange, field)
	}
	ver, err := ParseVersion(rest)
	if err != nil {
		return Comparator{}, fmt.Errorf("%w: %q", ErrInvalidRange, field)
	}
	return Comparator{Op: op, Version: ver}, nil
}

// Contains reports whether v falls inside the range.
//
// Matching is based purely on semver precedence, so prerelease versions
// are matched like any other version: a prerelease inside the declared
// bounds is contained, and build metadata is ignored. This deliberately
// errs toward flagging potentially malicious prereleases rather than
// silently excluding them from violation ranges.
func (r Range) Contains(v Version) bool {
	if r.wildcard {
		return true
	}
	for _, comp := range r.comparators {
		if !comp.matches(v) {
			return false
		}
	}
	return true
}

func (c Comparator) matches(v Version) bool {
	cmp := v.Compare(c.Version)
	switch c.Op {
	case OpEq:
		return cmp == 0
	case OpLt:
		return cmp < 0
	case OpLte:
		return cmp <= 0
	case OpGt:
		return cmp > 0
	case OpGte:
		return cmp >= 0
	default:
		return false
	}
}

// String returns the original range expression.
func (r Range) String() string {
	return r.raw
}
