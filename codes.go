package netsec

import (
	"crypto/sha256"
	"encoding/base32"
	"io"
	"strings"
)

const (
	deviceCodeBytes = 32
	userCodeBytes   = 10

	// UserCodeLength is the number of symbols in a normalized user code.
	UserCodeLength = 16

	userCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

var userCodeEncoding = base32.NewEncoding(userCodeAlphabet).WithPadding(base32.NoPadding)

// NormalizeUserCode canonicalizes a human-entered user code. It accepts lower
// case, separators, and the usual Crockford base32 substitutions (I/L for 1,
// O for 0). A code that does not have exactly UserCodeLength symbols from the
// alphabet is rejected with ErrInvalidUserCode.
func NormalizeUserCode(input string) (string, error) {
	var normalized strings.Builder
	normalized.Grow(UserCodeLength)

	for _, r := range input {
		switch r {
		case '-', ' ', '\t', '\r', '\n':
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		switch r {
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		if !strings.ContainsRune(userCodeAlphabet, r) {
			return "", ErrInvalidUserCode
		}
		normalized.WriteRune(r)
	}

	code := normalized.String()
	if len(code) != UserCodeLength {
		return "", ErrInvalidUserCode
	}
	return code, nil
}

// FormatUserCode inserts separators into a normalized user code for display.
// Invalid input is returned unchanged.
func FormatUserCode(code string) string {
	if len(code) != UserCodeLength {
		return code
	}
	return code[0:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:16]
}

func generateDeviceCode(r io.Reader) (string, error) {
	raw := make([]byte, deviceCodeBytes)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", err
	}
	return base64URLEncode(raw), nil
}

func generateUserCode(r io.Reader) (string, error) {
	raw := make([]byte, userCodeBytes)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", err
	}
	return userCodeEncoding.EncodeToString(raw), nil
}

func generateToken(r io.Reader) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", err
	}
	return base64URLEncode(raw), nil
}

func hashDeviceCode(code string) [sha256.Size]byte {
	return sha256.Sum256([]byte("device-code\x00" + code))
}

func hashUserCode(code string) [sha256.Size]byte {
	return sha256.Sum256([]byte("user-code\x00" + code))
}
