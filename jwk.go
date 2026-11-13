package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// jwkData is the public part of a JWK used to verify a DPoP proof.
type jwkData struct {
	kty string
	crv string
	x   string
	y   string
	n   string
	e   string
	alg string
}

func parseJWK(raw json.RawMessage) (*jwkData, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%w: empty jwk", ErrMissingJWK)
	}

	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, fmt.Errorf("%w: not a JSON object: %v", ErrInvalidJWK, err)
	}

	for _, name := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
		if _, ok := members[name]; ok {
			return nil, fmt.Errorf("%w: private member %q is not allowed", ErrInvalidJWK, name)
		}
	}

	j := &jwkData{}
	var err error
	if j.kty, err = jwkString(members, "kty", true); err != nil {
		return nil, err
	}
	if j.crv, err = jwkString(members, "crv", false); err != nil {
		return nil, err
	}
	if j.x, err = jwkString(members, "x", false); err != nil {
		return nil, err
	}
	if j.y, err = jwkString(members, "y", false); err != nil {
		return nil, err
	}
	if j.n, err = jwkString(members, "n", false); err != nil {
		return nil, err
	}
	if j.e, err = jwkString(members, "e", false); err != nil {
		return nil, err
	}
	if j.alg, err = jwkString(members, "alg", false); err != nil {
		return nil, err
	}

	switch j.kty {
	case "EC":
		if j.crv == "" || j.x == "" || j.y == "" {
			return nil, fmt.Errorf("%w: EC jwk requires crv, x and y", ErrInvalidJWK)
		}
	case "RSA":
		if j.n == "" || j.e == "" {
			return nil, fmt.Errorf("%w: RSA jwk requires n and e", ErrInvalidJWK)
		}
	case "OKP":
		if j.crv == "" || j.x == "" {
			return nil, fmt.Errorf("%w: OKP jwk requires crv and x", ErrInvalidJWK)
		}
	default:
		return nil, fmt.Errorf("%w: unsupported kty %q", ErrInvalidJWK, j.kty)
	}

	return j, nil
}

func jwkString(members map[string]json.RawMessage, name string, required bool) (string, error) {
	raw, ok := members[name]
	if !ok {
		if required {
			return "", fmt.Errorf("%w: missing %q", ErrInvalidJWK, name)
		}
		return "", nil
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("%w: %q must be a string", ErrInvalidJWK, name)
	}
	return value, nil
}

// Thumbprint returns the RFC 7638 JWK thumbprint (jkt) as base64url without
// padding. The required members are canonicalized exactly as RFC 7638
// specifies, so the result is stable across JSON encoders.
func (j *jwkData) Thumbprint() (string, error) {
	var canonical []byte
	var err error

	switch j.kty {
	case "EC":
		canonical, err = canonicalJWK("crv", j.crv, "kty", j.kty, "x", j.x, "y", j.y)
	case "RSA":
		canonical, err = canonicalJWK("e", j.e, "kty", j.kty, "n", j.n)
	case "OKP":
		canonical, err = canonicalJWK("crv", j.crv, "kty", j.kty, "x", j.x)
	default:
		return "", fmt.Errorf("%w: unsupported kty %q", ErrInvalidJWK, j.kty)
	}
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(canonical)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// canonicalJWK builds a JSON object with the members in the lexicographic
// order required by RFC 7638. Arguments are alternating name/value pairs.
func canonicalJWK(pairs ...string) ([]byte, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("%w: malformed canonical member list", ErrInvalidJWK)
	}

	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(pairs[i])
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(pairs[i+1])
		if err != nil {
			return nil, err
		}
		b.Write(name)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// JWKThumbprint computes the RFC 7638 thumbprint of a public JWK.
func JWKThumbprint(raw json.RawMessage) (string, error) {
	j, err := parseJWK(raw)
	if err != nil {
		return "", err
	}
	return j.Thumbprint()
}

// Thumbprint computes the RFC 7638 thumbprint of a public JWK represented as
// a generic JSON object.
func Thumbprint(jwk map[string]any) (string, error) {
	raw, err := json.Marshal(jwk)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidJWK, err)
	}
	return JWKThumbprint(raw)
}

// ComputeJWKThumbprint is an alias for JWKThumbprint.
func ComputeJWKThumbprint(raw json.RawMessage) (string, error) {
	return JWKThumbprint(raw)
}

func (j *jwkData) publicKey() (crypto.PublicKey, error) {
	switch j.kty {
	case "EC":
		curve, err := curveForName(j.crv)
		if err != nil {
			return nil, err
		}
		x, err := decodeBase64URL(j.x)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid x coordinate: %v", ErrInvalidJWK, err)
		}
		y, err := decodeBase64URL(j.y)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid y coordinate: %v", ErrInvalidJWK, err)
		}
		size := (curve.Params().BitSize + 7) / 8
		if len(x) > size || len(y) > size {
			return nil, fmt.Errorf("%w: EC coordinate length does not match %s", ErrInvalidJWK, j.crv)
		}
		x = leftPad(x, size)
		y = leftPad(y, size)
		point := make([]byte, 1+len(x)+len(y))
		point[0] = 4
		copy(point[1:], x)
		copy(point[1+len(x):], y)
		px, py := elliptic.Unmarshal(curve, point)
		if px == nil || py == nil {
			return nil, fmt.Errorf("%w: EC point is not on curve", ErrInvalidJWK)
		}
		return &ecdsa.PublicKey{Curve: curve, X: px, Y: py}, nil

	case "RSA":
		n, err := decodeBase64URL(j.n)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid modulus: %v", ErrInvalidJWK, err)
		}
		e, err := decodeBase64URL(j.e)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid exponent: %v", ErrInvalidJWK, err)
		}
		nBig := new(big.Int).SetBytes(n)
		eBig := new(big.Int).SetBytes(e)
		if nBig.Sign() <= 0 || !eBig.IsInt64() {
			return nil, fmt.Errorf("%w: invalid RSA parameters", ErrInvalidJWK)
		}
		exp := eBig.Int64()
		if exp < 3 || exp > 1<<31-1 {
			return nil, fmt.Errorf("%w: invalid RSA public exponent", ErrInvalidJWK)
		}
		return &rsa.PublicKey{N: nBig, E: int(exp)}, nil

	case "OKP":
		if j.crv != "Ed25519" {
			return nil, fmt.Errorf("%w: unsupported OKP curve %q", ErrInvalidJWK, j.crv)
		}
		x, err := decodeBase64URL(j.x)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid x coordinate: %v", ErrInvalidJWK, err)
		}
		if len(x) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 key length", ErrInvalidJWK)
		}
		return ed25519.PublicKey(x), nil

	default:
		return nil, fmt.Errorf("%w: unsupported kty %q", ErrInvalidJWK, j.kty)
	}
}

func leftPad(value []byte, size int) []byte {
	if len(value) >= size {
		return value
	}
	out := make([]byte, size)
	copy(out[size-len(value):], value)
	return out
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
		return nil, fmt.Errorf("%w: unsupported curve %q", ErrInvalidJWK, name)
	}
}

func decodeBase64URL(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("empty base64url value")
	}
	if strings.ContainsAny(value, "+/") {
		return nil, fmt.Errorf("value is not base64url")
	}
	trimmed := strings.TrimRight(value, "=")
	if trimmed == "" {
		return nil, fmt.Errorf("empty base64url value")
	}
	out, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func verifyProofSignature(j *jwkData, alg string, signingInput, signature []byte) error {
	if j.alg != "" && j.alg != alg {
		return fmt.Errorf("%w: jwk alg %q does not match header alg %q", ErrInvalidJWK, j.alg, alg)
	}

	pub, err := j.publicKey()
	if err != nil {
		return err
	}

	switch alg {
	case "ES256", "ES384", "ES512":
		wantCurve := map[string]string{"ES256": "P-256", "ES384": "P-384", "ES512": "P-521"}[alg]
		if j.kty != "EC" || j.crv != wantCurve {
			return fmt.Errorf("%w: %s requires an EC %s key", ErrInvalidJWK, alg, wantCurve)
		}
		key, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s has an incompatible key", ErrInvalidJWK, alg)
		}
		size := (key.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*size {
			return fmt.Errorf("%w: invalid ECDSA signature length", ErrInvalidSignature)
		}
		var digest []byte
		switch alg {
		case "ES256":
			sum := sha256.Sum256(signingInput)
			digest = sum[:]
		case "ES384":
			sum := sha512.Sum384(signingInput)
			digest = sum[:]
		case "ES512":
			sum := sha512.Sum512(signingInput)
			digest = sum[:]
		}
		r := new(big.Int).SetBytes(signature[:size])
		s := new(big.Int).SetBytes(signature[size:])
		if !ecdsa.Verify(key, digest, r, s) {
			return ErrInvalidSignature
		}
		return nil

	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		if j.kty != "RSA" {
			return fmt.Errorf("%w: %s requires an RSA key", ErrInvalidJWK, alg)
		}
		key, ok := pub.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s has an incompatible key", ErrInvalidJWK, alg)
		}
		hash, digest := hashForRSA(alg, signingInput)
		if strings.HasPrefix(alg, "PS") {
			err = rsa.VerifyPSS(key, hash, digest, signature, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthEqualsHash,
				Hash:       hash,
			})
		} else {
			err = rsa.VerifyPKCS1v15(key, hash, digest, signature)
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
		}
		return nil

	case "EdDSA":
		if j.kty != "OKP" || j.crv != "Ed25519" {
			return fmt.Errorf("%w: EdDSA requires an Ed25519 key", ErrInvalidJWK)
		}
		key, ok := pub.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: EdDSA has an incompatible key", ErrInvalidJWK)
		}
		if !ed25519.Verify(key, signingInput, signature) {
			return ErrInvalidSignature
		}
		return nil

	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
}

func hashForRSA(alg string, input []byte) (crypto.Hash, []byte) {
	switch alg {
	case "RS256", "PS256":
		sum := sha256.Sum256(input)
		return crypto.SHA256, sum[:]
	case "RS384", "PS384":
		sum := sha512.Sum384(input)
		return crypto.SHA384, sum[:]
	default:
		sum := sha512.Sum512(input)
		return crypto.SHA512, sum[:]
	}
}
