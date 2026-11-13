package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// algHS256 is the only signing algorithm supported by this library. JARM
// responses carrying any other value in the JWT header alg field are rejected
// so that a caller cannot be tricked into accepting an unsigned token.
const algHS256 = "HS256"

var (
	// ErrMalformedJWT is returned when a token is not three dot separated
	// base64url segments or cannot be decoded as JSON.
	ErrMalformedJWT = errors.New("netsec: malformed jwt")
	// ErrUnsupportedAlgorithm is returned when the token header advertises an
	// algorithm other than HS256, including the "none" algorithm.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported jwt algorithm")
	// ErrInvalidSignature is returned when the HMAC does not verify.
	ErrInvalidSignature = errors.New("netsec: invalid jwt signature")
	// ErrInvalidKey is returned when an empty signing key is configured.
	ErrInvalidKey = errors.New("netsec: signing key must not be empty")
)

// jwtHeader is the JOSE header of a signed JWT.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func base64URLDecode(segment string) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJWT, err)
	}
	return data, nil
}

// signJWT builds an HS256 compact serialization token from the claims.
func signJWT(claims map[string]any, key []byte) (string, error) {
	if len(key) == 0 {
		return "", ErrInvalidKey
	}
	header := jwtHeader{Alg: algHS256, Typ: "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64URLEncode(headerJSON) + "." + base64URLEncode(claimsJSON)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	signature := base64URLEncode(mac.Sum(nil))
	return signingInput + "." + signature, nil
}

// verifyJWT validates the signature and header of an HS256 token and returns
// the raw, still unvalidated claim set.
func verifyJWT(token string, key []byte) (map[string]any, error) {
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return nil, ErrMalformedJWT
	}
	headerBytes, err := base64URLDecode(segments[0])
	if err != nil {
		return nil, err
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJWT, err)
	}
	if header.Alg != algHS256 {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, header.Alg)
	}

	signingInput := segments[0] + "." + segments[1]
	expectedMAC := hmac.New(sha256.New, key)
	expectedMAC.Write([]byte(signingInput))
	expectedSignature := expectedMAC.Sum(nil)

	providedSignature, err := base64URLDecode(segments[2])
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(providedSignature, expectedSignature) != 1 {
		return nil, ErrInvalidSignature
	}

	payload, err := base64URLDecode(segments[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJWT, err)
	}
	return claims, nil
}
