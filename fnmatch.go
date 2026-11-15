package netsec

import (
	"unicode/utf8"
)

// ErrBadPattern indicates that a pattern is malformed (for example a
// character class that is never closed).
var ErrBadPattern = constError("netsec: malformed wildcard pattern")

// Fnmatch reports whether name matches the shell-style pattern.
//
// Matching is always case-sensitive and uses fnmatch-style semantics:
//
//   - '*' matches any (possibly empty) sequence of characters, including
//     path separators such as '/'.
//   - '?' matches exactly one character.
//   - '[...]' matches one of the enclosed characters; '[!...]' negates the
//     class. A leading ']' inside a class is treated as a literal.
//   - A backslash escapes the next character so that it is matched literally.
//
// A malformed pattern (for instance an unclosed '[') returns ErrBadPattern.
func Fnmatch(pattern, name string) (bool, error) {
	if err := validatePattern(pattern); err != nil {
		return false, err
	}
	return matchPattern(pattern, name), nil
}

// validatePattern walks the pattern once to reject malformed character
// classes before any matching takes place.
func validatePattern(pattern string) error {
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '\\':
			// A trailing backslash cannot escape anything.
			if i+1 >= len(pattern) {
				return ErrBadPattern
			}
			i += 2
		case '[':
			j := i + 1
			if j < len(pattern) && pattern[j] == '!' {
				j++
			}
			// A leading ']' inside a class is a literal member.
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				if pattern[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(pattern) {
				return ErrBadPattern
			}
			i = j + 1
		default:
			i++
		}
	}
	return nil
}

// matchPattern implements the recursive, backtracking matcher. The pattern
// is assumed to have been validated already.
func matchPattern(pattern, name string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			// Collapse consecutive stars: "**" behaves like "*".
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 0 {
				return true
			}
			// Try matching the remainder at every position, including the
			// end, so that '*' may match an empty suffix.
			for i := 0; i <= len(name); i++ {
				if matchPattern(pattern, name[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(name) == 0 {
				return false
			}
			_, size := utf8.DecodeRuneInString(name)
			pattern = pattern[1:]
			name = name[size:]
		case '[':
			if len(name) == 0 {
				return false
			}
			r, size := utf8.DecodeRuneInString(name)
			matched, next := matchClass(pattern, r)
			if !matched {
				return false
			}
			pattern = pattern[next:]
			name = name[size:]
		case '\\':
			pattern = pattern[1:]
			if len(pattern) == 0 || pattern[0] != nameFirstByte(name) {
				return false
			}
			_, size := utf8.DecodeRuneInString(name)
			pattern = pattern[1:]
			name = name[size:]
		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			_, size := utf8.DecodeRuneInString(name)
			pattern = pattern[1:]
			name = name[size:]
		}
	}
	return len(name) == 0
}

// matchClass checks whether rune r belongs to the character class beginning
// at pattern[0] (which must be '['). It returns the match result and the
// index just past the closing ']'.
func matchClass(pattern string, r rune) (bool, int) {
	i := 1
	negated := false
	if i < len(pattern) && pattern[i] == '!' {
		negated = true
		i++
	}
	// A leading ']' inside the class is a literal member, not the closer.
	if i < len(pattern) && pattern[i] == ']' {
		if r == ']' {
			return !negated, indexAfterClass(pattern, i+1)
		}
		i++
	}
	matched := false
	for i < len(pattern) && pattern[i] != ']' {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			i++
		}
		c := rune(pattern[i])
		i++
		// Support simple "a-z" ranges.
		if i+2 < len(pattern) && pattern[i] == '-' && pattern[i+1] != ']' {
			low := c
			i++ // '-'
			if pattern[i] == '\\' && i+1 < len(pattern) {
				i++
			}
			high := rune(pattern[i])
			i++
			if r >= low && r <= high {
				matched = true
			}
			continue
		}
		if c == r {
			matched = true
		}
	}
	// i points at the closing ']'; consume it.
	return matched != negated, i + 1
}

// indexAfterClass returns the position immediately after the closing ']'
// when an early literal match short-circuits class evaluation.
func indexAfterClass(pattern string, start int) int {
	for start < len(pattern) && pattern[start] != ']' {
		if pattern[start] == '\\' {
			start++
		}
		start++
	}
	return start + 1
}

func nameFirstByte(name string) byte {
	if len(name) == 0 {
		return 0
	}
	return name[0]
}

type constError string

func (e constError) Error() string { return string(e) }
