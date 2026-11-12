package vex

import (
	"errors"
	"strconv"
	"strings"
)

// semVer is a minimal semantic version used for ordering concrete component
// versions and the boundaries of version ranges.
type semVer struct {
	core    [3]int
	pre     string
	hasPre  bool
	rawCore string
}

func parseSemVer(raw string) (semVer, error) {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" {
		return semVer{}, errors.New("empty version")
	}

	build := strings.IndexByte(v, '+')
	if build >= 0 {
		v = v[:build]
	}
	core := v
	pre := ""
	hasPre := false
	if dash := strings.IndexByte(v, '-'); dash >= 0 {
		core = v[:dash]
		pre = v[dash+1:]
		hasPre = true
	}

	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return semVer{}, errors.New("version has more than three core identifiers: " + raw)
	}
	var parsed semVer
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return semVer{}, errors.New("invalid version identifier: " + raw)
		}
		parsed.core[i] = n
	}
	parsed.pre = pre
	parsed.hasPre = hasPre
	parsed.rawCore = core
	return parsed, nil
}

// compareSemVer returns -1, 0 or 1 as a sorts before, equals or follows b.
// Build metadata is ignored. A pre-release version sorts below the same core
// without a pre-release, matching semantic versioning precedence.
func compareSemVer(a, b semVer) int {
	for i := 0; i < 3; i++ {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case !a.hasPre && !b.hasPre:
		return 0
	case !a.hasPre && b.hasPre:
		return 1
	case a.hasPre && !b.hasPre:
		return -1
	default:
		return comparePre(a.pre, b.pre)
	}
}

func comparePre(a, b string) int {
	if a == b {
		return 0
	}
	idsA := strings.Split(a, ".")
	idsB := strings.Split(b, ".")
	for i := 0; i < len(idsA) && i < len(idsB); i++ {
		an, aErr := strconv.Atoi(idsA[i])
		bn, bErr := strconv.Atoi(idsB[i])
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aErr == nil && bErr != nil:
			return -1
		case aErr != nil && bErr == nil:
			return 1
		default:
			if idsA[i] != idsB[i] {
				if idsA[i] < idsB[i] {
					return -1
				}
				return 1
			}
		}
	}
	if len(idsA) < len(idsB) {
		return -1
	}
	return 1
}

type constraintOp string

const (
	opEq constraintOp = "="
	opNe constraintOp = "!="
	opGt constraintOp = ">"
	opGe constraintOp = ">="
	opLt constraintOp = "<"
	opLe constraintOp = "<="
)

type constraint struct {
	op constraintOp
	v  semVer
}

var ops = []constraintOp{opGe, opLe, opNe, opEq, opGt, opLt}

// parseConstraint accepts expressions such as ">=1.2.0", "< 2.0.0" or a bare
// version, which is interpreted as exact equality.
func parseConstraint(raw string) (constraint, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return constraint{}, errors.New("empty version constraint")
	}
	for _, op := range ops {
		if strings.HasPrefix(s, string(op)) {
			v, err := parseSemVer(strings.TrimSpace(s[len(op):]))
			if err != nil {
				return constraint{}, err
			}
			return constraint{op: op, v: v}, nil
		}
	}
	v, err := parseSemVer(s)
	if err != nil {
		return constraint{}, err
	}
	return constraint{op: opEq, v: v}, nil
}

func (c constraint) satisfied(v semVer) bool {
	cmp := compareSemVer(v, c.v)
	switch c.op {
	case opEq:
		return cmp == 0
	case opNe:
		return cmp != 0
	case opGt:
		return cmp > 0
	case opGe:
		return cmp >= 0
	case opLt:
		return cmp < 0
	case opLe:
		return cmp <= 0
	default:
		return false
	}
}

// parseVersionRange turns a CycloneDX version-range expression into concrete
// constraints. Supported syntaxes are:
//
//   - comma/whitespace separated constraints: ">=1.0, <2.0"
//   - interval notation: "[1.0,2.0)", "(,2.0.0]"
//   - "vers:" URIs (the "vers:" scheme prefix and any URI fragment are
//     stripped before parsing the remaining constraint list)
func parseVersionRange(raw string) ([]constraint, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "*" {
		return nil, nil
	}

	if strings.HasPrefix(s, "vers:") {
		s = strings.TrimPrefix(s, "vers:")
		if slash := strings.IndexByte(s, '/'); slash >= 0 {
			s = s[slash+1:]
		}
		if hash := strings.IndexByte(s, '#'); hash >= 0 {
			s = s[:hash]
		}
	}

	if len(s) >= 2 && (s[0] == '[' || s[0] == '(') {
		return parseInterval(s)
	}

	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '|' || r == ' ' || r == '\t'
	})
	var out []constraint
	for _, part := range parts {
		if part == "" {
			continue
		}
		c, err := parseConstraint(part)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func parseInterval(s string) ([]constraint, error) {
	closedLow := s[0] == '['
	closedHigh := s[len(s)-1] == ']'
	if s[len(s)-1] != ']' && s[len(s)-1] != ')' {
		return nil, errors.New("malformed version interval: " + s)
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	bounds := strings.Split(inner, ",")
	if len(bounds) != 2 {
		return nil, errors.New("version interval must have two bounds: " + s)
	}

	var out []constraint
	low := strings.TrimSpace(bounds[0])
	if low != "" {
		v, err := parseSemVer(low)
		if err != nil {
			return nil, err
		}
		c := constraint{v: v}
		if closedLow {
			c.op = opGe
		} else {
			c.op = opGt
		}
		out = append(out, c)
	}
	high := strings.TrimSpace(bounds[1])
	if high != "" {
		v, err := parseSemVer(high)
		if err != nil {
			return nil, err
		}
		c := constraint{v: v}
		if closedHigh {
			c.op = opLe
		} else {
			c.op = opLt
		}
		out = append(out, c)
	}
	return out, nil
}

func rangeContains(rawRange, version string) bool {
	constraints, err := parseVersionRange(rawRange)
	if err != nil {
		return false
	}
	if len(constraints) == 0 {
		return true
	}
	v, err := parseSemVer(version)
	if err != nil {
		return false
	}
	for _, c := range constraints {
		if !c.satisfied(v) {
			return false
		}
	}
	return true
}
