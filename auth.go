package netsec

import (
	"context"
	"net/http"
)

// Identity is the authenticated principal injected into the request.
type Identity struct {
	User   string
	Groups []string
}

// Authenticator verifies a request. A non-nil error rejects the request
// with 401 before the inject stage runs, so the response never carries
// injected identity headers.
type Authenticator interface {
	Authenticate(r *http.Request) (Identity, error)
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(r *http.Request) (Identity, error)

// Authenticate implements Authenticator.
func (f AuthenticatorFunc) Authenticate(r *http.Request) (Identity, error) {
	return f(r)
}

type identityContextKey struct{}

// IdentityFromContext returns the authenticated identity stored by the
// auth stage, if any.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityContextKey{}).(Identity)
	return id, ok
}

// authMiddleware runs the authenticator. On failure it writes 401 and
// stops the chain, so injection never happens for rejected requests.
func authMiddleware(auth Authenticator) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := auth.Authenticate(r)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), identityContextKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// injectMiddleware writes the authenticated identity into the internal
// identity headers. Sanitization has already removed any external copies,
// so these values are authoritative.
func injectMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFromContext(r.Context())
		if !ok {
			http.Error(w, "missing authenticated identity", http.StatusInternalServerError)
			return
		}
		r.Header.Set("X-Auth-User", id.User)
		r.Header.Del("X-Auth-Groups")
		for _, g := range id.Groups {
			r.Header.Add("X-Auth-Groups", g)
		}
		next.ServeHTTP(w, r)
	})
}
