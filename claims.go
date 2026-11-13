package netsec

import (
	"encoding/base64"
	"fmt"
	"time"
)

func parseClaims(claims map[string]any, header joseHeader) *IDToken {
	return &IDToken{
		Issuer:    stringClaim(claims, "iss"),
		Subject:   stringClaim(claims, "sub"),
		Audience:  audienceClaim(claims, "aud"),
		Nonce:     stringClaim(claims, "nonce"),
		Expiry:    timeClaim(claims, "exp"),
		NotBefore: timeClaim(claims, "nbf"),
		IssuedAt:  timeClaim(claims, "iat"),
		Alg:       header.Algorithm,
		KeyID:     header.KeyID,
		Claims:    claims,
	}
}

func (v *Verifier) checkClaims(tok *IDToken) error {
	now := v.now()
	leeway := v.leeway

	if !tok.Expiry.IsZero() && now.After(tok.Expiry.Add(leeway)) {
		return fmt.Errorf("%w: token expired", ErrClaim)
	}
	if !tok.NotBefore.IsZero() && now.Add(leeway).Before(tok.NotBefore) {
		return fmt.Errorf("%w: token not yet valid", ErrClaim)
	}
	if !tok.IssuedAt.IsZero() && now.Add(leeway).Before(tok.IssuedAt) {
		return fmt.Errorf("%w: token issued in the future", ErrClaim)
	}
	for _, want := range v.audience {
		if !containsString(tok.Audience, want) {
			return fmt.Errorf("%w: missing audience %q", ErrClaim, want)
		}
	}
	if v.nonce != "" && tok.Nonce != v.nonce {
		return fmt.Errorf("%w: nonce mismatch", ErrClaim)
	}
	return nil
}

func stringClaim(claims map[string]any, name string) string {
	value, _ := claims[name].(string)
	return value
}

// audienceClaim accepts both the string and the string-array forms of "aud".
func audienceClaim(claims map[string]any, name string) []string {
	switch value := claims[name].(type) {
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []any:
		var out []string
		for _, item := range value {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func timeClaim(claims map[string]any, name string) time.Time {
	number, ok := claims[name].(float64)
	if !ok {
		return time.Time{}
	}
	secs := int64(number)
	return time.Unix(secs, int64((number-float64(secs))*float64(time.Second)))
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func encodeSegment(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
