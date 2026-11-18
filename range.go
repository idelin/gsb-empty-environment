package netsec

import "fmt"

// VersionRange is a semver interval. The zero value with HasLower/HasUpper
// both false matches every version.
//
// Bounds are compared with semver precedence, so a range over a released
// version (e.g. 1.2.3) never contains its prereleases, and a prerelease bound
// only matches prereleases of the same core version as appropriate.
type VersionRange struct {
	Lower          Version
	Upper          Version
	HasLower       bool
	HasUpper       bool
	LowerInclusive bool
	UpperInclusive bool
}

// ParseVersionRange parses an interval such as "[1.0.0,2.0.0)", "(,1.2.3]"
// or "[1.2.3,1.2.3]". An empty side means unbounded: "[1.0.0,)" matches
// 1.0.0 and every later version. Square brackets are inclusive, parentheses
// exclusive.
func ParseVersionRange(s string) (VersionRange, error) {
	if len(s) < 2 {
		return VersionRange{}, fmt.Errorf("netsec: invalid version range %q", s)
	}
	open, close := s[0], s[len(s)-1]
	if open != '[' && open != '(' {
		return VersionRange{}, fmt.Errorf("netsec: invalid version range %q: expected '[' or '('", s)
	}
	if close != ']' && close != ')' {
		return VersionRange{}, fmt.Errorf("netsec: invalid version range %q: expected ']' or ')'", s)
	}
	body := s[1 : len(s)-1]
	comma := indexByte(body, ',')
	if comma < 0 {
		return VersionRange{}, fmt.Errorf("netsec: invalid version range %q: expected comma between bounds", s)
	}
	lowRaw := body[:comma]
	highRaw := body[comma+1:]

	var r VersionRange
	r.LowerInclusive = open == '['
	r.UpperInclusive = close == ']'
	if lowRaw != "" {
		low, err := ParseVersion(lowRaw)
		if err != nil {
			return VersionRange{}, fmt.Errorf("netsec: invalid lower bound in %q: %w", s, err)
		}
		r.Lower, r.HasLower = low, true
	}
	if highRaw != "" {
		high, err := ParseVersion(highRaw)
		if err != nil {
			return VersionRange{}, fmt.Errorf("netsec: invalid upper bound in %q: %w", s, err)
		}
		r.Upper, r.HasUpper = high, true
	}
	if r.HasLower && r.HasUpper && r.Lower.Compare(r.Upper) > 0 {
		return VersionRange{}, fmt.Errorf("netsec: invalid version range %q: lower bound above upper bound", s)
	}
	return r, nil
}

// Contains reports whether v falls inside the interval, honoring inclusive or
// exclusive bounds. Build metadata on v is ignored as in Version.Compare.
func (r VersionRange) Contains(v Version) bool {
	if r.HasLower {
		c := v.Compare(r.Lower)
		if c < 0 || (c == 0 && !r.LowerInclusive) {
			return false
		}
	}
	if r.HasUpper {
		c := v.Compare(r.Upper)
		if c > 0 || (c == 0 && !r.UpperInclusive) {
			return false
		}
	}
	return true
}
