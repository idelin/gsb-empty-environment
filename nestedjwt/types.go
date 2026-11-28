package nestedjwt

import (
	"crypto"
	"encoding/json"
	"time"
)

// JOSEHeader is the subset of JWS/JWE protected header parameters that the
// verifier inspects. Unknown parameters are preserved in Extra so callers can
// apply additional policy checks.
type JOSEHeader struct {
	Algorithm   string         `json:"alg"`
	Encryption  string         `json:"enc,omitempty"`
	KeyID       string         `json:"kid,omitempty"`
	ContentType string         `json:"cty,omitempty"`
	Compression string         `json:"zip,omitempty"`
	Critical    []string       `json:"crit,omitempty"`
	Issuer      string         `json:"iss,omitempty"`
	Subject     string         `json:"sub,omitempty"`
	Audience    Audience       `json:"aud,omitempty"`
	Extra       map[string]any `json:"-"`
}

// Audience holds the JWT aud claim, which may be encoded either as a single
// string or as an array of strings (RFC 7519 section 4.1.3).
type Audience []string

// UnmarshalJSON accepts both the string and the array-of-strings encodings.
func (a *Audience) UnmarshalJSON(raw []byte) error {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		*a = Audience{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*a = Audience(many)
	return nil
}

// MarshalJSON preserves the compact single-string encoding when possible.
func (a Audience) MarshalJSON() ([]byte, error) {
	if len(a) == 1 {
		return json.Marshal(a[0])
	}
	return json.Marshal([]string(a))
}

// Claims is the RFC 7519 registered-claims view of an inner JWS payload. The
// Raw map retains every original claim for application-level authorization.
type Claims struct {
	Issuer         string         `json:"iss,omitempty"`
	Subject        string         `json:"sub,omitempty"`
	Audience       Audience       `json:"aud,omitempty"`
	ExpirationTime int64          `json:"exp,omitempty"`
	NotBefore      int64          `json:"nbf,omitempty"`
	IssuedAt       int64          `json:"iat,omitempty"`
	JWTID          string         `json:"jti,omitempty"`
	Raw            map[string]any `json:"-"`
}

// VerifiedToken is the successful verification result.
type VerifiedToken struct {
	Claims       Claims
	OuterHeader  JOSEHeader
	InnerHeader  JOSEHeader
	NestingDepth int
}

// KeyResolver maps a kid header to the cryptographic key required at a layer.
// JWE decryption expects *rsa.PrivateKey; JWS verification expects
// *rsa.PublicKey or *ecdsa.PublicKey.
type KeyResolver interface {
	DecryptionKey(kid string) (crypto.PrivateKey, error)
	VerificationKey(kid string) (crypto.PublicKey, error)
}

// MapKeyStore is a static, concurrency-safe KeyResolver backed by maps.
type MapKeyStore struct {
	DecryptionKeys map[string]crypto.PrivateKey
	SigningKeys    map[string]crypto.PublicKey
}

// DecryptionKey returns the registered JWE private key for kid.
func (m MapKeyStore) DecryptionKey(kid string) (crypto.PrivateKey, error) {
	key, ok := m.DecryptionKeys[kid]
	if !ok {
		return nil, ErrUnknownKey
	}
	return key, nil
}

// VerificationKey returns the registered client JWS public key for kid.
func (m MapKeyStore) VerificationKey(kid string) (crypto.PublicKey, error) {
	key, ok := m.SigningKeys[kid]
	if !ok {
		return nil, ErrUnknownKey
	}
	return key, nil
}

// Policy configures the strict nested-JWT verification rules.
type Policy struct {
	// RequireNested rejects bare 3-segment JWS submissions when true.
	RequireNested bool
	// AllowedKeyEncryption is the JWE alg whitelist.
	AllowedKeyEncryption []string
	// AllowedContentEncryption is the JWE enc whitelist.
	AllowedContentEncryption []string
	// AllowedSigning is the JWS alg whitelist. Symmetric and "none" are
	// always rejected regardless of this list.
	AllowedSigning []string
	// MaxNestedDepth caps the number of compact token layers. The RFC 7519
	// sign-then-encrypt shape has depth 2 (one JWE wrapping one JWS).
	MaxNestedDepth int
	// RequireKID forbids anonymous keys at either layer.
	RequireKID bool
	// MatchCrossLayerKID optionally requires the outer JWE kid and inner JWS
	// kid to be identical. It defaults to false because the outer kid names
	// the gateway's JWE decryption key while the inner kid names the client's
	// signing key, which are normally distinct keys.
	MatchCrossLayerKID bool
	// RequireReplayClaims makes inner iss and jti mandatory.
	RequireReplayClaims bool
	// EnableReplayStore records consumed (iss, jti) pairs.
	EnableReplayStore bool
	// ReplayTTL bounds how long replay entries are retained; zero keeps
	// entries until they are evicted by ReplayMaxEntries.
	ReplayTTL time.Duration
	// ReplayMaxEntries bounds the in-memory replay cache size.
	ReplayMaxEntries int
	// ClockSkew tolerates exp/nbf clock drift.
	ClockSkew time.Duration
	// MaxTokenSize rejects oversize compact serializations.
	MaxTokenSize int
}

// DefaultPolicy returns the hardened policy: nested JWE mandatory,
// RSA-OAEP-256 + A256GCM, ES256/RS256/PS256 signatures, depth capped at two
// layers, cty/kid/cross-layer and replay checks all enabled.
func DefaultPolicy() Policy {
	return Policy{
		RequireNested:            true,
		AllowedKeyEncryption:     []string{"RSA-OAEP-256"},
		AllowedContentEncryption: []string{"A256GCM"},
		AllowedSigning:           []string{"ES256", "RS256", "PS256"},
		MaxNestedDepth:           2,
		RequireKID:               true,
		MatchCrossLayerKID:       false,
		RequireReplayClaims:      true,
		EnableReplayStore:        true,
		ReplayTTL:                24 * time.Hour,
		ReplayMaxEntries:         100_000,
		ClockSkew:                time.Minute,
		MaxTokenSize:             64 * 1024,
	}
}

// Verifier performs strict nested-JWT unwrapping and verification.
type Verifier struct {
	policy Policy
	keys   KeyResolver
	store  ReplayStore
	now    func() time.Time
}

// Option customizes a Verifier at construction time.
type Option func(*Verifier)

// WithPolicy overrides the default hardened policy.
func WithPolicy(p Policy) Option {
	return func(v *Verifier) { v.policy = p }
}

// WithReplayStore replaces the default in-memory replay cache, for example
// with a Redis-backed distributed store implementing ReplayStore.
func WithReplayStore(store ReplayStore) Option {
	return func(v *Verifier) { v.store = store }
}

// WithClock injects a time source, primarily useful for testing.
func WithClock(now func() time.Time) Option {
	return func(v *Verifier) { v.now = now }
}

// ReplayStore is the anti-replay abstraction. Seen atomically records an
// (iss, jti) pair and reports whether it was already present.
type ReplayStore interface {
	Seen(issuer, jti string) (bool, error)
}
