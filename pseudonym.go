package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	tokenVersion = "p1"
	domainPrefix = "netsec.trace.pseudonym.v1"

	// MinKeySize is the minimum HMAC key size accepted by NewKeyring.
	MinKeySize = 32
)

var (
	// ErrEmptyTenant is returned when a tenant identifier is missing.
	ErrEmptyTenant = errors.New("netsec: tenant is required")
	// ErrEmptyFieldType is returned when a field type is missing.
	ErrEmptyFieldType = errors.New("netsec: field type is required")
	// ErrEmptyValue is returned when a value to pseudonymize is empty.
	ErrEmptyValue = errors.New("netsec: value is required")
	// ErrUnknownGeneration is returned when no key exists for a generation.
	ErrUnknownGeneration = errors.New("netsec: key generation is not configured")
	// ErrShortKey is returned when an HMAC key is weaker than MinKeySize bytes.
	ErrShortKey = errors.New("netsec: HMAC key is too short")
	// ErrInvalidToken is returned when a token is malformed.
	ErrInvalidToken = errors.New("netsec: invalid pseudonym token")
	// ErrNilKeyring is returned when a pseudonymizer has no keyring.
	ErrNilKeyring = errors.New("netsec: keyring is required")
)

// Generation identifies a version of the pseudonymization key. Generations are
// included in both the HMAC domain and the token text so that identifiers from
// different key generations cannot be confused with one another.
type Generation uint64

// String returns the decimal representation of the generation.
func (g Generation) String() string {
	return strconv.FormatUint(uint64(g), 10)
}

// TenantID is the isolation boundary for a pseudonym. The same raw value in
// two tenants always produces different tokens.
type TenantID string

// FieldType identifies the semantic type of the pseudonymized value, for
// example "email" or "phone". It is part of the HMAC domain.
type FieldType string

// Token is an opaque, stable pseudonym. Its textual form is
// "p1.<generation>.<base64url(HMAC-SHA256)>". The tenant and field type are
// cryptographically bound to the digest but are not disclosed in the token.
type Token string

// String returns the serialized token.
func (t Token) String() string {
	return string(t)
}

// Valid reports whether t is a well-formed token.
func (t Token) Valid() bool {
	_, _, err := ParseToken(string(t))
	return err == nil
}

// Generation returns the key generation carried by the token.
func (t Token) Generation() (Generation, bool) {
	_, gen, err := ParseToken(string(t))
	return gen, err == nil
}

// ParseToken validates the textual form of a token and returns its generation.
func ParseToken(s string) (Token, Generation, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] != tokenVersion || parts[1] == "" {
		return "", 0, ErrInvalidToken
	}
	if len(parts[1]) > 1 && parts[1][0] == '0' {
		return "", 0, ErrInvalidToken
	}
	gen, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return "", 0, ErrInvalidToken
	}
	digest, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(digest) != sha256.Size {
		return "", 0, ErrInvalidToken
	}
	return Token(s), Generation(gen), nil
}

// Keyring is an immutable collection of HMAC keys indexed by generation.
// Keys are copied on construction so later mutation by the caller cannot
// change pseudonymization behavior.
type Keyring struct {
	current Generation
	keys    map[Generation][]byte
}

// NewKeyring validates and copies a set of generation keys. Every key must be
// at least MinKeySize bytes and the current generation must have a key.
func NewKeyring(current Generation, keys map[Generation][]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, errors.New("netsec: keyring is empty")
	}
	copied := make(map[Generation][]byte, len(keys))
	for gen, key := range keys {
		if len(key) < MinKeySize {
			return nil, fmt.Errorf("%w: generation %s", ErrShortKey, gen)
		}
		copied[gen] = append([]byte(nil), key...)
	}
	if _, ok := copied[current]; !ok {
		return nil, fmt.Errorf("%w: generation %s", ErrUnknownGeneration, current)
	}
	return &Keyring{current: current, keys: copied}, nil
}

// Current returns the generation used by PseudonymizeCurrent.
func (k *Keyring) Current() Generation {
	if k == nil {
		return 0
	}
	return k.current
}

// Has reports whether a generation has a configured key.
func (k *Keyring) Has(gen Generation) bool {
	if k == nil {
		return false
	}
	_, ok := k.keys[gen]
	return ok
}

func (k *Keyring) key(gen Generation) ([]byte, error) {
	if k == nil {
		return nil, ErrNilKeyring
	}
	key, ok := k.keys[gen]
	if !ok {
		return nil, fmt.Errorf("%w: generation %s", ErrUnknownGeneration, gen)
	}
	return key, nil
}

// Pseudonymizer computes tenant- and field-scoped HMAC-SHA256 pseudonyms.
// It is safe for concurrent use.
type Pseudonymizer struct {
	ring *Keyring
}

// NewPseudonymizer returns a pseudonymizer backed by ring.
func NewPseudonymizer(ring *Keyring) (*Pseudonymizer, error) {
	if ring == nil {
		return nil, ErrNilKeyring
	}
	return &Pseudonymizer{ring: ring}, nil
}

// CurrentGeneration returns the key generation used by PseudonymizeCurrent.
func (p *Pseudonymizer) CurrentGeneration() Generation {
	if p == nil {
		return 0
	}
	return p.ring.Current()
}

// Pseudonymize derives a stable token from the tenant, field type, key
// generation, and raw value. The value is never retained by the component.
func (p *Pseudonymizer) Pseudonymize(tenant TenantID, field FieldType, value string, gen Generation) (Token, error) {
	if p == nil {
		return "", ErrNilKeyring
	}
	if tenant == "" {
		return "", ErrEmptyTenant
	}
	if field == "" {
		return "", ErrEmptyFieldType
	}
	if value == "" {
		return "", ErrEmptyValue
	}
	key, err := p.ring.key(gen)
	if err != nil {
		return "", err
	}

	message := domainMessage(tenant, field, gen, value)
	defer wipe(message)

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)

	return Token(tokenVersion + "." + gen.String() + "." + base64.RawURLEncoding.EncodeToString(digest)), nil
}

// PseudonymizeCurrent pseudonymizes using the keyring's current generation.
func (p *Pseudonymizer) PseudonymizeCurrent(tenant TenantID, field FieldType, value string) (Token, error) {
	if p == nil {
		return "", ErrNilKeyring
	}
	return p.Pseudonymize(tenant, field, value, p.CurrentGeneration())
}

// domainMessage uses explicit length prefixes for every variable-length
// component. This prevents delimiter-injection and concatenation ambiguities
// such as ("a", "bc") versus ("ab", "c").
func domainMessage(tenant TenantID, field FieldType, gen Generation, value string) []byte {
	buf := make([]byte, 0, len(domainPrefix)+1+len(tenant)+len(field)+len(value)+32)
	buf = append(buf, domainPrefix...)
	buf = append(buf, 0)
	buf = appendField(buf, []byte(tenant))
	buf = appendField(buf, []byte(field))

	var generation [8]byte
	binary.BigEndian.PutUint64(generation[:], uint64(gen))
	buf = appendField(buf, generation[:])
	buf = appendField(buf, []byte(value))
	return buf
}

func appendField(dst, field []byte) []byte {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(field)))
	dst = append(dst, size[:]...)
	return append(dst, field...)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
