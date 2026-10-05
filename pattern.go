// Package pathcheck decides whether a set of allow path patterns admits a
// path that no deny pattern rejects, and produces a witness with the
// minimum number of segments.
//
// Patterns are built from literal segments, the single-segment wildcard
// "*" (matches exactly one segment, never crossing the separator) and the
// multi-segment wildcard "**" (matches zero or more segments). The empty
// path "" and the root path "/" are distinct values.
package pathcheck

import (
	"fmt"
	"strings"
)

// SegKind is the kind of a pattern segment.
type SegKind int

const (
	// Lit matches exactly one path segment equal to the literal.
	Lit SegKind = iota
	// Star matches exactly one arbitrary path segment; it never crosses
	// the separator.
	Star
	// StarStar matches zero or more consecutive path segments.
	StarStar
)

// Segment is one segment of a Pattern.
type Segment struct {
	Kind SegKind
	Lit  string
}

// Pattern is a parsed path pattern. Rooted distinguishes the root pattern
// "/" from the empty pattern "".
type Pattern struct {
	Rooted bool
	Segs   []Segment
}

// Path is a concrete path. Rooted distinguishes the root path "/" from the
// empty path "".
type Path struct {
	Rooted bool
	Segs   []string
}

// ParsePattern parses a pattern such as "", "/", "a/b", "a/*" or "a/**/c".
// A segment is a wildcard only when it is exactly "*" or "**"; every other
// segment (including ones containing asterisks, e.g. "a*") is a literal.
// Empty segments are rejected, so "a//b" and "a/" are invalid.
func ParsePattern(s string) (Pattern, error) {
	if s == "" {
		return Pattern{}, nil
	}
	p := Pattern{}
	rest := s
	if strings.HasPrefix(rest, "/") {
		p.Rooted = true
		rest = rest[1:]
	}
	if rest == "" {
		return p, nil
	}
	for _, seg := range strings.Split(rest, "/") {
		switch seg {
		case "":
			return Pattern{}, fmt.Errorf("pathcheck: empty segment in %q", s)
		case "*":
			p.Segs = append(p.Segs, Segment{Kind: Star})
		case "**":
			p.Segs = append(p.Segs, Segment{Kind: StarStar})
		default:
			p.Segs = append(p.Segs, Segment{Kind: Lit, Lit: seg})
		}
	}
	return p, nil
}

// MustPattern is ParsePattern that panics on error; handy for tests and
// static tables.
func MustPattern(s string) Pattern {
	p, err := ParsePattern(s)
	if err != nil {
		panic(err)
	}
	return p
}

// ParsePath parses a concrete path. Wildcards are rejected.
func ParsePath(s string) (Path, error) {
	p, err := ParsePattern(s)
	if err != nil {
		return Path{}, err
	}
	path := Path{Rooted: p.Rooted}
	for _, seg := range p.Segs {
		if seg.Kind != Lit {
			return Path{}, fmt.Errorf("pathcheck: wildcard in path %q", s)
		}
		path.Segs = append(path.Segs, seg.Lit)
	}
	return path, nil
}

func (p Pattern) String() string {
	var b strings.Builder
	if p.Rooted {
		b.WriteByte('/')
	}
	for i, seg := range p.Segs {
		if i > 0 {
			b.WriteByte('/')
		}
		switch seg.Kind {
		case Lit:
			b.WriteString(seg.Lit)
		case Star:
			b.WriteByte('*')
		case StarStar:
			b.WriteString("**")
		}
	}
	return b.String()
}

func (p Path) String() string {
	var b strings.Builder
	if p.Rooted {
		b.WriteByte('/')
	}
	for i, seg := range p.Segs {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(seg)
	}
	return b.String()
}

// Matches reports whether the pattern matches the path. Rootedness must
// agree, "*" consumes exactly one segment and "**" consumes zero or more.
func (p Pattern) Matches(path Path) bool {
	if p.Rooted != path.Rooted {
		return false
	}
	return matchSegs(p.Segs, path.Segs)
}

func matchSegs(pat []Segment, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	switch pat[0].Kind {
	case Lit:
		return len(segs) > 0 && segs[0] == pat[0].Lit && matchSegs(pat[1:], segs[1:])
	case Star:
		return len(segs) > 0 && matchSegs(pat[1:], segs[1:])
	case StarStar:
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	return false
}
