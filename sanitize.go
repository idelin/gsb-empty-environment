package netsec

import (
	"net/http"
	"strings"
)

// InternalIdentityHeaders are set exclusively by the inject stage. Any
// externally supplied copies are removed during sanitization.
var InternalIdentityHeaders = []string{
	"X-Auth-User",
	"X-Auth-Groups",
}

// hopByHopHeaders are connection-scoped headers that a proxy must not
// forward, per RFC 9110 section 7.6.1.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// sanitizeMiddleware strips forged internal identity headers and
// hop-by-hop headers from inbound requests. A request whose Connection
// header names an internal identity header is rejected with 400: that is
// an attempt to have the proxy treat the injected identity as a
// hop-by-hop header and delete it after injection.
func sanitizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		named := connectionTokens(r.Header)
		for _, token := range named {
			if isInternalIdentityHeader(token) {
				http.Error(w, "Connection header must not name internal identity headers", http.StatusBadRequest)
				return
			}
		}

		for _, token := range named {
			r.Header.Del(token)
		}
		for _, h := range hopByHopHeaders {
			r.Header.Del(h)
		}
		for _, h := range InternalIdentityHeaders {
			r.Header.Del(h)
		}

		next.ServeHTTP(w, r)
	})
}

// connectionTokens returns the header names listed in the Connection
// header, canonicalized.
func connectionTokens(h http.Header) []string {
	var tokens []string
	for _, value := range h.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token != "" {
				tokens = append(tokens, http.CanonicalHeaderKey(token))
			}
		}
	}
	return tokens
}

func isInternalIdentityHeader(name string) bool {
	for _, h := range InternalIdentityHeaders {
		if strings.EqualFold(name, h) {
			return true
		}
	}
	return false
}
