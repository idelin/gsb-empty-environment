package antismuggle

import "fmt"

func fmtSprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// isTokenByte reports whether b is a valid RFC 9110 tchar. Header field names
// must be tokens, which rules out whitespace, control characters and ':'.
func isTokenByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	default:
		switch b {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.',
			'^', '_', '`', '|', '~':
			return true
		}
	}
	return false
}

// isFieldVChar reports whether b is legal inside a (already unfolded) field
// value: SP, HTAB, VCHAR (0x21-0x7E) or obs-text (0x80-0xFF). Bare CR/LF can
// never occur here because lines are split before this check.
func isFieldVChar(b byte) bool {
	return b == '\t' || b == ' ' || b >= 0x21 && b != 0x7f
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isHexDigit(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

// trimOWS removes optional whitespace (SP/HTAB) from both ends.
func trimOWS(s []byte) []byte {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
