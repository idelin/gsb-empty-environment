package main

import (
	"fmt"
	"strings"
)

type segMatcher struct {
	any bool
	lit string
}

type pathPattern struct {
	segs       []segMatcher
	doubleStar bool
}

func parsePattern(s string) (pathPattern, error) {
	var p pathPattern
	s = strings.Trim(s, "/")
	if s == "" {
		return p, nil
	}
	parts := strings.Split(s, "/")
	for i, part := range parts {
		switch {
		case part == "**":
			if i != len(parts)-1 {
				return p, fmt.Errorf("** may only appear as the last segment")
			}
			p.doubleStar = true
		case part == "*":
			p.segs = append(p.segs, segMatcher{any: true})
		case part == "":
			return p, fmt.Errorf("empty segment in pattern")
		case strings.Contains(part, "*"):
			return p, fmt.Errorf("invalid segment %q: use * or ** as a whole segment", part)
		default:
			p.segs = append(p.segs, segMatcher{lit: part})
		}
	}
	return p, nil
}

func (p pathPattern) matches(path []string) bool {
	if p.doubleStar {
		if len(path) < len(p.segs) {
			return false
		}
	} else if len(path) != len(p.segs) {
		return false
	}
	for i, m := range p.segs {
		if m.any {
			continue
		}
		if path[i] != m.lit {
			return false
		}
	}
	return true
}
