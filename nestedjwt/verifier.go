package nestedjwt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// New builds a Verifier from a KeyResolver and functional options.
func New(keys KeyResolver, opts ...Option) *Verifier {
	v := &Verifier{
		policy: DefaultPolicy(),
		keys:   keys,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(v)
	}
	if v.policy.MaxNestedDepth <= 0 {
		v.policy.MaxNestedDepth = 2
	}
	if v.policy.MaxTokenSize <= 0 {
		v.policy.MaxTokenSize = 64 * 1024
	}
	if v.store == nil && v.policy.EnableReplayStore {
		v.store = NewMemoryReplayStore(v.policy.ReplayTTL, v.policy.ReplayMaxEntries)
	}
	return v
}

type unwrapResult struct {
	claims      Claims
	innerHeader JOSEHeader
	outerHeader JOSEHeader
	depth       int
}

// Verify fully unwraps and validates a compact-serialized nested JWT and
// returns the authenticated claims together with the JOSE headers.
func (v *Verifier) Verify(token string) (*VerifiedToken, error) {
	if token == "" || len(token) > v.policy.MaxTokenSize || strings.TrimSpace(token) != token {
		return nil, fmt.Errorf("%w: token empty, oversized or contains whitespace", ErrMalformedToken)
	}

	result, err := v.unwrap(token, 0)
	if err != nil {
		return nil, err
	}

	if err := v.checkCrossLayer(result.outerHeader, result.innerHeader, result.claims); err != nil {
		return nil, err
	}
	if err := v.checkLifetime(result.claims); err != nil {
		return nil, err
	}
	if err := v.checkReplay(result.claims); err != nil {
		return nil, err
	}

	return &VerifiedToken{
		Claims:       result.claims,
		OuterHeader:  result.outerHeader,
		InnerHeader:  result.innerHeader,
		NestingDepth: result.depth,
	}, nil
}

// unwrap recursively peels compact JWE/JWS layers. depth is the number of
// token layers already entered; a valid sign-then-encrypt token reaches
// depth 2 (outer JWE at depth 0, inner JWS at depth 1, claims payload).
func (v *Verifier) unwrap(token string, depth int) (*unwrapResult, error) {
	if depth >= v.policy.MaxNestedDepth {
		return nil, fmt.Errorf("%w: depth >= %d", ErrNestedDepth, v.policy.MaxNestedDepth)
	}

	segments := strings.Split(token, ".")
	switch len(segments) {
	case 3:
		// A JWS is only accepted at the entry point when nested mode is off.
		if depth == 0 && v.policy.RequireNested {
			return nil, ErrBareJWSRejected
		}
		header, payload, err := v.verifyJWS(segments)
		if err != nil {
			return nil, err
		}
		if isCompactToken(payload) {
			// JWS wrapping another compact token would exceed the two-layer
			// bound for any policy accepted here; reject unconditionally.
			return nil, fmt.Errorf("%w: JWS payload is itself a compact token", ErrNestedDepth)
		}
		claims, err := parseClaims(payload)
		if err != nil {
			return nil, err
		}
		return &unwrapResult{
			claims:      claims,
			innerHeader: header,
			outerHeader: header,
			depth:       depth + 1,
		}, nil

	case 5:
		outerHeader, plaintext, err := v.openJWE(segments, depth == 0)
		if err != nil {
			return nil, err
		}
		plainSegments := strings.Split(string(plaintext), ".")
		if len(plainSegments) != 3 {
			if len(plainSegments) == 5 && depth+2 >= v.policy.MaxNestedDepth {
				return nil, fmt.Errorf("%w: nested JWE inside JWE reaches depth %d, cap is %d", ErrNestedDepth, depth+2, v.policy.MaxNestedDepth)
			}
			// Raw JSON or other non-JWS plaintext: the inner signature has
			// been stripped and arbitrary claims were encrypted directly; a
			// nested JWE that exceeds the depth cap is reported separately.
			return nil, fmt.Errorf("%w: decrypted payload must be a 3-segment JWS, got %d segments", ErrInnerNotJWS, len(plainSegments))
		}
		header, payload, err := v.verifyJWS(plainSegments)
		if err != nil {
			return nil, err
		}
		if isCompactToken(payload) {
			return nil, fmt.Errorf("%w: inner JWS payload is itself a compact token", ErrNestedDepth)
		}
		claims, err := parseClaims(payload)
		if err != nil {
			return nil, err
		}
		return &unwrapResult{
			claims:      claims,
			innerHeader: header,
			outerHeader: outerHeader,
			depth:       depth + 2,
		}, nil

	default:
		return nil, fmt.Errorf("%w: expected 3 (JWS) or 5 (JWE) compact segments, got %d", ErrMalformedToken, len(segments))
	}
}

// checkCrossLayer enforces iss/sub/aud agreement between the outer JWE
// protected header and the inner claims, plus kid binding between headers.
func (v *Verifier) checkCrossLayer(outer, inner JOSEHeader, claims Claims) error {
	if outer.Issuer != "" && outer.Issuer != claims.Issuer {
		return fmt.Errorf("%w: outer iss %q != inner iss %q", ErrCrossLayerClaim, outer.Issuer, claims.Issuer)
	}
	if outer.Subject != "" && outer.Subject != claims.Subject {
		return fmt.Errorf("%w: outer sub %q != inner sub %q", ErrCrossLayerClaim, outer.Subject, claims.Subject)
	}
	if len(outer.Audience) > 0 && !audienceEqual(outer.Audience, claims.Audience) {
		return fmt.Errorf("%w: outer aud %v != inner aud %v", ErrCrossLayerClaim, outer.Audience, claims.Audience)
	}
	if v.policy.MatchCrossLayerKID && outer.KeyID != "" && inner.KeyID != "" && outer.KeyID != inner.KeyID {
		return fmt.Errorf("%w: outer kid %q != inner kid %q", ErrCrossLayerClaim, outer.KeyID, inner.KeyID)
	}
	return nil
}

func (v *Verifier) checkLifetime(claims Claims) error {
	now := v.now().Unix()
	skew := int64(v.policy.ClockSkew / time.Second)
	if claims.ExpirationTime != 0 && now > claims.ExpirationTime+skew {
		return fmt.Errorf("%w: exp=%d", ErrTokenExpired, claims.ExpirationTime)
	}
	if claims.NotBefore != 0 && now+skew < claims.NotBefore {
		return fmt.Errorf("%w: nbf=%d", ErrTokenNotYetValid, claims.NotBefore)
	}
	return nil
}

func (v *Verifier) checkReplay(claims Claims) error {
	if !v.policy.RequireReplayClaims && v.store == nil {
		return nil
	}
	if claims.Issuer == "" || claims.JWTID == "" {
		return fmt.Errorf("%w: iss=%q jti=%q", ErrMissingReplayClaims, claims.Issuer, claims.JWTID)
	}
	if v.store == nil {
		return nil
	}
	seen, err := v.store.Seen(claims.Issuer, claims.JWTID)
	if err != nil {
		return fmt.Errorf("nestedjwt: replay store failure: %w", err)
	}
	if seen {
		return fmt.Errorf("%w: iss=%q jti=%q", ErrReplayed, claims.Issuer, claims.JWTID)
	}
	return nil
}

// parseProtectedHeader base64url-decodes and parses a JOSE protected header,
// retaining unknown parameters in JOSEHeader.Extra.
func parseProtectedHeader(encoded string) (JOSEHeader, map[string]any, error) {
	raw, err := b64.DecodeString(encoded)
	if err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: header is not base64url: %v", ErrProtectedHeader, err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: header is not a JSON object: %v", ErrProtectedHeader, err)
	}
	var header JOSEHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: header fields have wrong types: %v", ErrProtectedHeader, err)
	}
	known := map[string]struct{}{
		"alg": {}, "enc": {}, "kid": {}, "cty": {}, "zip": {}, "crit": {},
		"iss": {}, "sub": {}, "aud": {},
	}
	header.Extra = make(map[string]any)
	for key, value := range generic {
		if _, ok := known[key]; !ok {
			header.Extra[key] = value
		}
	}
	return header, generic, nil
}

func parseClaims(payload []byte) (Claims, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrPayloadNotJSON, err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrPayloadNotJSON, err)
	}
	claims.Raw = raw
	return claims, nil
}

// isCompactToken reports whether data looks like a compact JWS/JWE (2 or 4
// dots, all base64url segments). Empty payloads are never tokens.
func isCompactToken(data []byte) bool {
	text := string(data)
	if text == "" {
		return false
	}
	parts := strings.Split(text, ".")
	if len(parts) != 3 && len(parts) != 5 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := b64.DecodeString(part); err != nil {
			return false
		}
	}
	return true
}

func audienceEqual(a, b Audience) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, value := range a {
		counts[value]++
	}
	for _, value := range b {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}

func contains(list []string, want string) bool {
	for _, value := range list {
		if value == want {
			return true
		}
	}
	return false
}
