package audit

import (
	"fmt"
	"strconv"
	"strings"
)

// segment is one step of a path: either an object key or an array index.
type segment struct {
	text    string
	isIndex bool
}

// path is a sequence of segments from the document root to a node.
type path []segment

// String renders the path in JSONPath-like form, e.g. "$.spec.containers[0].image".
func (p path) String() string {
	var b strings.Builder
	b.WriteString("$")
	for _, s := range p {
		if s.isIndex {
			b.WriteString("[")
			b.WriteString(s.text)
			b.WriteString("]")
		} else {
			b.WriteString(".")
			b.WriteString(s.text)
		}
	}
	return b.String()
}

// key returns a collision-free identity used for maps and dedup.
func (p path) key() string {
	var b strings.Builder
	for _, s := range p {
		b.WriteByte(0)
		if s.isIndex {
			b.WriteByte('i')
		} else {
			b.WriteByte('k')
		}
		b.WriteString(s.text)
	}
	return b.String()
}

func extendPath(p path, s segment) path {
	q := make(path, len(p)+1)
	copy(q, p)
	q[len(p)] = s
	return q
}

func keySegment(name string) segment { return segment{text: name} }
func indexSegment(i int) segment     { return segment{text: strconv.Itoa(i), isIndex: true} }
func isPrefixOf(prefix, p path) bool {
	if len(prefix) >= len(p) {
		return false
	}
	for i, s := range prefix {
		if p[i] != s {
			return false
		}
	}
	return true
}

// patSegment is one segment of a rule pattern; "*" matches exactly one
// segment (a single level), never more.
type patSegment struct {
	text string
	wild bool
}

type pattern []patSegment

// parsePattern parses a rule pattern such as "$.spec.containers[*].image".
// A leading "$" (optionally followed by ".") is accepted. "" and "$" denote
// the document root. "[*]" is equivalent to a "*" segment.
func parsePattern(s string) (pattern, error) {
	if s == "" || s == "$" {
		return pattern{}, nil
	}
	if strings.HasPrefix(s, "$.") {
		s = s[2:]
	} else if strings.HasPrefix(s, "$") {
		s = s[1:]
	}
	var pat pattern
	i := 0
	needSeg := true // true at start and right after '.'
	for i < len(s) {
		switch s[i] {
		case '.':
			if needSeg {
				return nil, fmt.Errorf("audit: empty segment in pattern %q", s)
			}
			needSeg = true
			i++
		case '[':
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("audit: unterminated '[' in pattern %q", s)
			}
			inner := s[i+1 : i+j]
			switch {
			case inner == "*":
				pat = append(pat, patSegment{wild: true})
			case isDigits(inner):
				pat = append(pat, patSegment{text: inner})
			default:
				return nil, fmt.Errorf("audit: invalid index %q in pattern %q", inner, s)
			}
			i += j + 1
			if i < len(s) && s[i] != '.' && s[i] != '[' {
				return nil, fmt.Errorf("audit: unexpected character after ']' in pattern %q", s)
			}
			needSeg = false
		default:
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			name := s[i:j]
			if name == "*" {
				pat = append(pat, patSegment{wild: true})
			} else {
				pat = append(pat, patSegment{text: name})
			}
			i = j
			needSeg = false
		}
	}
	if needSeg && len(pat) > 0 {
		return nil, fmt.Errorf("audit: trailing '.' in pattern %q", s)
	}
	return pat, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// match reports whether the pattern matches the path exactly: same number of
// segments, each equal or a single-level wildcard.
func (pat pattern) match(p path) bool {
	if len(pat) != len(p) {
		return false
	}
	for i, ps := range pat {
		if ps.wild {
			continue
		}
		if ps.text != p[i].text {
			return false
		}
	}
	return true
}

func matchAny(pats []pattern, p path) bool {
	for _, pat := range pats {
		if pat.match(p) {
			return true
		}
	}
	return false
}
