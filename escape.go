// Package stresc encodes arbitrary Unicode text into a custom
// double-quoted string format and decodes it back.
//
// Format: text is wrapped in double quotes. Backslash and double quote
// are escaped as \\ and \". The usual C-style escapes \a \b \f \n \r
// \t \v are used, other control characters become \xNN. Decoders also
// accept \xNN (ASCII only) and \uXXXX with UTF-16 surrogate pairs.
// All error offsets are byte offsets into the original input.
package stresc

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// errIncomplete signals that more input bytes are needed to decide
// whether the current construct is valid. It never escapes the package.
var errIncomplete = errors.New("stresc: incomplete input")

// Error describes an encode or decode failure. Offset is a byte offset
// into the original input.
type Error struct {
	Offset int
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("stresc: offset %d: %s", e.Offset, e.Msg)
}

const hexdig = "0123456789abcdef"

// Encode appends the quoted encoding of s to dst and returns the
// extended buffer.
func Encode(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for _, r := range s {
		dst = encodeRune(dst, r)
	}
	return append(dst, '"')
}

func encodeRune(dst []byte, r rune) []byte {
	switch r {
	case '"':
		return append(dst, '\\', '"')
	case '\\':
		return append(dst, '\\', '\\')
	case '\a':
		return append(dst, '\\', 'a')
	case '\b':
		return append(dst, '\\', 'b')
	case '\f':
		return append(dst, '\\', 'f')
	case '\n':
		return append(dst, '\\', 'n')
	case '\r':
		return append(dst, '\\', 'r')
	case '\t':
		return append(dst, '\\', 't')
	case '\v':
		return append(dst, '\\', 'v')
	}
	if r < 0x20 || r == 0x7f {
		return append(dst, '\\', 'x', hexdig[r>>4], hexdig[r&0xf])
	}
	return utf8.AppendRune(dst, r)
}

// Decode parses a single quoted string at the start of src and returns
// the decoded text plus the number of input bytes consumed, including
// both quotes. Errors carry byte offsets into src.
func Decode(src []byte) (string, int, error) {
	v, n, err := parseQuoted(src)
	if err == errIncomplete {
		return "", 0, &Error{Offset: len(src), Msg: "unexpected end of input"}
	}
	if err != nil {
		return "", 0, err
	}
	return v, n, nil
}

// parseQuoted parses a quoted string starting at src[0]. It returns the
// decoded value and the number of bytes consumed, errIncomplete if more
// input is needed, or an *Error with an offset relative to src.
func parseQuoted(src []byte) (string, int, error) {
	if len(src) == 0 {
		return "", 0, errIncomplete
	}
	if src[0] != '"' {
		return "", 0, &Error{Offset: 0, Msg: "expected opening quote"}
	}
	var out []byte
	i := 1
	for {
		if i >= len(src) {
			return "", 0, errIncomplete
		}
		c := src[i]
		switch {
		case c == '"':
			return string(out), i + 1, nil
		case c == '\\':
			var err error
			out, i, err = parseEscape(src, i, out)
			if err != nil {
				return "", 0, err
			}
		case c < 0x20 || c == 0x7f:
			return "", 0, &Error{Offset: i, Msg: "unescaped control character"}
		case c < 0x80:
			out = append(out, c)
			i++
		default:
			if !utf8.FullRune(src[i:]) {
				return "", 0, errIncomplete
			}
			r, size := utf8.DecodeRune(src[i:])
			if r == utf8.RuneError && size == 1 {
				if isSurrogateEncoding(src[i:]) {
					return "", 0, &Error{Offset: i, Msg: "isolated surrogate in UTF-8 input"}
				}
				return "", 0, &Error{Offset: i, Msg: "invalid UTF-8"}
			}
			out = append(out, src[i:i+size]...)
			i += size
		}
	}
}

// isSurrogateEncoding reports whether b starts with the UTF-8 encoding
// of a UTF-16 surrogate code point (U+D800..U+DFFF).
func isSurrogateEncoding(b []byte) bool {
	return len(b) >= 3 && b[0] == 0xED && b[1] >= 0xA0 && b[1] <= 0xBF
}

// parseEscape parses the escape sequence starting at src[i], where
// src[i] == '\\', appends the decoded bytes to out and returns the
// index just past the escape.
func parseEscape(src []byte, i int, out []byte) ([]byte, int, error) {
	if i+1 >= len(src) {
		return nil, 0, errIncomplete
	}
	switch e := src[i+1]; e {
	case '\\':
		return append(out, '\\'), i + 2, nil
	case '"':
		return append(out, '"'), i + 2, nil
	case 'a':
		return append(out, '\a'), i + 2, nil
	case 'b':
		return append(out, '\b'), i + 2, nil
	case 'f':
		return append(out, '\f'), i + 2, nil
	case 'n':
		return append(out, '\n'), i + 2, nil
	case 'r':
		return append(out, '\r'), i + 2, nil
	case 't':
		return append(out, '\t'), i + 2, nil
	case 'v':
		return append(out, '\v'), i + 2, nil
	case 'x':
		v, err := hexValue(src, i+2, 2)
		if err != nil {
			return nil, 0, err
		}
		if v >= 0x80 {
			return nil, 0, &Error{Offset: i, Msg: "non-ASCII \\x escape"}
		}
		return append(out, byte(v)), i + 4, nil
	case 'u':
		v, err := hexValue(src, i+2, 4)
		if err != nil {
			return nil, 0, err
		}
		switch {
		case v >= 0xDC00 && v <= 0xDFFF:
			return nil, 0, &Error{Offset: i, Msg: "isolated low surrogate"}
		case v >= 0xD800 && v <= 0xDBFF:
			// A high surrogate must be followed immediately by a
			// \uXXXX low surrogate, possibly in a later chunk.
			j := i + 6
			if j >= len(src) {
				return nil, 0, errIncomplete
			}
			if src[j] != '\\' {
				return nil, 0, &Error{Offset: i, Msg: "lone high surrogate"}
			}
			if j+1 >= len(src) {
				return nil, 0, errIncomplete
			}
			if src[j+1] != 'u' {
				return nil, 0, &Error{Offset: i, Msg: "lone high surrogate"}
			}
			lo, err := hexValue(src, j+2, 4)
			if err != nil {
				return nil, 0, err
			}
			if lo < 0xDC00 || lo > 0xDFFF {
				return nil, 0, &Error{Offset: i, Msg: "lone high surrogate"}
			}
			r := utf16.DecodeRune(rune(v), rune(lo))
			return utf8.AppendRune(out, r), j + 6, nil
		default:
			return utf8.AppendRune(out, rune(v)), i + 6, nil
		}
	default:
		return nil, 0, &Error{Offset: i + 1, Msg: fmt.Sprintf("unknown escape \\%c", e)}
	}
}

// hexValue reads n hex digits starting at src[off]. It returns
// errIncomplete if src ends first, or an *Error at the first digit
// that is not valid hexadecimal.
func hexValue(src []byte, off, n int) (int, error) {
	v := 0
	for j := 0; j < n; j++ {
		if off+j >= len(src) {
			return 0, errIncomplete
		}
		c := src[off+j]
		if !isHex(c) {
			return 0, &Error{Offset: off + j, Msg: "invalid hex digit"}
		}
		v = v<<4 | hexVal(c)
	}
	return v, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// escapeLen reports the length of the legal escape sequence beginning
// at buf[i] (buf[i] == '\\'), or 0 if the bytes do not form a legal
// escape. ok is false when more input is needed to decide.
func escapeLen(buf []byte, i int) (n int, ok bool) {
	if i+1 >= len(buf) {
		return 0, false
	}
	switch buf[i+1] {
	case '\\', '"', 'a', 'b', 'f', 'n', 'r', 't', 'v':
		return 2, true
	case 'x':
		return hexEscapeLen(buf, i, 2, 4)
	case 'u':
		return hexEscapeLen(buf, i, 4, 6)
	}
	return 0, true
}

func hexEscapeLen(buf []byte, i, digits, total int) (int, bool) {
	for j := 0; j < digits; j++ {
		if i+2+j >= len(buf) {
			return 0, false
		}
		if !isHex(buf[i+2+j]) {
			return 0, true
		}
	}
	if digits == 2 {
		v := hexVal(buf[i+2])<<4 | hexVal(buf[i+3])
		if v >= 0x80 {
			return 0, true
		}
	}
	return total, true
}
