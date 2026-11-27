package dpop

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

type cnfMember struct {
	JKT string `json:"jkt"`
}

type accessTokenPayload struct {
	Cnf *cnfMember `json:"cnf"`
}

// ParseJWTTokenClaims decodes the payload of a compact JWT access token and
// extracts cnf.jkt. It performs no signature or expiry validation: gateway
// hosts must validate the token themselves (via a wrapping TokenInspector or
// token introspection) before relying on the result. Non-JWT opaque tokens
// yield empty claims rather than an error, allowing an introspection-based
// inspector to handle them.
func ParseJWTTokenClaims(accessToken string) (*TokenClaims, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return &TokenClaims{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, newError(ErrInvalidToken, "access token JWT payload is not valid base64url")
	}
	var parsed accessTokenPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, newError(ErrInvalidToken, "access token JWT payload is not valid JSON")
	}
	claims := &TokenClaims{}
	if parsed.Cnf != nil {
		claims.CnfJKT = parsed.Cnf.JKT
	}
	return claims, nil
}
