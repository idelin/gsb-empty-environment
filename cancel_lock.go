package netsec

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

type cancelElement struct {
	scheme string
	value  string
}

func parseCancelElements(values []string) ([]cancelElement, error) {
	var elements []cancelElement
	for _, value := range values {
		tokens, err := splitCFWS(value)
		if err != nil {
			return nil, err
		}
		for _, token := range tokens {
			colon := strings.IndexByte(token, ':')
			if colon <= 0 || colon == len(token)-1 {
				return nil, fmt.Errorf("invalid cancel element %q", token)
			}
			elements = append(elements, cancelElement{
				scheme: strings.ToLower(token[:colon]),
				value:  token[colon+1:],
			})
		}
	}
	return elements, nil
}

func verifyCancelKeys(keys, locks []cancelElement) bool {
	var matched int
	for _, key := range keys {
		for _, lock := range locks {
			if key.scheme != lock.scheme {
				continue
			}
			if verifyCancelKey(key, lock) {
				matched = 1
			}
		}
	}
	return matched == 1
}

// verifyCancelKey implements RFC 8315 section 2.1: the lock is the Base64
// encoding of the hash of the Base64-encoded key string (the literal bytes of
// <c-key-string>), not of its decoded octets.
func verifyCancelKey(key, lock cancelElement) bool {
	var digest []byte
	switch key.scheme {
	case "sha256":
		sum := sha256.Sum256([]byte(key.value))
		digest = sum[:]
	case "sha512":
		sum := sha512.Sum512([]byte(key.value))
		digest = sum[:]
	default:
		return false
	}

	decoded, err := decodeBase64(lock.value)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(digest, decoded) == 1
}

func decodeBase64(value string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	// RFC 8315 requires accepting obsolete key strings without padding. A
	// corresponding lock is still normally padded, but raw Base64 is harmless
	// to accept for interoperability.
	return base64.RawStdEncoding.DecodeString(value)
}

// splitCFWS splits a header field body into elements while accepting RFC 5322
// comments. Comments are ignored; elements are returned without surrounding
// folding whitespace.
func splitCFWS(value string) ([]string, error) {
	var tokens []string
	var token strings.Builder
	depth := 0
	escaped := false

	for i := 0; i < len(value); i++ {
		c := value[i]
		if depth > 0 {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '(':
				depth++
			case c == ')':
				depth--
			}
			continue
		}
		switch c {
		case '(', ')':
			if c == ')' {
				return nil, errors.New("unmatched ')' in header value")
			}
			if token.Len() > 0 {
				tokens = append(tokens, token.String())
				token.Reset()
			}
			depth = 1
		case ' ', '\t':
			if token.Len() > 0 {
				tokens = append(tokens, token.String())
				token.Reset()
			}
		default:
			token.WriteByte(c)
		}
	}
	if depth != 0 {
		return nil, errors.New("unmatched '(' in header value")
	}
	if token.Len() > 0 {
		tokens = append(tokens, token.String())
	}
	return tokens, nil
}
