package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const maxEvidenceNesting = 8

// decodeEvidenceValue accepts a raw COSE_Sign1, a CCA collection/map, a
// decoded claims map, or a struct with claim-shaped fields. It returns a realm
// claim set, a platform claim set, or a single claim set.
func decodeEvidenceValue(v any) (*claimSet, *claimSet, *claimSet, error) {
	return decodeEvidenceValueDepth(v, 0)
}

func decodeEvidenceValueDepth(v any, depth int) (*claimSet, *claimSet, *claimSet, error) {
	if depth > maxEvidenceNesting {
		return nil, nil, nil, fmt.Errorf("%w: evidence nesting too deep", ErrMalformedToken)
	}
	switch x := v.(type) {
	case []byte:
		return decodeEvidenceBytes(x, depth)
	case json.RawMessage:
		return decodeEvidenceBytes(x, depth)
	case string:
		return decodeEvidenceString(x, depth)
	case []any:
		return interpretDecoded(x, depth+1)
	default:
		m, err := claimsFromAny(v)
		if err != nil {
			return nil, nil, nil, err
		}
		return interpretMap(m, depth)
	}
}

func decodeEvidenceString(s string, depth int) (*claimSet, *claimSet, *claimSet, error) {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return decodeEvidenceBytes([]byte(trimmed), depth)
	}
	if decoded, ok := decodeBase64(trimmed); ok {
		return decodeEvidenceBytes(decoded, depth+1)
	}
	return nil, nil, nil, fmt.Errorf("%w: token string is not JSON or base64-encoded CBOR", ErrMalformedToken)
}

func decodeEvidenceBytes(data []byte, depth int) (*claimSet, *claimSet, *claimSet, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: empty token", ErrMalformedToken)
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		m, err := decodeJSONClaims(trimmed)
		if err == nil {
			return interpretMap(m, depth+1)
		}
		// A leading '[' can also be valid CBOR; fall through to CBOR.
	}

	decoded, err := decodeCBOR(trimmed)
	if err != nil {
		return nil, nil, nil, err
	}
	return interpretDecoded(decoded, depth+1)
}

func interpretDecoded(v any, depth int) (*claimSet, *claimSet, *claimSet, error) {
	if depth > maxEvidenceNesting {
		return nil, nil, nil, fmt.Errorf("%w: evidence nesting too deep", ErrMalformedToken)
	}
	switch x := v.(type) {
	case []byte:
		return decodeEvidenceBytes(x, depth+1)
	case string:
		return decodeEvidenceString(x, depth+1)
	case map[any]any:
		return interpretMap(x, depth+1)
	case []any:
		if len(x) == 2 {
			if realm, platform, ok, err := interpretEvidencePair(x, depth+1); err == nil && ok {
				return realm, platform, nil, nil
			}
		}
		cs, err := parseCOSEArray(x, depth+1)
		return nil, nil, cs, err
	default:
		return nil, nil, nil, fmt.Errorf("%w: unexpected decoded token type %T", ErrMalformedToken, v)
	}
}

// interpretEvidencePair accepts a two-element collection (either order) in
// addition to the standard CCA CBOR map collection.
func interpretEvidencePair(arr []any, depth int) (*claimSet, *claimSet, bool, error) {
	r1, p1, c1, err := decodeEvidenceValueDepth(arr[0], depth+1)
	if err != nil {
		return nil, nil, false, err
	}
	r2, p2, c2, err := decodeEvidenceValueDepth(arr[1], depth+1)
	if err != nil {
		return nil, nil, false, err
	}

	first := onlyClaimSet(r1, p1, c1)
	second := onlyClaimSet(r2, p2, c2)
	if first == nil || second == nil {
		return nil, nil, false, fmt.Errorf("%w: two-element collection must contain two claim sets", ErrMalformedToken)
	}

	if r1 != nil && p2 != nil {
		return r1, p2, true, nil
	}
	if p1 != nil && r2 != nil {
		return r2, p1, true, nil
	}

	rs1, ps1 := first.realmScore(), first.platformScore()
	rs2, ps2 := second.realmScore(), second.platformScore()
	switch {
	case rs1 > ps1 && ps2 >= rs2:
		return first, second, true, nil
	case rs2 > ps2 && ps1 >= rs1:
		return second, first, true, nil
	default:
		return first, second, true, nil
	}
}

func onlyClaimSet(realm, platform, claims *claimSet) *claimSet {
	if claims != nil {
		return claims
	}
	if realm != nil && platform == nil {
		return realm
	}
	if platform != nil && realm == nil {
		return platform
	}
	return nil
}

func interpretMap(m map[any]any, depth int) (*claimSet, *claimSet, *claimSet, error) {
	var platformValue, realmValue any
	hasPlatformLabel := false
	hasRealmLabel := false

	for k, v := range m {
		name, ok := canonicalClaimKey(k)
		if !ok {
			continue
		}
		switch name {
		case claimWrapperPlatformToken:
			hasPlatformLabel = true
			platformValue = v
		case claimWrapperRealmToken:
			hasRealmLabel = true
			realmValue = v
		}
	}

	// A CCA collection always carries the platform token label (key 44234 /
	// "cca-platform-token"). Requiring it avoids mistaking a realm claim that
	// happens to use key 44241 for a collection wrapper.
	if hasPlatformLabel {
		platform, err := decodeNestedSingleClaimSet(platformValue, depth+1)
		if err != nil {
			return nil, nil, nil, err
		}
		var realm *claimSet
		if hasRealmLabel {
			realm, err = decodeNestedSingleClaimSet(realmValue, depth+1)
			if err != nil {
				return nil, nil, nil, err
			}
		}
		if platform == nil && realm == nil {
			return nil, nil, nil, fmt.Errorf("%w: empty CCA collection", ErrMalformedToken)
		}
		return realm, platform, nil, nil
	}

	cs, err := newClaimSet(m)
	if err != nil {
		return nil, nil, nil, err
	}
	return nil, nil, cs, nil
}

func decodeNestedSingleClaimSet(v any, depth int) (*claimSet, error) {
	realm, platform, claims, err := decodeEvidenceValueDepth(v, depth+1)
	if err != nil {
		return nil, err
	}
	if claims != nil {
		return claims, nil
	}
	if realm != nil && platform == nil {
		return realm, nil
	}
	if platform != nil && realm == nil {
		return platform, nil
	}
	return nil, fmt.Errorf("%w: nested token must contain one claim set", ErrMalformedToken)
}

func parseCOSEArray(arr []any, depth int) (*claimSet, error) {
	if len(arr) != 4 {
		return nil, fmt.Errorf("%w: COSE_Sign1 must have four elements", ErrMalformedToken)
	}
	if _, err := singleOctets(arr[0]); err != nil {
		return nil, fmt.Errorf("%w: invalid COSE protected header: %v", ErrMalformedToken, err)
	}
	switch arr[1].(type) {
	case map[any]any, nil:
		// Unprotected headers are irrelevant after signature verification,
		// but must still be structurally plausible.
	default:
		return nil, fmt.Errorf("%w: invalid COSE unprotected header", ErrMalformedToken)
	}
	payload, err := singleOctets(arr[2])
	if err != nil {
		return nil, fmt.Errorf("%w: COSE payload is detached or invalid", ErrMalformedToken)
	}
	if _, err := singleOctets(arr[3]); err != nil {
		return nil, fmt.Errorf("%w: invalid COSE signature element", ErrMalformedToken)
	}

	decoded, err := decodeCBOR(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid COSE payload: %v", ErrMalformedToken, err)
	}

	switch claims := decoded.(type) {
	case map[any]any:
		return newClaimSet(claims)
	case []byte:
		return decodeNestedSingleClaimSet(claims, depth+1)
	default:
		return nil, fmt.Errorf("%w: COSE payload is not a claims map", ErrMalformedToken)
	}
}
