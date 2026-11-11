package jwsbridge

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// AlgorithmEdDSA is the only accepted JWS algorithm.
	AlgorithmEdDSA = "EdDSA"

	// KeyUseSign marks keys and tokens intended for signing.
	KeyUseSign = "sig"
	// KeyUseEncrypt marks keys intended for encryption; they must never
	// be accepted for signature verification.
	KeyUseEncrypt = "enc"
)

var (
	ErrMalformedToken = errors.New("jwsbridge: malformed token")
	ErrAlgorithm      = errors.New("jwsbridge: unexpected signing algorithm")
	ErrKeyUse         = errors.New("jwsbridge: key usage not permitted for signing")
	ErrSignature      = errors.New("jwsbridge: signature verification failed")
)

// Header carries the JWS metadata used to route verification to the
// correct tenant key and to enforce key usage.
type Header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Use string `json:"use"`
}

// Claims is the claim set understood by the broker.
type Claims struct {
	Issuer    string    `json:"iss"`
	Subject   string    `json:"sub"`
	Audience  []string  `json:"aud"`
	Scopes    []string  `json:"scope,omitempty"`
	Tenant    string    `json:"tenant"`
	ID        string    `json:"jti"`
	ExpiresAt time.Time `json:"exp"`
}

// HasAudience reports whether aud is present in the claim set.
func (c Claims) HasAudience(aud string) bool {
	for _, a := range c.Audience {
		if a == aud {
			return true
		}
	}
	return false
}

// Signer is a local signing party holding one Ed25519 key of a specific
// tenant, key id, usage and rotation generation.
type Signer struct {
	Tenant     string
	KeyID      string
	Use        string
	Generation int
	key        ed25519.PrivateKey
}

// NewSigner generates a fresh Ed25519 key for the given tenant/kid/use/gen.
func NewSigner(tenant, kid, use string, generation int) (*Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("jwsbridge: generate key: %w", err)
	}
	return &Signer{Tenant: tenant, KeyID: kid, Use: use, Generation: generation, key: priv}, nil
}

// Record exports the public half of the signer as a KeyRecord.
func (s *Signer) Record() KeyRecord {
	pub, ok := s.key.Public().(ed25519.PublicKey)
	if !ok {
		panic("jwsbridge: unexpected key type")
	}
	return KeyRecord{
		KeyID:      s.KeyID,
		Use:        s.Use,
		Generation: s.Generation,
		Public:     append(ed25519.PublicKey(nil), pub...),
	}
}

// Sign serializes and signs the claims, stamping tenant/kid/use metadata
// from the signer into the protected header.
func (s *Signer) Sign(claims Claims) (string, error) {
	header := Header{Alg: AlgorithmEdDSA, Kid: s.KeyID, Use: s.Use}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claims.Tenant = s.Tenant
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(headerJSON) + "." + enc.EncodeToString(claimsJSON)
	sig := ed25519.Sign(s.key, []byte(input))
	return input + "." + enc.EncodeToString(sig), nil
}

// parseToken splits and decodes a compact JWS without verifying it.
func parseToken(token string) (Header, Claims, []byte, []byte, error) {
	var header Header
	var claims Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return header, claims, nil, nil, ErrMalformedToken
	}
	enc := base64.RawURLEncoding
	headerJSON, err := enc.DecodeString(parts[0])
	if err != nil {
		return header, claims, nil, nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return header, claims, nil, nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	claimsJSON, err := enc.DecodeString(parts[1])
	if err != nil {
		return header, claims, nil, nil, fmt.Errorf("%w: claims: %v", ErrMalformedToken, err)
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return header, claims, nil, nil, fmt.Errorf("%w: claims: %v", ErrMalformedToken, err)
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return header, claims, nil, nil, fmt.Errorf("%w: signature: %v", ErrMalformedToken, err)
	}
	input := parts[0] + "." + parts[1]
	return header, claims, []byte(input), sig, nil
}
