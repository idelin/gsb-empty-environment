package dpop

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

const dpopJWTType = "dpop+jwt"

// allowedProofAlgs is the asymmetric algorithm whitelist. Symmetric
// algorithms (HS256, ...) and the unsecured "none" algorithm are rejected.
var allowedProofAlgs = map[string]bool{
	"ES256": true,
	"EdDSA": true,
}

type joseHeader struct {
	Type      string `json:"typ"`
	Algorithm string `json:"alg"`
	Key       *JWK   `json:"jwk"`
	KeyID     string `json:"kid,omitempty"`
	KeyURL    string `json:"jku,omitempty"`
	X509URL   string `json:"x5u,omitempty"`
}

type rawProofClaims struct {
	HTM   string          `json:"htm"`
	HTU   string          `json:"htu"`
	ATH   string          `json:"ath"`
	JTI   string          `json:"jti"`
	Nonce string          `json:"nonce,omitempty"`
	IAT   json.Number     `json:"iat"`
	Extra json.RawMessage `json:"-"`
}

type parsedProof struct {
	header       joseHeader
	claims       rawProofClaims
	signingInput []byte
	signature    []byte
}

// parseProof splits and validates the structural and header requirements of a
// DPoP proof JWT. It does not verify the signature; verifySignature does.
func parseProof(compact string) (*parsedProof, error) {
	parts := strings.Split(compact, ".")
	if len(parts) != 3 {
		return nil, newError(ErrInvalidDPoP, "DPoP header must be a compact JWS with three parts")
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, newError(ErrInvalidDPoP, "DPoP JWS parts must not be empty")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, newError(ErrInvalidDPoP, "DPoP JWT header is not valid base64url")
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, newError(ErrInvalidDPoP, "DPoP JWT payload is not valid base64url")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, newError(ErrInvalidDPoP, "DPoP JWT signature is not valid base64url")
	}

	var header joseHeader
	if err := unmarshalUnique(headerJSON, &header); err != nil {
		return nil, newError(ErrInvalidDPoP, "malformed DPoP JWT header: "+err.Error())
	}
	if header.Type != dpopJWTType {
		return nil, newError(ErrInvalidDPoP, `DPoP JWT typ header must be "dpop+jwt"`)
	}
	if header.Algorithm == "" || strings.EqualFold(header.Algorithm, "none") {
		return nil, newError(ErrInvalidDPoP, "DPoP JWT alg must not be empty or none")
	}
	if !allowedProofAlgs[header.Algorithm] {
		return nil, newError(ErrInvalidDPoP, "DPoP JWT alg "+header.Algorithm+" is not allowed; only ES256 and EdDSA are accepted")
	}
	if header.Key == nil {
		return nil, newError(ErrInvalidDPoP, "DPoP JWT header must embed a public jwk")
	}
	if header.KeyURL != "" || header.X509URL != "" {
		return nil, newError(ErrInvalidDPoP, "DPoP JWT must reference the proof key via an embedded jwk only")
	}
	if header.Key.Algorithm != "" && header.Key.Algorithm != header.Algorithm {
		return nil, newError(ErrInvalidDPoP, "DPoP JWK alg does not match JWT header alg")
	}

	var claims rawProofClaims
	if err := unmarshalUnique(payloadJSON, &claims); err != nil {
		return nil, newError(ErrInvalidDPoP, "malformed DPoP JWT payload: "+err.Error())
	}

	return &parsedProof{
		header:       header,
		claims:       claims,
		signingInput: []byte(parts[0] + "." + parts[1]),
		signature:    signature,
	}, nil
}

// verifySignature checks the proof signature using the embedded public JWK.
func (p *parsedProof) verifySignature() error {
	publicKey, err := p.header.Key.PublicKey()
	if err != nil {
		return err
	}

	sum := sha256.Sum256(p.signingInput)
	switch p.header.Algorithm {
	case "ES256":
		ecKey, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			return newError(ErrInvalidDPoP, "ES256 proof requires an EC P-256 jwk")
		}
		if len(p.signature) != 64 {
			return newError(ErrInvalidDPoP, "ES256 signature must be the 64-byte R||S concatenation")
		}
		r := new(big.Int).SetBytes(p.signature[:32])
		s := new(big.Int).SetBytes(p.signature[32:])
		if !ecdsa.Verify(ecKey, sum[:], r, s) {
			return newError(ErrInvalidDPoP, "DPoP proof signature verification failed")
		}
	case "EdDSA":
		edKey, ok := publicKey.(ed25519.PublicKey)
		if !ok {
			return newError(ErrInvalidDPoP, "EdDSA proof requires an OKP Ed25519 jwk")
		}
		if !ed25519.Verify(edKey, p.signingInput, p.signature) {
			return newError(ErrInvalidDPoP, "DPoP proof signature verification failed")
		}
	default:
		return newError(ErrInvalidDPoP, fmt.Sprintf("unsupported DPoP alg %q", p.header.Algorithm))
	}
	return nil
}

// tokenHash returns the base64url-no-pad SHA-256 hash of the raw access
// token, which is the value required by the DPoP "ath" claim.
func tokenHash(accessToken string) string {
	sum := sha256.Sum256([]byte(accessToken))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
