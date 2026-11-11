package dpopcheck

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"
)

// dpopJWTType is the exact "typ" value required for DPoP proof JWTs
// (RFC 9449 section 4.2).
const dpopJWTType = "dpop+jwt"

// supportedAlgorithm is the only JWS algorithm accepted by this verifier.
const supportedAlgorithm = "ES256"

// proofHeader is the protected header of a DPoP proof JWT.
type proofHeader struct {
	Typ string `json:"typ"`
	Alg string `json:"alg"`
	JWK jwk    `json:"jwk"`
}

// jwk is the minimal JSON Web Key representation needed for ES256 proofs.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// publicKey converts the JWK into an *ecdsa.PublicKey, rejecting malformed
// coordinates or a key that does not match the P-256 / ES256 parameters.
func (k jwk) publicKey() (*ecdsa.PublicKey, error) {
	if k.Kty != "EC" || k.Crv != "P-256" || k.X == "" || k.Y == "" {
		return nil, errors.New("jwk must be an EC P-256 public key")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, errors.New("jwk x is not valid base64url")
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, errors.New("jwk y is not valid base64url")
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	publicKey := &ecdsa.PublicKey{
		Curve: ecdsaCurve(),
		X:     x,
		Y:     y,
	}
	if !publicKey.Curve.IsOnCurve(x, y) {
		return nil, errors.New("jwk point is not on the P-256 curve")
	}
	return publicKey, nil
}

// thumbprint computes the RFC 7638 JSON Web Key Thumbprint (SHA-256,
// base64url without padding) of the key. The members are serialized in the
// lexical order mandated by the RFC: crv, kty, x, y.
func (k jwk) thumbprint() (string, error) {
	if _, err := k.publicKey(); err != nil {
		return "", err
	}
	canonical := struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}{
		Crv: k.Crv,
		Kty: k.Kty,
		X:   k.X,
		Y:   k.Y,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// parsedProof holds the validated structural pieces of a proof JWT.
type parsedProof struct {
	header    proofHeader
	payload   proofClaims
	signing   []byte
	signature []byte
}

// parseProof splits the compact JWS, decodes header and payload and checks
// the header's typ/alg. The embedded-key signature is verified separately by
// the caller.
func parseProof(token string) (*parsedProof, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, errors.New("proof is not a compact JWS")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("proof header is not valid base64url")
	}
	var header proofHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, errors.New("proof header is not valid JSON")
	}
	if header.Typ != dpopJWTType {
		return nil, errors.New("proof typ must be \"dpop+jwt\"")
	}
	if header.Alg != supportedAlgorithm {
		return nil, errors.New("proof alg must be ES256")
	}
	if _, err := header.JWK.publicKey(); err != nil {
		return nil, err
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("proof payload is not valid base64url")
	}
	var claims proofClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, errors.New("proof payload is not valid JSON")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("proof signature is not valid base64url")
	}
	return &parsedProof{
		header:    header,
		payload:   claims,
		signing:   []byte(parts[0] + "." + parts[1]),
		signature: signature,
	}, nil
}

// verifyES256 checks a raw R||S ECDSA signature over the JWS signing input
// using the embedded public key.
func (p *parsedProof) verifyES256() error {
	publicKey, err := p.header.JWK.publicKey()
	if err != nil {
		return err
	}
	if len(p.signature) != 64 {
		return errors.New("ES256 signature must be 64 bytes (R||S)")
	}
	hash := sha256.Sum256(p.signing)
	r := new(big.Int).SetBytes(p.signature[:32])
	s := new(big.Int).SetBytes(p.signature[32:])
	if !ecdsa.Verify(publicKey, hash[:], r, s) {
		return errors.New("ES256 signature verification failed")
	}
	return nil
}

// normalizeHTU builds the canonical target URI the proof's htu claim must
// contain: the absolute request URL without query or fragment, with default
// ports removed. Both the incoming request and the claim are passed through
// the same function so equivalent spellings compare equal.
func normalizeHTU(u *url.URL) string {
	if u == nil {
		return ""
	}
	normalized := *u
	normalized.RawQuery = ""
	normalized.Fragment = ""
	normalized.RawFragment = ""
	if host := normalized.Hostname(); host != "" {
		port := normalized.Port()
		scheme := strings.ToLower(normalized.Scheme)
		if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
			normalized.Host = host
		}
	}
	return normalized.String()
}

// hashAccessToken produces the base64url(SHA-256(access token)) "ath" value
// defined by RFC 9449 section 4.2.
func hashAccessToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
