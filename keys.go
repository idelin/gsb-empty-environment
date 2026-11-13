package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
)

// KeySet holds the public keys of a single issuer. A KeySet never mixes keys
// from different issuers: each Verifier owns its own, so a key cached for one
// issuer can never validate a token whose iss names another issuer.
type KeySet struct {
	mu   sync.RWMutex
	keys []jsonWebKey
}

type jsonWebKey struct {
	kid    string
	alg    string
	kty    string
	crv    string
	use    string
	pubKey crypto.PublicKey
}

// jwkDocument is the on-the-wire JWKS representation (RFC 7517).
type jwkDocument struct {
	Keys []rawJWK `json:"keys"`
}

type rawJWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// ParseKeySet decodes a JWKS document. Keys marked "use":"enc" are ignored.
func ParseKeySet(data []byte) (*KeySet, error) {
	var doc jwkDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("netsec: invalid JWKS: %w", err)
	}
	set := &KeySet{}
	for i := range doc.Keys {
		jwk, err := convertJWK(&doc.Keys[i])
		if err != nil {
			return nil, err
		}
		if jwk.use == "enc" {
			continue
		}
		set.keys = append(set.keys, jwk)
	}
	if len(set.keys) == 0 {
		return nil, errors.New("netsec: JWKS contains no usable keys")
	}
	return set, nil
}

// AddKey registers an already-parsed public key under kid for an algorithm
// family (for example "RS256" or "ES256"). It is intended for pinned/static
// keys and tests.
func (s *KeySet) AddKey(kid, alg string, pub crypto.PublicKey) error {
	if pub == nil {
		return errors.New("netsec: AddKey requires a non-nil key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, jsonWebKey{kid: kid, alg: alg, pubKey: pub})
	return nil
}

// Lookup returns the key matching kid (and, when present on the JWK, the
// algorithm family). nil means no key of this issuer matches.
func (s *KeySet) Lookup(kid, alg string) crypto.PublicKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.keys {
		jwk := &s.keys[i]
		if jwk.kid != kid {
			continue
		}
		if jwk.alg != "" && jwk.alg != alg {
			continue
		}
		return jwk.pubKey
	}
	return nil
}

// Len reports how many keys the set holds.
func (s *KeySet) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keys)
}

func convertJWK(raw *rawJWK) (jsonWebKey, error) {
	jwk := jsonWebKey{kid: raw.Kid, alg: raw.Alg, kty: raw.Kty, crv: raw.Crv, use: raw.Use}
	switch raw.Kty {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(raw.N)
		if err != nil {
			return jwk, fmt.Errorf("netsec: RSA key %q: bad modulus: %w", raw.Kid, err)
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(raw.E)
		if err != nil {
			return jwk, fmt.Errorf("netsec: RSA key %q: bad exponent: %w", raw.Kid, err)
		}
		exponent := 0
		for _, b := range eBytes {
			exponent = exponent<<8 + int(b)
		}
		jwk.pubKey = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: exponent,
		}
	case "EC":
		xBytes, err := base64.RawURLEncoding.DecodeString(raw.X)
		if err != nil {
			return jwk, fmt.Errorf("netsec: EC key %q: bad x: %w", raw.Kid, err)
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(raw.Y)
		if err != nil {
			return jwk, fmt.Errorf("netsec: EC key %q: bad y: %w", raw.Kid, err)
		}
		curve, err := curveForName(raw.Crv)
		if err != nil {
			return jwk, fmt.Errorf("netsec: EC key %q: %w", raw.Kid, err)
		}
		jwk.pubKey = &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}
	case "OKP":
		if raw.Crv != "Ed25519" {
			return jwk, fmt.Errorf("netsec: OKP key %q: unsupported curve %q", raw.Kid, raw.Crv)
		}
		rawKey, err := base64.RawURLEncoding.DecodeString(raw.X)
		if err != nil {
			return jwk, fmt.Errorf("netsec: OKP key %q: bad x: %w", raw.Kid, err)
		}
		if len(rawKey) != ed25519.PublicKeySize {
			return jwk, fmt.Errorf("netsec: OKP key %q: wrong key length", raw.Kid)
		}
		jwk.pubKey = ed25519.PublicKey(rawKey)
	default:
		return jwk, fmt.Errorf("netsec: unsupported JWK key type %q", raw.Kty)
	}
	return jwk, nil
}

func curveForName(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported curve %q", name)
	}
}
