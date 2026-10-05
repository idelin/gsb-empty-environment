package packer

// segMatch reports whether a single path segment pattern matches name.
// Supported syntax: '*', '?', character classes '[...]' (with '!' or '^'
// negation and 'a-z' ranges) and backslash escapes. Matching is
// segment-local, so '*' never crosses a '/' boundary.
func segMatch(pattern, name string) bool {
	return matchRunes([]rune(pattern), []rune(name))
}

func matchRunes(p, s []rune) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			i := 0
			for i < len(p) && p[i] == '*' {
				i++
			}
			if i == len(p) {
				return true
			}
			for k := 0; k <= len(s); k++ {
				if matchRunes(p[i:], s[k:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			p, s = p[1:], s[1:]
		case '[':
			if len(s) == 0 {
				return false
			}
			class, rest, ok := parseClass(p)
			if !ok {
				if s[0] != '[' {
					return false
				}
				p, s = p[1:], s[1:]
				continue
			}
			if !class(s[0]) {
				return false
			}
			p, s = rest, s[1:]
		case '\\':
			if len(p) < 2 {
				if len(s) == 0 || s[0] != '\\' {
					return false
				}
				p, s = p[1:], s[1:]
				continue
			}
			if len(s) == 0 || s[0] != p[1] {
				return false
			}
			p, s = p[2:], s[1:]
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return len(s) == 0
}

// parseClass parses a '[...]' character class at the start of p.
// ok is false when the class is unterminated, meaning '[' is literal.
func parseClass(p []rune) (match func(rune) bool, rest []rune, ok bool) {
	i := 1
	negate := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		negate = true
		i++
	}
	type rng struct{ lo, hi rune }
	var ranges []rng
	first := true
	for i < len(p) {
		c := p[i]
		if c == ']' && !first {
			m := func(r rune) bool {
				for _, rg := range ranges {
					if r >= rg.lo && r <= rg.hi {
						return !negate
					}
				}
				return negate
			}
			return m, p[i+1:], true
		}
		first = false
		if c == '\\' && i+1 < len(p) {
			i++
			c = p[i]
		}
		if i+2 < len(p) && p[i+1] == '-' && p[i+2] != ']' {
			ranges = append(ranges, rng{c, p[i+2]})
			i += 3
		} else {
			ranges = append(ranges, rng{c, c})
			i++
		}
	}
	return nil, p, false
}

// matchSegments matches an anchored pattern (path segments) against a path.
// A segment equal to "**" matches zero or more whole segments.
func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		if len(pat) == 1 {
			return true
		}
		for k := 0; k <= len(segs); k++ {
			if matchSegments(pat[1:], segs[k:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	return segMatch(pat[0], segs[0]) && matchSegments(pat[1:], segs[1:])
}
