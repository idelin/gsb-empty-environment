package jwtmw

import (
	"errors"
	"net/http"
	"strings"
)

// Middleware authenticates every request using a Bearer JWT. On success the
// validated claims are placed in the request context.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := bearerToken(r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, err)
			return
		}

		claims, err := v.Verify(r.Context(), token)
		if err != nil {
			writeVerifyError(w, err)
			return
		}

		ctx := contextWithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", ErrMalformedToken
	}
	const scheme = "Bearer "
	if len(header) < len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", ErrMalformedToken
	}
	token := strings.TrimSpace(header[len(scheme):])
	if token == "" || strings.Contains(token, " ") {
		return "", ErrMalformedToken
	}
	return token, nil
}

func writeVerifyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCacheExpired), errors.Is(err, ErrJWKSUnavailable):
		// The request may carry a perfectly valid token; the gateway simply
		// cannot verify it because the key service is down and the cache stale.
		writeAuthError(w, http.StatusServiceUnavailable, err)
	default:
		writeAuthError(w, http.StatusUnauthorized, err)
	}
}

func writeAuthError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="jwt"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + sanitize(err.Error()) + `"}`))
}

func sanitize(msg string) string {
	replacer := strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\n", " ", "\r", " ")
	return replacer.Replace(msg)
}
