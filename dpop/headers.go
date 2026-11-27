package dpop

import (
	"net/http"
	"strings"
)

// parseAuthorization enforces a single, well-formed Authorization header and
// rejects header smuggling via multiple header lines or comma lists.
func parseAuthorization(r *http.Request) (scheme, token string, err error) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "", "", newError(ErrInvalidToken, "missing Authorization header")
	}
	if len(values) > 1 {
		return "", "", newError(ErrInvalidToken, "multiple Authorization headers are not allowed")
	}
	raw := strings.TrimSpace(values[0])
	if strings.ContainsAny(raw, ",") {
		return "", "", newError(ErrInvalidToken, "multiple authorization credentials in one header are not allowed")
	}
	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[1] == "" {
		return "", "", newError(ErrInvalidToken, "Authorization header must be '<scheme> <token>'")
	}
	scheme = fields[0]
	if !strings.EqualFold(scheme, "DPoP") && !strings.EqualFold(scheme, "Bearer") {
		return "", "", newError(ErrInvalidToken, "unsupported HTTP authentication scheme")
	}
	return scheme, fields[1], nil
}

// parseDPoPHeader enforces exactly one DPoP proof header.
func parseDPoPHeader(r *http.Request) (string, error) {
	// http.CanonicalHeaderKey canonicalizes "DPoP" to "Dpop".
	values := r.Header.Values("Dpop")
	if len(values) == 0 {
		return "", newError(ErrInvalidDPoP, "missing DPoP proof header")
	}
	if len(values) > 1 {
		return "", newError(ErrInvalidDPoP, "multiple DPoP proof headers are not allowed")
	}
	proof := strings.TrimSpace(values[0])
	if proof == "" || strings.ContainsAny(proof, ",") {
		return "", newError(ErrInvalidDPoP, "DPoP header must contain exactly one compact JWT")
	}
	return proof, nil
}
