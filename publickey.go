package netsec

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

const (
	algorithmEd = "Ed"

	publicKeyRecordSize = 2 + 8 + ed25519.PublicKeySize
)

// PublicKey is a signify Ed25519 public key together with its eight-byte
// key number. The key number binds signatures to a specific key.
type PublicKey struct {
	KeyNum [8]byte
	Key    ed25519.PublicKey
}

// NewPublicKey validates and copies a raw eight-byte key number and an Ed25519
// public key.
func NewPublicKey(keyNum, key []byte) (PublicKey, error) {
	if len(keyNum) != len(PublicKey{}.KeyNum) {
		return PublicKey{}, fmt.Errorf("%w: key number has %d bytes, want 8", ErrMalformedPublicKey, len(keyNum))
	}
	if len(key) != ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("%w: public key has %d bytes, want %d", ErrMalformedPublicKey, len(key), ed25519.PublicKeySize)
	}

	var result PublicKey
	copy(result.KeyNum[:], keyNum)
	result.Key = append(ed25519.PublicKey(nil), key...)
	return result, nil
}

// ParsePublicKey parses a signify public key file. The first line is an
// untrusted comment and is ignored; the key record on the next non-empty line
// must contain "Ed", an eight-byte key number, and a 32-byte Ed25519 key.
func ParsePublicKey(data []byte) (PublicKey, error) {
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		line = bytes.TrimSpace(line)
		if len(line) == 0 || bytes.HasPrefix(line, []byte("untrusted comment:")) {
			continue
		}

		record, err := base64.StdEncoding.DecodeString(string(line))
		if err != nil {
			return PublicKey{}, fmt.Errorf("%w: decode public key: %v", ErrMalformedPublicKey, err)
		}
		if len(record) != publicKeyRecordSize {
			return PublicKey{}, fmt.Errorf("%w: decoded key has %d bytes, want %d", ErrMalformedPublicKey, len(record), publicKeyRecordSize)
		}
		if string(record[:2]) != algorithmEd {
			return PublicKey{}, fmt.Errorf("%w: algorithm is %q, want %q", ErrMalformedPublicKey, record[:2], algorithmEd)
		}
		return NewPublicKey(record[2:10], record[10:])
	}

	return PublicKey{}, fmt.Errorf("%w: no key record", ErrMalformedPublicKey)
}

// LoadPublicKey reads and parses a signify public key file.
func LoadPublicKey(path string) (PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PublicKey{}, err
	}
	return ParsePublicKey(data)
}

func validatePublicKey(key PublicKey) error {
	if len(key.Key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key has %d bytes, want %d", ErrMalformedPublicKey, len(key.Key), ed25519.PublicKeySize)
	}
	return nil
}

func keyNumString(keyNum [8]byte) string {
	parts := make([]string, len(keyNum))
	for index, value := range keyNum {
		parts[index] = fmt.Sprintf("%02x", value)
	}
	return strings.Join(parts, "")
}
