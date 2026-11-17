package netsec

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	ErrPathNotUTF8     = errors.New("netsec: path is not valid UTF-8")
	ErrPathBackslash   = errors.New("netsec: path contains a backslash")
	ErrPathNUL         = errors.New("netsec: path contains a NUL byte")
	ErrPathEscapesRoot = errors.New("netsec: path escapes the install root")
	ErrPathEmpty       = errors.New("netsec: path canonicalizes to empty")
)

// canonicalizePath normalizes a raw entry path to its canonical form.
//
// Rules: the path must be valid UTF-8; backslash and NUL are rejected.
// Segments are split on '/'; empty and "." segments collapse; ".." pops the
// last segment and escaping the root is rejected. No Unicode or case folding
// is performed: distinct byte sequences remain distinct.
func canonicalizePath(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", ErrPathNotUTF8
	}
	if strings.IndexByte(raw, '\\') >= 0 {
		return "", ErrPathBackslash
	}
	if strings.IndexByte(raw, 0) >= 0 {
		return "", ErrPathNUL
	}
	var stack []string
	for _, seg := range strings.Split(raw, "/") {
		switch seg {
		case "", ".":
			// collapse
		case "..":
			if len(stack) == 0 {
				return "", ErrPathEscapesRoot
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, seg)
		}
	}
	if len(stack) == 0 {
		return "", ErrPathEmpty
	}
	return strings.Join(stack, "/"), nil
}
