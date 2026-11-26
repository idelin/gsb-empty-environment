package devpolicy

import (
	"strconv"
	"strings"
)

// ParsePerm converts an "rwm" style permission string (any order, any
// non-empty subset) to its bit mask. The m bit is parsed independently and
// never implies r or w.
func ParsePerm(s string) (uint8, error) {
	if s == "" {
		return 0, invalidf("empty permission string")
	}
	var mask uint8
	for _, ch := range s {
		switch ch {
		case 'r':
			mask |= BitR
		case 'w':
			mask |= BitW
		case 'm':
			mask |= BitM
		default:
			return 0, invalidf("unknown permission %q in %q", string(ch), s)
		}
	}
	return mask, nil
}

// ParseInterval parses one closed number interval.
//
// "*" and "*..*" denote [0, MaxDeviceNo]; "n" denotes [n,n]; "lo..hi"
// denotes the closed interval [lo,hi].
func ParseInterval(s string) (lo, hi uint16, err error) {
	s = strings.TrimSpace(s)
	if s == "*" || s == "*..*" {
		return 0, MaxDeviceNo, nil
	}
	if !strings.Contains(s, "..") {
		n, convErr := strconv.Atoi(s)
		if convErr != nil || n < 0 || n > MaxDeviceNo {
			return 0, 0, invalidf("invalid interval bound %q", s)
		}
		return uint16(n), uint16(n), nil
	}
	parts := strings.SplitN(s, "..", 2)
	lo64, err1 := parseBound(parts[0])
	hi64, err2 := parseBound(parts[1])
	if err1 != nil || err2 != nil || lo64 > hi64 {
		return 0, 0, invalidf("invalid interval %q", s)
	}
	return uint16(lo64), uint16(hi64), nil
}

// parseBound accepts a number or '*' (wildcard edge).
func parseBound(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "*" {
		return MaxDeviceNo, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > MaxDeviceNo {
		return 0, invalidf("invalid bound %q", s)
	}
	return n, nil
}

// ParseRule parses one textual rule of the form
//
// "<allow|deny> <c|b|a> <majorInterval> <minorInterval> <perm>"
//
// for example "allow b 8..15 * rwm" or "deny c 1 0..3 m". Layer and order
// are assigned by the caller afterwards; name defaults to the raw text.
func ParseRule(line string) (Rule, error) {
	fields := strings.Fields(line)
	if len(fields) != 5 {
		return Rule{}, invalidf("rule %q must have 5 fields, got %d", line, len(fields))
	}
	var effect RuleEffect
	switch fields[0] {
	case "allow":
		effect = EffectAllow
	case "deny":
		effect = EffectDeny
	default:
		return Rule{}, invalidf("rule %q: effect must be allow or deny", line)
	}
	t := DevType(fields[1][0])
	if len(fields[1]) != 1 || (t != TypeChar && t != TypeBlock && t != TypeAll) {
		return Rule{}, invalidf("rule %q: type must be c, b or a", line)
	}
	majLo, majHi, err := ParseInterval(fields[2])
	if err != nil {
		return Rule{}, err
	}
	minLo, minHi, err := ParseInterval(fields[3])
	if err != nil {
		return Rule{}, err
	}
	perm, err := ParsePerm(fields[4])
	if err != nil {
		return Rule{}, err
	}
	return Rule{
		Effect:   effect,
		Type:     t,
		MajorMin: majLo, MajorMax: majHi,
		MinorMin: minLo, MinorMax: minHi,
		Perm: perm,
		Name: line,
	}, nil
}
