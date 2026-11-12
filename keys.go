package netsec

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"crypto/ed25519"
)

// Key is an Ed25519 public key with the identifier used in JWT issuer,
// subject, and signing_keys fields. If ID is empty, PublicKeyID(PublicKey) is
// used.
type Key struct {
	ID        string
	PublicKey ed25519.PublicKey
}

// NewKey returns a Key whose identifier is the URL-safe base64 encoding of the
// public key.
func NewKey(publicKey ed25519.PublicKey) Key {
	return Key{ID: PublicKeyID(publicKey), PublicKey: publicKey}
}

// NewKeyWithID returns a Key with an explicit identifier. NATS nkey
// identifiers, base64, and hex encodings are all accepted by key resolution.
func NewKeyWithID(id string, publicKey ed25519.PublicKey) Key {
	return Key{ID: id, PublicKey: publicKey}
}

// PublicKeyID returns a stable, URL-safe base64 key identifier. The value is
// directly accepted anywhere this package expects a key identifier.
func PublicKeyID(publicKey ed25519.PublicKey) string {
	if len(publicKey) != ed25519.PublicKeySize {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(publicKey)
}

// ParsePublicKeyID resolves a key identifier to an Ed25519 public key.
//
// It accepts URL-safe or standard base64, hexadecimal strings, and NATS public
// nkeys. Key identifiers may also be arbitrary application names, but those
// must be registered with Authenticator.RegisterKey.
func ParsePublicKeyID(id string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty identifier", ErrInvalidKey)
	}

	if len(trimmed) == hex.EncodedLen(ed25519.PublicKeySize) && isHex(trimmed) {
		if raw, err := hex.DecodeString(trimmed); err == nil && len(raw) == ed25519.PublicKeySize {
			return ed25519.PublicKey(raw), nil
		}
	}

	for _, encoding := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		if raw, err := encoding.DecodeString(trimmed); err == nil && len(raw) == ed25519.PublicKeySize {
			return ed25519.PublicKey(raw), nil
		}
	}

	if publicKey, ok := decodeNKeyPublic(trimmed); ok {
		return publicKey, nil
	}

	return nil, fmt.Errorf("%w: unrecognised identifier %q", ErrInvalidKey, id)
}

func isHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func decodeNKeyPublic(id string) (ed25519.PublicKey, bool) {
	upper := strings.ToUpper(strings.TrimSpace(id))
	encodings := []*base32.Encoding{
		base32.StdEncoding.WithPadding(base32.NoPadding),
		base32.StdEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(upper)
		if err != nil {
			continue
		}
		// Public nkeys contain a one-byte prefix, the 32-byte Ed25519 key, and
		// a two-byte checksum. The checksum is not needed to resolve the key.
		if len(decoded) == 1+ed25519.PublicKeySize+2 {
			return ed25519.PublicKey(decoded[1 : 1+ed25519.PublicKeySize]), true
		}
	}
	return nil, false
}

func validPublicKey(publicKey ed25519.PublicKey) bool {
	return len(publicKey) == ed25519.PublicKeySize
}
