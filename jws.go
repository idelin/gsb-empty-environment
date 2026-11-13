package netsec

import (
	"crypto"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"example.org/netsec/q0155/strictjson"
)

// Errors returned by the compact JWS verifier.
var (
	ErrMalformed      = errors.New("netsec: malformed compact JWS")
	ErrDuplicateKey   = errors.New("netsec: duplicate JSON key rejected")
	ErrEmptyAlgorithm = errors.New("netsec: missing or empty \"alg\" header parameter")
	ErrNoneAlgorithm  = errors.New("netsec: \"none\" algorithm is not accepted")
	ErrUnsupportedAlg = errors.New("netsec: unsupported algorithm")
	ErrBadSignature   = errors.New("netsec: signature verification failed")
)

// ProtectedHeader holds the parameters parsed from the JWS protected header.
// Unknown parameters are preserved in Extra so no header information is lost.
type ProtectedHeader struct {
	Algorithm string         `json:"alg"`
	Type      string         `json:"typ,omitempty"`
	KeyID     string         `json:"kid,omitempty"`
	JWKSetURL string         `json:"jku,omitempty"`
	JWK       any            `json:"jwk,omitempty"`
	Extra     map[string]any `json:"-"`
}

// Claims holds the standard JWT claims parsed from the JWS payload. Claims
// that are absent keep their zero value; non-standard claims are in Extra.
type Claims struct {
	Issuer         string         `json:"iss,omitempty"`
	Subject        string         `json:"sub,omitempty"`
	Audience       any            `json:"aud,omitempty"`
	ExpirationTime int64          `json:"exp,omitempty"`
	NotBefore      int64          `json:"nbf,omitempty"`
	IssuedAt       int64          `json:"iat,omitempty"`
	JWTID          string         `json:"jti,omitempty"`
	Extra          map[string]any `json:"-"`
}

// hmacHash maps JWS "alg" values for MAC-based algorithms to their hash.
var hmacHash = map[string]crypto.Hash{
	"HS256": crypto.SHA256,
	"HS384": crypto.SHA384,
	"HS512": crypto.SHA512,
}

// VerifyCompact verifies a compact-serialized JWS (JWT) using a shared
// HMAC secret. Both the protected header and the payload are decoded with the
// importable strict JSON decoder (example.org/netsec/q0155/strictjson), which
// rejects duplicate keys outright: a protected header that lists "alg" twice,
// or any other repeated key, fails verification instead of letting a later
// value overwrite an earlier one.
func VerifyCompact(token string, secret []byte) (*ProtectedHeader, *Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, fmt.Errorf("%w: expected 3 parts, got %d", ErrMalformed, len(parts))
	}
	headerPart, payloadPart, signaturePart := parts[0], parts[1], parts[2]

	headerJSON, err := base64.RawURLEncoding.DecodeString(headerPart)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: header is not valid base64url: %v", ErrMalformed, err)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: payload is not valid base64url: %v", ErrMalformed, err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: signature is not valid base64url: %v", ErrMalformed, err)
	}

	header, err := decodeProtectedHeader(headerJSON)
	if err != nil {
		return nil, nil, err
	}
	claims, err := decodeClaims(payloadJSON)
	if err != nil {
		return nil, nil, err
	}

	if err := verifySignature(header.Algorithm, secret, headerPart+"."+payloadPart, signature); err != nil {
		return nil, nil, err
	}
	return header, claims, nil
}

// decodeProtectedHeader strictly decodes the protected header JSON. A
// duplicate key anywhere in the object (including two "alg" entries) is a
// hard failure.
func decodeProtectedHeader(data []byte) (*ProtectedHeader, error) {
	var raw map[string]any
	if err := strictjson.Unmarshal(data, &raw); err != nil {
		if errors.Is(err, strictjson.ErrDuplicateKey) {
			return nil, fmt.Errorf("%w in protected header: %v", ErrDuplicateKey, err)
		}
		return nil, fmt.Errorf("%w: header is not valid JSON: %v", ErrMalformed, err)
	}

	var header ProtectedHeader
	if err := strictjson.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("%w: header cannot be decoded: %v", ErrMalformed, err)
	}
	header.Extra = make(map[string]any, len(raw))
	for key, value := range raw {
		header.Extra[key] = value
	}

	if header.Algorithm == "" {
		return nil, ErrEmptyAlgorithm
	}
	if header.Algorithm == "none" {
		return nil, ErrNoneAlgorithm
	}
	return &header, nil
}

// decodeClaims strictly decodes the JWT payload JSON, applying the same
// duplicate-key rule as the header.
func decodeClaims(data []byte) (*Claims, error) {
	var raw map[string]any
	if err := strictjson.Unmarshal(data, &raw); err != nil {
		if errors.Is(err, strictjson.ErrDuplicateKey) {
			return nil, fmt.Errorf("%w in payload: %v", ErrDuplicateKey, err)
		}
		return nil, fmt.Errorf("%w: payload is not valid JSON: %v", ErrMalformed, err)
	}

	var claims Claims
	if err := strictjson.Unmarshal(data, &claims); err != nil {
		return nil, fmt.Errorf("%w: payload cannot be decoded: %v", ErrMalformed, err)
	}
	claims.Extra = make(map[string]any, len(raw))
	for key, value := range raw {
		claims.Extra[key] = value
	}
	return &claims, nil
}

// verifySignature checks the MAC over the compact signing input.
func verifySignature(algorithm string, secret []byte, signingInput string, signature []byte) error {
	hash, ok := hmacHash[algorithm]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlg, algorithm)
	}
	if !hash.Available() {
		return fmt.Errorf("%w: hash for %q is unavailable", ErrUnsupportedAlg, algorithm)
	}
	mac := hmac.New(hash.New, secret)
	mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, signature) {
		return ErrBadSignature
	}
	return nil
}

// EncodeCompact builds a compact JWS for tests and callers that need to emit
// tokens. Header and payload are encoded as-is; keys must not be duplicated
// because Go maps cannot represent them.
func EncodeCompact(header ProtectedHeader, claims Claims, secret []byte) (string, error) {
	if header.Algorithm == "" {
		return "", ErrEmptyAlgorithm
	}
	headerJSON, err := marshalHeader(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := marshalClaims(claims)
	if err != nil {
		return "", err
	}

	enc := base64.RawURLEncoding
	headerPart := enc.EncodeToString(headerJSON)
	payloadPart := enc.EncodeToString(payloadJSON)
	signingInput := headerPart + "." + payloadPart

	hash, ok := hmacHash[header.Algorithm]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAlg, header.Algorithm)
	}
	mac := hmac.New(hash.New, secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + enc.EncodeToString(mac.Sum(nil)), nil
}

func marshalHeader(header ProtectedHeader) ([]byte, error) {
	fields := map[string]any{}
	for key, value := range header.Extra {
		fields[key] = value
	}
	fields["alg"] = header.Algorithm
	if header.Type != "" {
		fields["typ"] = header.Type
	}
	if header.KeyID != "" {
		fields["kid"] = header.KeyID
	}
	if header.JWKSetURL != "" {
		fields["jku"] = header.JWKSetURL
	}
	if header.JWK != nil {
		fields["jwk"] = header.JWK
	}
	return json.Marshal(fields)
}

func marshalClaims(claims Claims) ([]byte, error) {
	fields := map[string]any{}
	for key, value := range claims.Extra {
		fields[key] = value
	}
	if claims.Issuer != "" {
		fields["iss"] = claims.Issuer
	}
	if claims.Subject != "" {
		fields["sub"] = claims.Subject
	}
	if claims.Audience != nil {
		fields["aud"] = claims.Audience
	}
	if claims.ExpirationTime != 0 {
		fields["exp"] = claims.ExpirationTime
	}
	if claims.NotBefore != 0 {
		fields["nbf"] = claims.NotBefore
	}
	if claims.IssuedAt != 0 {
		fields["iat"] = claims.IssuedAt
	}
	if claims.JWTID != "" {
		fields["jti"] = claims.JWTID
	}
	return json.Marshal(fields)
}
