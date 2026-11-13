package netsec

import "strings"

// MatchGroup reports whether group matches pattern. pattern uses the NNTP
// wildmat syntax: comma-separated patterns, *, ?, character classes and
// leading ! negation.
//
// Matching is intentionally case-sensitive, as newsgroup names are.
func MatchGroup(pattern, group string) bool {
	if pattern == "" || group == "" {
		return false
	}

	matched := false
	for _, item := range strings.Split(pattern, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		negated := strings.HasPrefix(item, "!")
		if negated {
			item = item[1:]
		}
		if item == "" {
			continue
		}

		ok := matchWildmat(item, group)
		if negated && ok {
			return false
		}
		if !negated && ok {
			matched = true
		}
	}
	return matched
}

func matchWildmat(pattern, value string) bool {
	for {
		if pattern == "" {
			return value == ""
		}

		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if pattern == "" {
				return true
			}
			for i := 0; i <= len(value); i++ {
				if matchWildmat(pattern, value[i:]) {
					return true
				}
			}
			return false

		case '?':
			if value == "" {
				return false
			}
			pattern, value = pattern[1:], value[1:]

		case '[':
			ok, rest, valid := matchClass(pattern, value)
			if !valid {
				if value == "" || value[0] != '[' {
					return false
				}
				pattern, value = pattern[1:], value[1:]
				continue
			}
			if value == "" || !ok {
				return false
			}
			pattern, value = rest, value[1:]

		case '\\':
			if len(pattern) < 2 {
				if value == "" || value[0] != '\\' {
					return false
				}
				pattern, value = pattern[1:], value[1:]
				continue
			}
			if value == "" || value[0] != pattern[1] {
				return false
			}
			pattern, value = pattern[2:], value[1:]

		default:
			if value == "" || value[0] != pattern[0] {
				return false
			}
			pattern, value = pattern[1:], value[1:]
		}
	}
}

// matchClass matches value[0] against a character class beginning at pattern.
// It returns the match result, the remainder of the pattern after the closing
// bracket, and whether a closing bracket was present.
func matchClass(pattern, value string) (matched bool, rest string, valid bool) {
	if value == "" || len(pattern) < 2 || pattern[0] != '[' {
		return false, pattern, false
	}

	negated := false
	i := 1
	if i < len(pattern) && (pattern[i] == '^' || pattern[i] == '!') {
		negated = true
		i++
	}

	end := -1
	for j := i; j < len(pattern); j++ {
		if pattern[j] == '\\' {
			j++
			continue
		}
		if pattern[j] == ']' {
			end = j
			break
		}
	}
	if end < 0 {
		return false, pattern, false
	}

	ch := value[0]
	found := false
	for j := i; j < end; j++ {
		if pattern[j] == '\\' && j+1 < end {
			j++
			if pattern[j] == ch {
				found = true
			}
			continue
		}
		if j+2 < end && pattern[j+1] == '-' {
			lo, hi := pattern[j], pattern[j+2]
			if lo <= ch && ch <= hi {
				found = true
			}
			j += 2
			continue
		}
		if pattern[j] == ch {
			found = true
		}
	}

	if negated {
		found = !found
	}
	return found, pattern[end+1:], true
}
