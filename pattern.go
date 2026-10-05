// Package pathpat decides, given a set of allow patterns and a set of deny
// patterns over slash-separated paths, whether some path is allowed and not
// denied, and produces a witness with the fewest segments.
//
// Patterns contain only three kinds of segments:
//   - literal segments, matching exactly one equal segment;
//   - "*", a single-segment wildcard matching exactly one arbitrary segment
//     (it never crosses the "/" separator);
//   - "**", a multi-segment wildcard matching zero or more segments.
//
// The empty path ("") has zero segments and is explicitly distinguished from
// the root path ("/"), which has exactly one empty segment.
package pathpat

import "strings"

// SegKind classifies pattern segments.
type SegKind int

const (
	// Lit is a literal segment matching exactly one equal segment.
	Lit SegKind = iota
	// Star matches exactly one arbitrary segment.
	Star
	// StarStar matches zero or more arbitrary segments.
	StarStar
)

// Seg is one pattern segment.
type Seg struct {
	Kind SegKind
	Lit  string // only meaningful when Kind == Lit
}

// Pattern is a sequence of segments matched against a whole path.
type Pattern struct {
	Segs []Seg
}

// SplitPath splits a path string into its segments.
//
// The empty path "" has zero segments. The root path "/" has exactly one
// empty segment, so the two are explicitly distinguished. Any other string
// is split on "/".
func SplitPath(s string) []string {
	switch s {
	case "":
		return nil
	case "/":
		return []string{""}
	default:
		return strings.Split(s, "/")
	}
}

// RenderPath renders segments back to a path string; it is the inverse of
// SplitPath for the empty path, the root path, and paths whose segments are
// not all empty.
func RenderPath(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	if len(segs) == 1 && segs[0] == "" {
		return "/"
	}
	return strings.Join(segs, "/")
}

// ParsePattern parses a pattern string. Segments "*" and "**" become the
// single- and multi-segment wildcards; every other segment is literal.
// Pattern "" matches only the empty path; pattern "/" matches only the
// root path.
func ParsePattern(s string) Pattern {
	parts := SplitPath(s)
	p := Pattern{Segs: make([]Seg, 0, len(parts))}
	for _, part := range parts {
		switch part {
		case "*":
			p.Segs = append(p.Segs, Seg{Kind: Star})
		case "**":
			p.Segs = append(p.Segs, Seg{Kind: StarStar})
		default:
			p.Segs = append(p.Segs, Seg{Kind: Lit, Lit: part})
		}
	}
	return p
}

// LitPattern builds a pattern of literal segments.
func LitPattern(segs ...string) Pattern {
	p := Pattern{Segs: make([]Seg, 0, len(segs))}
	for _, s := range segs {
		p.Segs = append(p.Segs, Seg{Kind: Lit, Lit: s})
	}
	return p
}

// Match reports whether the pattern matches the given path segments.
// "*" matches exactly one segment (never a separator), "**" matches zero or
// more segments.
func (p Pattern) Match(path []string) bool {
	n, m := len(p.Segs), len(path)
	// dp[j] holds whether Segs[i:] matches path[j:] for the current i,
	// iterating i from n-1 down to 0; initially i == n.
	dp := make([]bool, m+1)
	dp[m] = true
	for i := n - 1; i >= 0; i-- {
		seg := p.Segs[i]
		switch seg.Kind {
		case StarStar:
			// dp'[j] = dp[j] (zero segments) || dp'[j+1] (one more segment).
			for j := m - 1; j >= 0; j-- {
				dp[j] = dp[j] || dp[j+1]
			}
		case Star:
			// dp'[j] = j < m && dp[j+1]; iterate upwards so dp[j+1] is old.
			for j := 0; j < m; j++ {
				dp[j] = dp[j+1]
			}
			dp[m] = false
		default:
			for j := 0; j < m; j++ {
				dp[j] = dp[j+1] && path[j] == seg.Lit
			}
			dp[m] = false
		}
	}
	return dp[0]
}
