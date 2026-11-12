package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a NuGet-style version: up to four numeric components plus an
// optional pre-release suffix. Build metadata ("+...") is ignored.
type Version struct {
	nums [4]int
	n    int
	pre  string
}

// ParseVersion parses versions such as "1.0", "1.2.3.4", "2.0.0-rc.1".
func ParseVersion(s string) (*Version, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty version")
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return nil, fmt.Errorf("version %q has more than 4 components", orig)
	}
	v := &Version{pre: pre}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("version %q has invalid component %q", orig, p)
		}
		v.nums[v.n] = n
		v.n++
	}
	return v, nil
}

// Compare returns -1, 0 or 1. Missing numeric components count as zero, so
// "1.0" equals "1.0.0". A release sorts after its pre-releases.
func (v *Version) Compare(o *Version) int {
	for i := 0; i < 4; i++ {
		switch {
		case v.nums[i] < o.nums[i]:
			return -1
		case v.nums[i] > o.nums[i]:
			return 1
		}
	}
	return comparePrerelease(v.pre, o.pre)
}

func (v *Version) String() string {
	parts := make([]string, v.n)
	for i := 0; i < v.n; i++ {
		parts[i] = strconv.Itoa(v.nums[i])
	}
	if v.pre != "" {
		return strings.Join(parts, ".") + "-" + v.pre
	}
	return strings.Join(parts, ".")
}

func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := comparePrereleaseIdent(as[i], bs[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

func comparePrereleaseIdent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// VersionRange is a NuGet version range such as "[1.0, 2.0)", "[1.0]",
// "(, 2.0]" or a bare "1.0" (meaning ">= 1.0").
type VersionRange struct {
	min, max         *Version
	minIncl, maxIncl bool
	exact            *Version
}

// ParseVersionRange parses a NuGet version range.
func ParseVersionRange(s string) (*VersionRange, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty version range")
	}
	if s[0] == '[' || s[0] == '(' {
		if len(s) < 2 {
			return nil, fmt.Errorf("malformed version range %q", orig)
		}
		minIncl := s[0] == '['
		switch s[len(s)-1] {
		case ']':
			return parseRangeBody(orig, s[1:len(s)-1], minIncl, true)
		case ')':
			return parseRangeBody(orig, s[1:len(s)-1], minIncl, false)
		default:
			return nil, fmt.Errorf("version range %q is missing a closing bracket", orig)
		}
	}
	v, err := ParseVersion(s)
	if err != nil {
		return nil, err
	}
	return &VersionRange{min: v, minIncl: true}, nil
}

func parseRangeBody(orig, body string, minIncl, maxIncl bool) (*VersionRange, error) {
	if !strings.Contains(body, ",") {
		if !minIncl || !maxIncl {
			return nil, fmt.Errorf("exact version range %q must use square brackets", orig)
		}
		v, err := ParseVersion(body)
		if err != nil {
			return nil, err
		}
		return &VersionRange{exact: v}, nil
	}
	parts := strings.SplitN(body, ",", 2)
	r := &VersionRange{minIncl: minIncl, maxIncl: maxIncl}
	if lo := strings.TrimSpace(parts[0]); lo != "" {
		v, err := ParseVersion(lo)
		if err != nil {
			return nil, err
		}
		r.min = v
	}
	if hi := strings.TrimSpace(parts[1]); hi != "" {
		v, err := ParseVersion(hi)
		if err != nil {
			return nil, err
		}
		r.max = v
	}
	if r.min == nil && r.max == nil {
		return nil, fmt.Errorf("version range %q has no bounds", orig)
	}
	return r, nil
}

// SatisfiedBy reports whether v falls inside the range.
func (r *VersionRange) SatisfiedBy(v *Version) bool {
	if r.exact != nil {
		return v.Compare(r.exact) == 0
	}
	if r.min != nil {
		c := v.Compare(r.min)
		if c < 0 || (c == 0 && !r.minIncl) {
			return false
		}
	}
	if r.max != nil {
		c := v.Compare(r.max)
		if c > 0 || (c == 0 && !r.maxIncl) {
			return false
		}
	}
	return true
}
