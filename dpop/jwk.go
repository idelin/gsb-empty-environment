package dpop

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

// JWK is a public JSON Web Key. Private and symmetric members are never
// accepted: UnmarshalJSON rejects them explicitly.
type JWK struct {
	// KeyType is the JWK "kty" parameter ("EC" or "OKP").
	KeyType string `json:"kty"`
	// Use is the optional "use" parameter.
	Use string `json:"use,omitempty"`
	// Algorithm is the optional "alg" parameter.
	Algorithm string `json:"alg,omitempty"`
	// KeyID is the optional "kid" parameter.
	KeyID string `json:"kid,omitempty"`
	// Curve is the "crv" parameter ("P-256" or "Ed25519").
	Curve string `json:"crv,omitempty"`
	// X is the base64url-no-padding "x" coordinate / public key.
	X string `json:"x,omitempty"`
	// Y is the base64url-no-padding "y" coordinate (EC only).
	Y string `json:"y,omitempty"`
}

// forbiddenJWKMembers are private or symmetric key parameters. A proof JWK
// must only contain public key material.
var forbiddenJWKMembers = map[string]bool{
	"d": true, // EC / OKP private exponent
	"k": true, // symmetric key value
	"p": true, "q": true, "dp": true, "dq": true, "qi": true,
	"oth": true, // RSA private / multi-prime parameters
}

// UnmarshalJSON decodes a JWK while rejecting duplicate JSON object members
// and any private or symmetric key parameters.
func (j *JWK) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := unmarshalUnique(data, &raw); err != nil {
		return fmt.Errorf("jwk: %w", err)
	}
	for member := range raw {
		if forbidden, ok := forbiddenJWKMembers[member]; ok && forbidden {
			return newError(ErrInvalidDPoP, "DPoP JWK must not contain private/symmetric key parameter "+member)
		}
	}

	// Decode into the struct through an alias type to avoid recursion.
	type plainJWK JWK
	var alias plainJWK
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&alias); err != nil {
		return fmt.Errorf("jwk: malformed JSON: %w", err)
	}
	*j = JWK(alias)
	return nil
}

var b64 = base64.RawURLEncoding

// Thumbprint returns the canonical RFC 7638 SHA-256 JWK thumbprint, encoded
// as base64url without padding, which is the representation used by the
// access token cnf.jkt member.
func (j *JWK) Thumbprint() (string, error) {
	var canonical []byte
	var err error

	switch j.KeyType {
	case "EC":
		if j.Curve != "P-256" || j.X == "" || j.Y == "" {
			return "", newError(ErrInvalidDPoP, "DPoP EC JWK must use P-256 and contain x and y")
		}
		canonical, err = json.Marshal(map[string]string{
			"crv": j.Curve,
			"kty": j.KeyType,
			"x":   j.X,
			"y":   j.Y,
		})
	case "OKP":
		if j.Curve != "Ed25519" || j.X == "" {
			return "", newError(ErrInvalidDPoP, "DPoP OKP JWK must use Ed25519 and contain x")
		}
		canonical, err = json.Marshal(map[string]string{
			"crv": j.Curve,
			"kty": j.KeyType,
			"x":   j.X,
		})
	default:
		return "", newError(ErrInvalidDPoP, "DPoP JWK must use an asymmetric EC P-256 or OKP Ed25519 key")
	}
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(canonical)
	return b64.EncodeToString(sum[:]), nil
}

// PublicKey converts the JWK into a Go public key suitable for signature
// verification, after validating the encoded coordinates.
func (j *JWK) PublicKey() (interface{}, error) {
	switch j.KeyType {
	case "EC":
		if j.Curve != "P-256" {
			return nil, newError(ErrInvalidDPoP, "only P-256 EC keys are supported")
		}
		xBytes, err := decodeJWKField("x", j.X, 32)
		if err != nil {
			return nil, err
		}
		yBytes, err := decodeJWKField("y", j.Y, 32)
		if err != nil {
			return nil, err
		}
		curve := elliptic.P256()
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !curve.IsOnCurve(x, y) {
			return nil, newError(ErrInvalidDPoP, "DPoP EC JWK coordinates are not on P-256")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil

	case "OKP":
		if j.Curve != "Ed25519" {
			return nil, newError(ErrInvalidDPoP, "only Ed25519 OKP keys are supported")
		}
		raw, err := decodeJWKField("x", j.X, ed25519.PublicKeySize)
		if err != nil {
			return nil, err
		}
		return ed25519.PublicKey(raw), nil

	default:
		return nil, newError(ErrInvalidDPoP, "unsupported DPoP JWK key type")
	}
}

func decodeJWKField(name, value string, size int) ([]byte, error) {
	raw, err := b64.DecodeString(value)
	if err != nil {
		return nil, newError(ErrInvalidDPoP, "DPoP JWK parameter "+name+" is not valid base64url")
	}
	if len(raw) != size {
		return nil, newError(ErrInvalidDPoP, fmt.Sprintf("DPoP JWK parameter %s has invalid length %d", name, len(raw)))
	}
	return raw, nil
}

// unmarshalUnique decodes data into target while rejecting duplicate object
// member names anywhere in the JSON document.
func unmarshalUnique(data []byte, target interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected JSON object")
	}
	if err := decodeUniqueObject(dec, make(map[string]bool)); err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func decodeUniqueObject(dec *json.Decoder, seen map[string]bool) error {
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("invalid object key")
		}
		if seen[key] {
			return fmt.Errorf("duplicate JSON member %q", key)
		}
		seen[key] = true
		if err := decodeUniqueValue(dec, seen); err != nil {
			return err
		}
	}
	closing, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := closing.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("invalid JSON object terminator")
	}
	return nil
}

func decodeUniqueValue(dec *json.Decoder, parentSeen map[string]bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar value
	}
	switch delim {
	case '{':
		return decodeUniqueObject(dec, make(map[string]bool))
	case '[':
		for dec.More() {
			if err := decodeUniqueValue(dec, parentSeen); err != nil {
				return err
			}
		}
		closeTok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := closeTok.(json.Delim); !ok || d != ']' {
			return fmt.Errorf("invalid JSON array terminator")
		}
	}
	return nil
}
