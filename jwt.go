package netsec

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const maxTokenSize = 1 << 20

// Header is the compact JWT header used by this package.
type Header struct {
	Type      string `json:"typ,omitempty"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"xkey,omitempty"`
}

// Token is a parsed compact JWT. Its signature has not yet been verified.
type Token struct {
	Raw       string
	Header    Header
	Payload   []byte
	Signature []byte
}

// ParseJWT parses a compact JWT and enforces supported header fields. Signature
// verification is deliberately separate because the trusted key depends on
// claims that have not been authenticated yet.
func ParseJWT(token string) (*Token, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" || len(trimmed) > maxTokenSize {
		return nil, ErrMalformedToken
	}

	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return nil, ErrMalformedToken
	}

	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid header encoding", ErrMalformedToken)
	}
	payload, err := decodeSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid payload encoding", ErrMalformedToken)
	}
	signature, err := decodeSegment(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: invalid signature encoding", ErrMalformedToken)
	}

	var header Header
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: invalid header JSON", ErrMalformedToken)
	}
	if !strings.EqualFold(header.Algorithm, "ed25519") &&
		!strings.EqualFold(header.Algorithm, "EdDSA") {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, header.Algorithm)
	}
	if header.Type != "" && !strings.EqualFold(header.Type, "JWT") {
		return nil, fmt.Errorf("%w: unsupported header type %q", ErrMalformedToken, header.Type)
	}

	var payloadObject map[string]json.RawMessage
	if err := json.Unmarshal(payload, &payloadObject); err != nil || payloadObject == nil {
		return nil, fmt.Errorf("%w: claims must be a JSON object", ErrMalformedToken)
	}

	return &Token{
		Raw:       trimmed,
		Header:    header,
		Payload:   payload,
		Signature: signature,
	}, nil
}

// VerifySignature verifies the JWT with publicKey.
func (token *Token) VerifySignature(publicKey ed25519.PublicKey) bool {
	if token == nil || !validPublicKey(publicKey) || len(token.Signature) != ed25519.SignatureSize {
		return false
	}
	firstDot := strings.IndexByte(token.Raw, '.')
	secondDot := strings.IndexByte(token.Raw[firstDot+1:], '.')
	if firstDot < 0 || secondDot < 0 {
		return false
	}
	input := token.Raw[:firstDot+1+secondDot]
	return ed25519.Verify(publicKey, []byte(input), token.Signature)
}

// SignJWT signs claims with Ed25519 using the NATS compact JWT header.
func SignJWT(claims any, privateKey ed25519.PrivateKey) (string, error) {
	return SignJWTWithHeader(claims, Header{Type: "JWT", Algorithm: "ed25519"}, privateKey)
}

// SignToken is an alias for SignJWT.
func SignToken(claims any, privateKey ed25519.PrivateKey) (string, error) {
	return SignJWT(claims, privateKey)
}

// SignJWTWithHeader signs claims using a custom header. The header algorithm
// must be Ed25519.
func SignJWTWithHeader(claims any, header Header, privateKey ed25519.PrivateKey) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("%w: private key", ErrInvalidKey)
	}
	if !strings.EqualFold(header.Algorithm, "ed25519") &&
		!strings.EqualFold(header.Algorithm, "EdDSA") {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, header.Algorithm)
	}
	if header.Type == "" {
		header.Type = "JWT"
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("netsec: marshal header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("netsec: marshal claims: %w", err)
	}

	headerSegment := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsSegment := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerSegment + "." + claimsSegment
	signature := ed25519.Sign(privateKey, []byte(signingInput))

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// ParseAccountClaims decodes account claims without verifying their signature.
// Use Authenticator.VerifyAccount for untrusted input.
func ParseAccountClaims(token string) (*AccountClaims, error) {
	parsed, err := ParseJWT(token)
	if err != nil {
		return nil, err
	}
	return decodeAccountClaims(parsed.Payload)
}

// ParseUserClaims decodes user claims without verifying their signature. Use
// Authenticator.VerifyUser for untrusted input.
func ParseUserClaims(token string) (*UserClaims, error) {
	parsed, err := ParseJWT(token)
	if err != nil {
		return nil, err
	}
	return decodeUserClaims(parsed.Payload)
}

func decodeAccountClaims(payload []byte) (*AccountClaims, error) {
	var claims AccountClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func decodeUserClaims(payload []byte) (*UserClaims, error) {
	var claims UserClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func decodeSegment(segment string) ([]byte, error) {
	if segment == "" {
		return nil, ErrMalformedToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err == nil {
		return decoded, nil
	}
	if decoded, paddedErr := base64.URLEncoding.DecodeString(segment); paddedErr == nil {
		return decoded, nil
	}
	return nil, err
}
