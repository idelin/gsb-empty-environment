package netsec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// tokenClaimsError is returned for structurally invalid JWT or JSON token
// input.
func tokenClaimsError(format string, args ...any) error {
	return fmt.Errorf("netsec: invalid token claims: "+format, args...)
}

// ParseJWTClaims decodes the payload of a compact JWT. It does not verify the
// signature or any JOSE header; the authorization or resource server must do
// that before calling the binding validators.
func ParseJWTClaims(raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, tokenClaimsError("JWT must have three segments")
	}
	if parts[1] == "" {
		return nil, tokenClaimsError("JWT payload is empty")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return nil, tokenClaimsError("JWT payload is not unpadded base64url: %v", err)
	}
	return decodeClaimsObject(payload)
}

// ParseIntrospectionResponse decodes an RFC 7662 introspection response into
// token claims. It does not check the "active" member.
func ParseIntrospectionResponse(raw []byte) (map[string]any, error) {
	return decodeClaimsObject(raw)
}

func decodeClaimsObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var claims map[string]any
	if err := decoder.Decode(&claims); err != nil {
		return nil, tokenClaimsError("claims are not a JSON object: %v", err)
	}
	if claims == nil {
		return nil, tokenClaimsError("claims are not a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, tokenClaimsError("trailing JSON data after claims object")
	}
	return claims, nil
}
