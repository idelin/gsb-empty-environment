package netsec

import (
	"errors"
	"net/http"
	"strings"
)

const (
	// HeaderAuthUser is the trusted user identity header set after successful
	// authentication.
	HeaderAuthUser = "X-Auth-User"
	// HeaderAuthGroups is the trusted group identity header set after
	// successful authentication.
	HeaderAuthGroups = "X-Auth-Groups"
)

var (
	// ErrIdentityHeaderInConnection indicates a request tried to have an
	// internal identity header treated as hop-by-hop.
	ErrIdentityHeaderInConnection = errors.New("netsec: connection header names an internal identity header")

	hopByHopHeaders = []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Proxy-Connection",
		"TE",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	}
)

// SanitizeMiddleware strips inbound identity and hop-by-hop data before the
// next handler runs. It is the first stage of every Chain and is exported for
// composition in other reverse-proxy pipelines.
//
// Requests whose Connection header names X-Auth-User or X-Auth-Groups are
// rejected with 400 Bad Request.
func SanitizeMiddleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sanitizeRequest(r); err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SanitizeRequest removes inbound internal identity headers and hop-by-hop
// request data in place. It returns ErrIdentityHeaderInConnection when the
// Connection header names an internal identity header.
func SanitizeRequest(request *http.Request) error {
	return sanitizeRequest(request)
}

func sanitizeMiddleware(next http.Handler) http.Handler {
	return SanitizeMiddleware(next)
}

func sanitizeRequest(r *http.Request) error {
	if r == nil {
		return errors.New("netsec: nil request")
	}

	connectionHeaders := headerValues(r.Header, "Connection")
	for _, value := range connectionHeaders {
		for _, token := range splitHeaderList(value) {
			if isIdentityHeader(token) {
				return ErrIdentityHeaderInConnection
			}
		}
	}

	// Remove headers explicitly nominated by Connection before removing the
	// Connection header itself.
	for _, value := range connectionHeaders {
		for _, token := range splitHeaderList(value) {
			deleteHeader(r.Header, token)
		}
	}
	for _, name := range hopByHopHeaders {
		deleteHeader(r.Header, name)
	}

	// Internal identity is established only by the authentication stage.
	deleteIdentityHeaders(r.Header)

	// These fields carry the same hop-by-hop semantics as their corresponding
	// headers and must not survive the sanitizer.
	r.TransferEncoding = nil
	r.Trailer = nil
	r.Close = false

	return nil
}

func headerValues(header http.Header, name string) []string {
	var values []string
	for existing, existingValues := range header {
		if strings.EqualFold(existing, name) {
			values = append(values, existingValues...)
		}
	}
	return values
}

func deleteHeader(header http.Header, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for existing := range header {
		if strings.EqualFold(existing, name) {
			delete(header, existing)
		}
	}
}

func splitHeaderList(value string) []string {
	parts := strings.Split(value, ",")
	tokens := parts[:0]
	for _, part := range parts {
		if token := strings.TrimSpace(part); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}
