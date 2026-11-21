package nsecproof

import (
	"errors"
	"fmt"
	"strings"
)

// ErrReversedInterval is returned when an NSEC owner name is not strictly
// smaller than its NextDomain in canonical ordering, i.e. the interval
// (owner, next) would be empty or wrap the wrong way.
var ErrReversedInterval = errors.New("nsec: reversed interval (owner must be canonically less than next domain)")

// Name is a wire-ordered, lowercased, absolute DNS name used for canonical
// ordering (RFC 4034, section 6.1).
type Name struct {
	labels []string
	abs    bool
}

// ParseName converts a presentation-form name into a canonical Name.
func ParseName(s string) (Name, error) {
	abs := strings.HasSuffix(s, ".")
	trimmed := strings.TrimSuffix(s, ".")
	if trimmed == "" {
		return Name{labels: nil, abs: true}, nil
	}
	parts := strings.Split(trimmed, ".")
	labels := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return Name{}, fmt.Errorf("nsec: empty label in name %q", s)
		}
		if len(p) > 63 {
			return Name{}, fmt.Errorf("nsec: label too long in name %q", s)
		}
		labels = append(labels, strings.ToLower(p))
	}
	return Name{labels: labels, abs: abs}, nil
}

// MustParseName parses a name or panics; intended for static test fixtures.
func MustParseName(s string) Name {
	n, err := ParseName(s)
	if err != nil {
		panic(err)
	}
	return n
}

// String renders the name in presentation form.
func (n Name) String() string {
	if len(n.labels) == 0 {
		return "."
	}
	s := strings.Join(n.labels, ".")
	if n.abs {
		return s + "."
	}
	return s
}

// Equal reports whether two names have the same labels.
func (n Name) Equal(o Name) bool {
	if len(n.labels) != len(o.labels) {
		return false
	}
	for i := range n.labels {
		if n.labels[i] != o.labels[i] {
			return false
		}
	}
	return true
}

// IsSubDomain reports whether n is within zone (equal to the apex or below it).
func (n Name) IsSubDomain(zone Name) bool {
	if len(n.labels) < len(zone.labels) {
		return false
	}
	off := len(n.labels) - len(zone.labels)
	for i := range zone.labels {
		if n.labels[off+i] != zone.labels[i] {
			return false
		}
	}
	return true
}

// compareNames implements canonical DNS name ordering per RFC 4034 6.1.
// It returns -1, 0 or +1. The root (zero labels) sorts first.
func compareNames(a, b Name) int {
	if len(a.labels) == 0 {
		if len(b.labels) == 0 {
			return 0
		}
		return -1
	}
	if len(b.labels) == 0 {
		return 1
	}
	// Compare from the most significant (rightmost) label upward.
	i, j := len(a.labels)-1, len(b.labels)-1
	for i >= 0 && j >= 0 {
		if c := compareLabels(a.labels[i], b.labels[j]); c != 0 {
			return c
		}
		i--
		j--
	}
	// Shared suffix is equal; the shorter name comes first.
	switch {
	case i < 0 && j < 0:
		return 0
	case i < 0:
		return -1
	default:
		return 1
	}
}

func compareLabels(a, b string) int {
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// NSEC is one NSEC resource record: every name x with owner < x < nextDomain
// (in canonical ordering, with wrap-around at the zone apex) is provably
// absent from the zone.
type NSEC struct {
	Owner      Name
	NextDomain Name
	Types      []string
}

// Validate rejects structurally invalid intervals. A normal interval must
// satisfy owner < nextDomain; the chain's closing record wraps to the zone
// apex (nextDomain == zone) and is also valid. Equal endpoints, or a
// backwards interval that does not close on the apex, are rejected.
func (r NSEC) Validate(zone Name) error {
	if r.Owner.Equal(r.NextDomain) {
		return fmt.Errorf("%w: owner=%s next=%s", ErrReversedInterval, r.Owner, r.NextDomain)
	}
	if r.NextDomain.Equal(zone) {
		return nil
	}
	if compareNames(r.Owner, r.NextDomain) >= 0 {
		return fmt.Errorf("%w: owner=%s next=%s", ErrReversedInterval, r.Owner, r.NextDomain)
	}
	return nil
}

// Covers reports whether target falls strictly inside the NSEC interval.
// A normal interval (owner < next) covers owner < target < next. The closing
// record (next == zone apex) wraps around and covers everything above owner.
func (r NSEC) Covers(target, zone Name) bool {
	if compareNames(r.Owner, target) >= 0 {
		return false
	}
	if r.NextDomain.Equal(zone) {
		return true
	}
	return compareNames(target, r.NextDomain) < 0
}

// HasType reports whether the NSEC type bitmap asserts existence of qtype.
func (r NSEC) HasType(qtype string) bool {
	qtype = strings.ToUpper(qtype)
	for _, t := range r.Types {
		if strings.ToUpper(t) == qtype {
			return true
		}
	}
	return false
}
