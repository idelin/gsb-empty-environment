package netsec

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxHeaderLine       = 78
	maxEncodedWord      = 75
	encodedWordOverhead = len("=?UTF-8?B??=")
)

// encodePhrase converts a raw display name into one or more RFC 2047
// encoded-words. Short printable ASCII names stay quoted, which is easier for
// humans to read; everything else is B-encoded at rune boundaries so no
// encoded-word exceeds the 75-character limit.
func encodePhrase(name string) []string {
	if isShortPrintableASCII(name) {
		return []string{quotePhrase(name)}
	}
	return encodeWords(name)
}

func isShortPrintableASCII(value string) bool {
	if len(value) > 60 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func quotePhrase(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\', '"':
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}

func encodeWords(value string) []string {
	const maxRaw = (maxEncodedWord-encodedWordOverhead)*3/4 - 2
	words := make([]string, 0, len(value)/maxRaw+1)
	for len(value) > 0 {
		size := maxRaw
		if size > len(value) {
			size = len(value)
		}
		for size > 0 && size < len(value) && !utf8.RuneStart(value[size]) {
			size--
		}
		if size == 0 {
			_, size = utf8.DecodeRuneInString(value)
			if size == 0 {
				break
			}
		}
		chunk := value[:size]
		value = value[size:]
		words = append(words, "=?UTF-8?B?"+base64.StdEncoding.EncodeToString([]byte(chunk))+"?=")
	}
	return words
}

func encodeUnstructured(value string) ([]string, error) {
	if err := validateText(value, "header text"); err != nil {
		return nil, err
	}
	if isShortPrintableASCII(value) {
		return []string{value}, nil
	}
	return encodeWords(value), nil
}

func validateASCIIHeaderToken(value string) error {
	if value == "" {
		return fmt.Errorf("%w: empty header token", ErrInvalidHeader)
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return fmt.Errorf("%w: header token contains a control or non-ASCII byte", ErrHeaderInjection)
		}
	}
	return nil
}
