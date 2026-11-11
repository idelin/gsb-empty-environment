package signedlink

import (
	"net/http"
	"time"
)

// Middleware returns an http.Handler that verifies the request signature
// before delegating to next. Rejected requests get 403 and never reach the
// media handler. The signature binds the request method and the escaped
// path, so a signature minted for "GET /a/b" authorizes nothing else.
func Middleware(ks *KeyStore, next http.Handler) http.Handler {
	return MiddlewareWithClock(ks, time.Now, next)
}

// MiddlewareWithClock is Middleware with an injectable clock for tests.
func MiddlewareWithClock(ks *KeyStore, now func() time.Time, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := ks.Verify(r.Method, r.URL.EscapedPath(), r.URL.Query(), now()); err != nil {
			http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
