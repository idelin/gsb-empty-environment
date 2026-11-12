package netsec

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// PublicKey is an SSH public key accepted as a trusted CA.
type PublicKey interface {
	// Type returns the SSH key type, e.g. "ssh-ed25519".
	Type() string
	// Marshal returns the canonical SSH wire encoding of the key.
	Marshal() []byte
	// Fingerprint returns the SHA256 fingerprint per RFC 4716.
	Fingerprint() string
}

type publicKey struct {
	keyType string
	wire    []byte

	ed25519 ed25519.PublicKey
	rsa     *rsa.PublicKey
	ecdsa   *ecdsa.PublicKey

	fingerprint string
}

func (k *publicKey) Type() string    { return k.keyType }
func (k *publicKey) Marshal() []byte { return append([]byte(nil), k.wire...) }

func (k *publicKey) Fingerprint() string {
	if k.fingerprint == "" {
		sum := sha256.Sum256(k.wire)
		k.fingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	}
	return k.fingerprint
}

// ParseAuthorizedKey parses a single authorized_keys line:
// "<key-type> <base64-blob> [comment]". Blank lines and lines beginning
// with '#' are rejected; callers should skip them.
func ParseAuthorizedKey(line string) (PublicKey, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil, ErrMalformedPublicKey
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, ErrMalformedPublicKey
	}
	wire, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return nil, ErrMalformedPublicKey
	}
	reader := &wireReader{full: wire}
	key, err := parseKeyBlob(reader)
	if err != nil {
		return nil, err
	}
	if reader.remaining() != 0 {
		return nil, ErrMalformedPublicKey
	}
	if key.Type() != fields[0] {
		return nil, ErrMalformedPublicKey
	}
	return key, nil
}

// EncodeAuthorizedKey renders a key as an authorized_keys line with comment.
func EncodeAuthorizedKey(key PublicKey, comment string) string {
	line := key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal())
	if comment != "" {
		line += " " + comment
	}
	return line
}
