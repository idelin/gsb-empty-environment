package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

var forbiddenJWKFields = map[string]struct{}{
	"d":   {},
	"p":   {},
	"q":   {},
	"dp":  {},
	"dq":  {},
	"qi":  {},
	"k":   {},
	"oth": {},
}

type jwkFields struct {
	KTY string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
	CRV string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// ThumbprintJWK returns the base64url-encoded SHA-256 JWK thumbprint from
// RFC 7638. Public RSA, EC, and Ed25519 JWKs are accepted.
func ThumbprintJWK(raw json.RawMessage) (string, error) {
	_, key, err := parseJWK(raw)
	if err != nil {
		return "", err
	}

	var canonical map[string]string
	switch key.KTY {
	case "RSA":
		if key.N == "" || key.E == "" {
			return "", fmt.Errorf("%w: RSA JWK requires n and e", ErrInvalidProof)
		}
		canonical = map[string]string{
			"e":   key.E,
			"kty": key.KTY,
			"n":   key.N,
		}
	case "EC":
		if key.CRV == "" || key.X == "" || key.Y == "" {
			return "", fmt.Errorf("%w: EC JWK requires crv, x, and y", ErrInvalidProof)
		}
		canonical = map[string]string{
			"crv": key.CRV,
			"kty": key.KTY,
			"x":   key.X,
			"y":   key.Y,
		}
	case "OKP":
		if key.CRV == "" || key.X == "" {
			return "", fmt.Errorf("%w: OKP JWK requires crv and x", ErrInvalidProof)
		}
		canonical = map[string]string{
			"crv": key.CRV,
			"kty": key.KTY,
			"x":   key.X,
		}
	default:
		return "", fmt.Errorf("%w: unsupported JWK key type %q", ErrInvalidProof, key.KTY)
	}

	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProof, err)
	}
	digest := sha256.Sum256(canonicalJSON)
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func publicKeyFromJWK(raw json.RawMessage) (crypto.PublicKey, error) {
	_, key, err := parseJWK(raw)
	if err != nil {
		return nil, err
	}

	switch key.KTY {
	case "RSA":
		nBytes, err := decodeBase64URL(key.N)
		if err != nil {
			return nil, invalidJWKError(err)
		}
		eBytes, err := decodeBase64URL(key.E)
		if err != nil {
			return nil, invalidJWKError(err)
		}

		n := new(big.Int).SetBytes(nBytes)
		e := new(big.Int).SetBytes(eBytes)
		if n.Sign() <= 0 || !e.IsInt64() || e.Sign() <= 0 {
			return nil, fmt.Errorf("%w: invalid RSA modulus or exponent", ErrInvalidProof)
		}

		publicKey := &rsa.PublicKey{N: n, E: int(e.Int64())}
		if publicKey.Size() == 0 {
			return nil, fmt.Errorf("%w: invalid RSA public key", ErrInvalidProof)
		}
		return publicKey, nil

	case "EC":
		curve, err := curveFromCRV(key.CRV)
		if err != nil {
			return nil, err
		}
		xBytes, err := decodeBase64URL(key.X)
		if err != nil {
			return nil, invalidJWKError(err)
		}
		yBytes, err := decodeBase64URL(key.Y)
		if err != nil {
			return nil, invalidJWKError(err)
		}

		publicKey := &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}
		if !curve.IsOnCurve(publicKey.X, publicKey.Y) {
			return nil, fmt.Errorf("%w: EC JWK point is not on curve %q", ErrInvalidProof, key.CRV)
		}
		return publicKey, nil

	case "OKP":
		if key.CRV != "Ed25519" {
			return nil, fmt.Errorf("%w: unsupported OKP curve %q", ErrInvalidProof, key.CRV)
		}
		xBytes, err := decodeBase64URL(key.X)
		if err != nil {
			return nil, invalidJWKError(err)
		}
		if len(xBytes) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 public key length", ErrInvalidProof)
		}
		return ed25519.PublicKey(xBytes), nil

	default:
		return nil, fmt.Errorf("%w: unsupported JWK key type %q", ErrInvalidProof, key.KTY)
	}
}

func parseJWK(raw json.RawMessage) (map[string]json.RawMessage, jwkFields, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, jwkFields{}, invalidJWKError(err)
	}
	if fields == nil {
		return nil, jwkFields{}, fmt.Errorf("%w: JWK must be a JSON object", ErrInvalidProof)
	}

	for field := range fields {
		if _, forbidden := forbiddenJWKFields[field]; forbidden {
			return nil, jwkFields{}, fmt.Errorf("%w: JWK must not contain private or symmetric field %q", ErrInvalidProof, field)
		}
	}

	var key jwkFields
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, jwkFields{}, invalidJWKError(err)
	}
	if key.KTY == "" {
		return nil, jwkFields{}, fmt.Errorf("%w: JWK requires kty", ErrInvalidProof)
	}
	return fields, key, nil
}

func curveFromCRV(crv string) (elliptic.Curve, error) {
	switch crv {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("%w: unsupported EC curve %q", ErrInvalidProof, crv)
	}
}

func decodeBase64URL(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func invalidJWKError(err error) error {
	return fmt.Errorf("%w: invalid JWK: %v", ErrInvalidProof, err)
}
