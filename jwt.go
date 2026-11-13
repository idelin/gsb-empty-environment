package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func splitJWT(token string) (header, payload, signature []byte, err error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, nil, nil, fmt.Errorf("%w: compact JWT must have three parts", ErrInvalidProof)
	}
	if header, err = decodeBase64URL(parts[0]); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: invalid header encoding: %v", ErrInvalidProof, err)
	}
	if payload, err = decodeBase64URL(parts[1]); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: invalid payload encoding: %v", ErrInvalidProof, err)
	}
	if signature, err = decodeBase64URL(parts[2]); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: invalid signature encoding: %v", ErrInvalidProof, err)
	}
	return header, payload, signature, nil
}

func parseJSONObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON value is not an object")
	}

	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, err
	}
	return object, nil
}

// ParseAccessTokenClaims parses the claims of a compact JWT access token. It
// does not verify the token signature: the resource server must authenticate
// the token with its issuer/authorization server before calling this package.
func ParseAccessTokenClaims(accessToken string) (map[string]any, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("%w: empty token", ErrInvalidToken)
	}
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: compact JWT must have three parts", ErrInvalidToken)
	}
	payload, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid payload encoding: %v", ErrInvalidToken, err)
	}
	claims, err := parseJSONObject(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid claims: %v", ErrInvalidToken, err)
	}
	return claims, nil
}

// TokenJKT returns the cnf.jkt value from an access token. A token without a
// usable cnf.jkt is not DPoP-bound and is rejected with ErrTokenNotBound.
func TokenJKT(accessToken string) (string, error) {
	claims, err := ParseAccessTokenClaims(accessToken)
	if err != nil {
		return "", err
	}
	return JKTFromClaims(claims)
}

// JKTFromClaims extracts cnf.jkt from already-parsed token claims.
func JKTFromClaims(claims map[string]any) (string, error) {
	value, ok := claims["cnf"]
	if !ok || value == nil {
		return "", fmt.Errorf("%w: missing cnf", ErrTokenNotBound)
	}
	cnf, ok := value.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%w: cnf is not an object", ErrTokenNotBound)
	}
	rawJKT, ok := cnf["jkt"]
	if !ok || rawJKT == nil {
		return "", fmt.Errorf("%w: missing cnf.jkt", ErrTokenNotBound)
	}
	jkt, ok := rawJKT.(string)
	if !ok || strings.TrimSpace(jkt) == "" {
		return "", fmt.Errorf("%w: cnf.jkt must be a non-empty string", ErrTokenNotBound)
	}
	return jkt, nil
}

func claimString(claims map[string]any, name string) (string, bool, error) {
	value, ok := claims[name]
	if !ok || value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("%w: %s must be a string", ErrInvalidProof, name)
	}
	return text, true, nil
}

func claimInt64(claims map[string]any, name string) (int64, bool, error) {
	value, ok := claims[name]
	if !ok || value == nil {
		return 0, false, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, true, fmt.Errorf("%w: %s must be an integer", ErrInvalidProof, name)
	}
	integer, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("%w: %s must be an integer", ErrInvalidProof, name)
	}
	return integer, true, nil
}

func claimObject(claims map[string]any, name string) (map[string]any, bool, error) {
	value, ok := claims[name]
	if !ok || value == nil {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("%w: %s must be an object", ErrInvalidProof, name)
	}
	return object, true, nil
}
