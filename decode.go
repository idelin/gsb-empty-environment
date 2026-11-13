package netsec

import (
	"errors"
	"unicode/utf8"
)

// ErrInvalidUTF8 is returned when input contains a sequence that Go's
// unicode/utf8 decoder considers invalid.
var ErrInvalidUTF8 = errors.New("netsec: invalid UTF-8")

// DecodeRune decodes the first UTF-8 encoding in p using Go's decoder.
//
// A CESU-8 surrogate is not valid UTF-8. DecodeRune consumes one byte and
// returns utf8.RuneError for it rather than combining a high surrogate with
// an adjacent low surrogate. A genuine four-byte supplementary character is
// decoded normally.
func DecodeRune(p []byte) (rune, int) {
	return utf8.DecodeRune(p)
}

// DecodeRuneInString is the string form of DecodeRune.
func DecodeRuneInString(s string) (rune, int) {
	return utf8.DecodeRuneInString(s)
}

// DecodeBytes decodes b strictly as UTF-8 and returns the decoded runes.
//
// Invalid bytes are represented by utf8.RuneError. This deliberately follows
// unicode/utf8 semantics: CESU-8 surrogate triples are invalid, are consumed
// one byte at a time, and are never joined into a supplementary character.
func DecodeBytes(b []byte) []rune {
	runes := make([]rune, 0, len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		runes = append(runes, r)
		b = b[size:]
	}
	return runes
}

// Decode is an alias for DecodeBytes.
func Decode(b []byte) []rune {
	return DecodeBytes(b)
}

// DecodeUTF8 is an alias for DecodeBytes.
func DecodeUTF8(b []byte) []rune {
	return DecodeBytes(b)
}

// DecodeRunes is an alias for DecodeBytes.
func DecodeRunes(b []byte) []rune {
	return DecodeBytes(b)
}

// DecodeString decodes s strictly as UTF-8.
func DecodeString(s string) []rune {
	return DecodeBytes([]byte(s))
}

// Valid reports whether b is well-formed UTF-8 according to Go's decoder.
//
// In particular, CESU-8 encoded surrogate pairs and lone surrogate encodings
// are invalid, while genuine four-byte supplementary characters are valid.
func Valid(b []byte) bool {
	return utf8.Valid(b)
}

// ValidString reports whether s is well-formed UTF-8.
func ValidString(s string) bool {
	return utf8.ValidString(s)
}

// IsValid is an alias for Valid.
func IsValid(b []byte) bool {
	return Valid(b)
}

// IsValidUTF8 is an alias for Valid.
func IsValidUTF8(b []byte) bool {
	return Valid(b)
}

// ValidUTF8 is an alias for Valid.
func ValidUTF8(b []byte) bool {
	return Valid(b)
}

// IsValidString is an alias for ValidString.
func IsValidString(s string) bool {
	return ValidString(s)
}

// Validate returns ErrInvalidUTF8 if b is not well-formed UTF-8.
func Validate(b []byte) error {
	if Valid(b) {
		return nil
	}
	return ErrInvalidUTF8
}

// ValidateUTF8 is an alias for Validate.
func ValidateUTF8(b []byte) error {
	return Validate(b)
}

// ValidateString returns ErrInvalidUTF8 if s is not well-formed UTF-8.
func ValidateString(s string) error {
	if ValidString(s) {
		return nil
	}
	return ErrInvalidUTF8
}

// DecodeStrict validates b and returns its bytes as a string only when the
// entire input is well-formed UTF-8.
func DecodeStrict(b []byte) (string, error) {
	if err := Validate(b); err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeStringStrict is the string form of DecodeStrict.
func DecodeStringStrict(s string) (string, error) {
	if err := ValidateString(s); err != nil {
		return "", err
	}
	return s, nil
}

// Sanitize replaces each invalid UTF-8 byte with utf8.RuneError. Valid
// sequences, including four-byte supplementary characters, are preserved.
func Sanitize(b []byte) []byte {
	if Valid(b) {
		return append([]byte(nil), b...)
	}

	out := make([]byte, 0, len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			out = utf8.AppendRune(out, utf8.RuneError)
			b = b[1:]
			continue
		}
		out = append(out, b[:size]...)
		b = b[size:]
	}
	return out
}

// SanitizeString replaces each invalid UTF-8 byte in s with utf8.RuneError.
func SanitizeString(s string) string {
	return string(Sanitize([]byte(s)))
}

// ContainsCESU8 reports whether b contains a three-byte encoding of a UTF-16
// surrogate (U+D800 through U+DFFF), as used by CESU-8. It does not combine
// adjacent surrogates.
func ContainsCESU8(b []byte) bool {
	for i := 0; i+2 < len(b); i++ {
		if b[i] != 0xED || b[i+1] < 0xA0 || b[i+1] > 0xBF {
			continue
		}
		if b[i+2] >= 0x80 && b[i+2] <= 0xBF {
			return true
		}
	}
	return false
}

// IsCESU8 is an alias for ContainsCESU8.
func IsCESU8(b []byte) bool {
	return ContainsCESU8(b)
}

// DetectCESU8 is an alias for ContainsCESU8.
func DetectCESU8(b []byte) bool {
	return ContainsCESU8(b)
}

// IsCESU8Pair reports whether b begins with a high-surrogate CESU-8 encoding
// immediately followed by a low-surrogate CESU-8 encoding.
func IsCESU8Pair(b []byte) bool {
	if len(b) < 6 {
		return false
	}
	return isSurrogateEncoding(b[:3], 0xD800, 0xDBFF) &&
		isSurrogateEncoding(b[3:6], 0xDC00, 0xDFFF)
}

func isSurrogateEncoding(b []byte, lo, hi rune) bool {
	if len(b) < 3 || b[0] != 0xED || b[1] < 0xA0 || b[1] > 0xBF || b[2] < 0x80 || b[2] > 0xBF {
		return false
	}
	r := 0x0D<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	return r >= lo && r <= hi
}
