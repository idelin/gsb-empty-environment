package netsec

// Fnmatch reports whether name matches pattern using POSIX fnmatch-style
// matching. Matching is case-sensitive and '*' also matches '/', which is
// important for DDS/ROS topic names such as "rt/chatter".
//
// Supported syntax:
//
//   - any sequence, including an empty one
//     ?        any single byte
//     [seq]    one byte from seq; ranges such as [a-z] are supported
//     [!seq]   one byte not in seq; [^seq] is accepted as well
//     \c       the literal byte c
//
// POSIX character classes such as [[:digit:]] are supported. A malformed
// pattern never matches.
func Fnmatch(pattern, name string) bool {
	if !validPattern(pattern) {
		return false
	}

	type state struct {
		pattern int
		name    int
	}
	memo := make(map[state]bool)

	var match func(p, n int) bool
	match = func(p, n int) bool {
		key := state{pattern: p, name: n}
		if cached, ok := memo[key]; ok {
			return cached
		}

		result := false
		defer func() {
			memo[key] = result
		}()

		for {
			if p == len(pattern) {
				result = n == len(name)
				return result
			}

			switch pattern[p] {
			case '*':
				for p < len(pattern) && pattern[p] == '*' {
					p++
				}
				if p == len(pattern) {
					result = true
					return result
				}
				for i := n; i <= len(name); i++ {
					if match(p, i) {
						result = true
						return result
					}
				}
				return false

			case '?':
				if n == len(name) {
					return false
				}
				p++
				n++

			case '\\':
				p++
				if p == len(pattern) || n == len(name) || name[n] != pattern[p] {
					return false
				}
				p++
				n++

			case '[':
				if n == len(name) {
					return false
				}
				matched, next, ok := matchClass(pattern, p, name[n])
				if !ok || !matched {
					return false
				}
				p = next
				n++

			default:
				if n == len(name) || name[n] != pattern[p] {
					return false
				}
				p++
				n++
			}
		}
	}

	return match(0, 0)
}

func validPattern(pattern string) bool {
	for p := 0; p < len(pattern); {
		switch pattern[p] {
		case '\\':
			if p+1 >= len(pattern) {
				return false
			}
			p += 2
		case '[':
			_, next, ok := matchClass(pattern, p, 0)
			if !ok {
				return false
			}
			p = next
		default:
			p++
		}
	}
	return true
}

// matchClass parses a bracket expression beginning at start ('['). It returns
// whether c is included, the index just past the closing ']', and whether the
// expression is syntactically valid.
func matchClass(pattern string, start int, c byte) (matched bool, next int, ok bool) {
	p := start + 1
	negated := false
	if p < len(pattern) && (pattern[p] == '!' || pattern[p] == '^') {
		negated = true
		p++
	}

	result := false
	first := true
	for p < len(pattern) {
		if pattern[p] == ']' && !first {
			return result != negated, p + 1, true
		}
		first = false

		// POSIX character class: [:alpha:], [:digit:], etc.
		if pattern[p] == '[' && p+1 < len(pattern) && pattern[p+1] == ':' {
			relativeEnd := indexString(pattern[p+2:], ":]")
			if relativeEnd < 0 {
				return false, 0, false
			}
			name := pattern[p+2 : p+2+relativeEnd]
			if posixClassMatches(name, c) {
				result = true
			} else if _, known := posixClass(name); !known {
				return false, 0, false
			}
			p = p + 2 + relativeEnd + 2
			continue
		}

		var low byte
		if pattern[p] == '\\' {
			if p+1 >= len(pattern) {
				return false, 0, false
			}
			low = pattern[p+1]
			p += 2
		} else {
			low = pattern[p]
			p++
		}

		if p+1 < len(pattern) && pattern[p] == '-' && pattern[p+1] != ']' {
			p++ // '-'
			var high byte
			if pattern[p] == '\\' {
				if p+1 >= len(pattern) {
					return false, 0, false
				}
				high = pattern[p+1]
				p += 2
			} else if pattern[p] == '[' && p+1 < len(pattern) && pattern[p+1] == ':' {
				// Ranges cannot use a POSIX class as an endpoint.
				return false, 0, false
			} else {
				high = pattern[p]
				p++
			}
			if low > high {
				return false, 0, false
			}
			if c >= low && c <= high {
				result = true
			}
			continue
		}

		if c == low {
			result = true
		}
	}

	// Unterminated bracket expression.
	return false, 0, false
}

func indexString(s, needle string) int {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func posixClass(name string) (func(byte) bool, bool) {
	switch name {
	case "alnum":
		return func(c byte) bool {
			return isAlpha(c) || isDigit(c)
		}, true
	case "alpha":
		return isAlpha, true
	case "blank":
		return func(c byte) bool { return c == ' ' || c == '\t' }, true
	case "cntrl":
		return func(c byte) bool { return c < 0x20 || c == 0x7f }, true
	case "digit":
		return isDigit, true
	case "graph":
		return func(c byte) bool { return c > 0x20 && c < 0x7f }, true
	case "lower":
		return func(c byte) bool { return c >= 'a' && c <= 'z' }, true
	case "print":
		return func(c byte) bool { return c >= 0x20 && c < 0x7f }, true
	case "punct":
		return func(c byte) bool {
			return c > 0x20 && c < 0x7f && !isAlpha(c) && !isDigit(c)
		}, true
	case "space":
		return func(c byte) bool {
			return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
		}, true
	case "upper":
		return func(c byte) bool { return c >= 'A' && c <= 'Z' }, true
	case "xdigit":
		return func(c byte) bool {
			return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		}, true
	default:
		return nil, false
	}
}

func posixClassMatches(name string, c byte) bool {
	fn, ok := posixClass(name)
	return ok && fn(c)
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
