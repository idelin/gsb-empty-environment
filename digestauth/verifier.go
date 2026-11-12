package digestauth

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
)

// ParseNC parses the digest nc parameter: exactly eight lowercase or upper
// case hexadecimal digits per RFC 2617, e.g. "00000001".
func ParseNC(raw string) (uint32, error) {
	if len(raw) != 8 {
		return 0, fmt.Errorf("%w: nc must be 8 hex digits", ErrInvalidCount)
	}
	value, err := hex.DecodeString(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: nc is not hexadecimal", ErrInvalidCount)
	}
	nc := uint32(value[0])<<24 | uint32(value[1])<<16 |
		uint32(value[2])<<8 | uint32(value[3])
	if nc == 0 {
		return 0, ErrInvalidCount
	}
	return nc, nil
}

// HA1Lookup resolves the precomputed HA1 digest for a user/realm pair. It
// typically wraps a credential store lookup. Return an error for unknown users.
type HA1Lookup func(username, realm string) (ha1 string, err error)

// RFC2617Verifier confirms the response signature according to RFC 2617,
// including username, realm, method, request target and, for qop=auth-int, the
// entity-body digest.
type RFC2617Verifier struct {
	lookup HA1Lookup
}

// NewRFC2617Verifier builds a verifier backed by an HA1 credential lookup.
func NewRFC2617Verifier(lookup HA1Lookup) *RFC2617Verifier {
	return &RFC2617Verifier{lookup: lookup}
}

// Verify implements Verifier.
func (v *RFC2617Verifier) Verify(_ context.Context, e Evidence) error {
	if v == nil || v.lookup == nil {
		return errors.New("digestauth: verifier has no credential lookup")
	}
	ha1, err := v.lookup(e.Username, e.Realm)
	if err != nil {
		return fmt.Errorf("credential lookup failed: %w", err)
	}

	hasher := md5.New
	if strings.EqualFold(e.Algorithm, "SHA-256") {
		hasher = sha256.New
	}

	ha2 := computeHA2(hasher, e)

	var want string
	switch {
	case e.QOP == "auth" || e.QOP == "auth-int":
		want = digest(hasher, ha1, e.Nonce,
			fmt.Sprintf("%08x", e.NC), e.CNonce, e.QOP, ha2)
	default:
		// Legacy form without quality of protection.
		want = digest(hasher, ha1, e.Nonce, ha2)
	}

	if subtle.ConstantTimeCompare([]byte(want), []byte(e.Response)) != 1 {
		return errors.New("client response does not match expected digest")
	}
	return nil
}

func computeHA2(hasher func() hash.Hash, e Evidence) string {
	if e.QOP == "auth-int" {
		return digest(hasher, e.Method, e.URI, e.BodySHA)
	}
	return digest(hasher, e.Method, e.URI)
}

func digest(hasher func() hash.Hash, parts ...string) string {
	h := hasher()
	h.Write([]byte(strings.Join(parts, ":")))
	return hex.EncodeToString(h.Sum(nil))
}
