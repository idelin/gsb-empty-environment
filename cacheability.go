package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
)

// Decision explains whether a response may enter the shared cache and, if so,
// under which constraints. The decision is derived solely from the request
// method, the response status code and its Cache-Control, Vary, Set-Cookie
// and Expires headers. The request URL path and its extension never matter.
type Decision struct {
	// Storable is false when the response must not enter the shared cache.
	Storable bool
	// Reason is the machine-readable explanation for the decision.
	Reason string
	// MustRevalidate means the response may be kept, but every later hit
	// requires origin revalidation (no-cache / max-age=0 / must-revalidate).
	MustRevalidate bool
	// VaryFields are the request header field names that partition the cache
	// entry, normalized to lower case.
	VaryFields []string
	// NoTransform mirrors Cache-Control: no-transform for the edge to honor.
	NoTransform bool
}

// Stable, machine-readable reason codes returned in Decision.Reason.
const (
	ReasonStorable                  = "storable"
	ReasonMethodNotCacheable        = "method-not-cacheable"
	ReasonNoStore                   = "no-store"
	ReasonPrivateDirective          = "private-cache-control"
	ReasonSetCookie                 = "set-cookie-private"
	ReasonVaryWildcard              = "vary-wildcard"
	ReasonCredentialedNoPublic      = "credentialed-without-public"
	ReasonStatusNotDefaultCacheable = "status-not-default-cacheable"
	ReasonNonCacheableStatus        = "non-cacheable-status"
)

// defaultCacheableStatus is the RFC 9111 section 4 list of status codes for
// which a heuristic cache may store a response even without explicit freshness.
var defaultCacheableStatus = map[int]bool{
	http.StatusOK:                true,
	http.StatusCreated:           true,
	http.StatusMovedPermanently:  true,
	http.StatusFound:             true,
	http.StatusPermanentRedirect: true,
	http.StatusNotModified:       true,
	http.StatusUnauthorized:      true,
	http.StatusNotFound:          true,
	http.StatusMethodNotAllowed:  true,
	http.StatusGone:              true,
	http.StatusRequestURITooLong: true,
	http.StatusNotImplemented:    true,
}

// Decide determines whether the response to req may be stored in the shared
// edge cache. The request path (including any ".css"/".html" suffix) and the
// Accept header are never consulted: only method, credentials and the response
// metadata drive the outcome.
func Decide(req *http.Request, status int, respHeaders http.Header) Decision {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return Decision{Storable: false, Reason: ReasonMethodNotCacheable}
	}

	cc := ParseCacheControl(respHeaders)

	// Hard private markers: these always keep a response out of the cache.
	// "private" wins over a contradictory "public" directive.
	if cc.NoStore {
		return Decision{Storable: false, Reason: ReasonNoStore}
	}
	if cc.Private {
		return Decision{Storable: false, Reason: ReasonPrivateDirective}
	}
	if len(respHeaders.Values("Set-Cookie")) > 0 {
		// A Set-Cookie establishes per-user state. Even "public" cannot
		// safely turn such a response into a shared, cross-user object.
		return Decision{Storable: false, Reason: ReasonSetCookie}
	}

	varyFields, wildcard := ParseVary(respHeaders)
	if wildcard {
		return Decision{Storable: false, Reason: ReasonVaryWildcard}
	}

	// A request carrying user credentials establishes a user context. The
	// response is shared-cacheable only when the origin explicitly labels it
	// public; otherwise it is treated as private content.
	if RequestHasCredentials(req) && !cc.Public {
		return Decision{Storable: false, Reason: ReasonCredentialedNoPublic}
	}

	explicitFreshness := cc.HasMaxAge || cc.HasSMaxAge ||
		respHeaders.Get("Expires") != "" || cc.MustRevalidate || cc.ProxyRevalidate
	if !defaultCacheableStatus[status] && !explicitFreshness {
		return Decision{Storable: false, Reason: ReasonStatusNotDefaultCacheable}
	}

	zeroFreshness := (cc.HasSMaxAge && cc.SMaxAge == 0) ||
		(!cc.HasSMaxAge && cc.HasMaxAge && cc.MaxAge == 0)
	mustRevalidate := cc.NoCache || cc.MustRevalidate || cc.ProxyRevalidate || zeroFreshness

	return Decision{
		Storable:       true,
		Reason:         ReasonStorable,
		MustRevalidate: mustRevalidate,
		VaryFields:     varyFields,
		NoTransform:    cc.NoTransform,
	}
}

// RequestHasCredentials reports whether req carries user-specific credentials
// in the Authorization or Cookie header fields.
func RequestHasCredentials(req *http.Request) bool {
	return req.Header.Get("Authorization") != "" || len(req.Header.Values("Cookie")) > 0
}

// PrimaryKey is the credential-independent cache key: method, scheme, host
// and path. It deliberately excludes the path extension semantics and the
// Accept header, because those never alter cacheability.
func PrimaryKey(req *http.Request) string {
	scheme := req.URL.Scheme
	if scheme == "" {
		if req.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	var b strings.Builder
	b.WriteString(req.Method)
	b.WriteByte(' ')
	b.WriteString(scheme)
	b.WriteString("://")
	b.WriteString(strings.ToLower(host))
	b.WriteString(req.URL.Path)
	if req.URL.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(req.URL.RawQuery)
	}
	return b.String()
}

// VariantKey captures the Vary-selected request header values for a response,
// so negotiated variants do not collide in the shared cache.
func VariantKey(req *http.Request, varyFields []string) string {
	var b strings.Builder
	for _, field := range varyFields {
		b.WriteString(field)
		b.WriteString("=")
		values := req.Header.Values(http.CanonicalHeaderKey(field))
		if len(values) == 0 {
			b.WriteString("\x00")
		} else {
			b.WriteString(strings.Join(values, ","))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// CredentialFingerprint binds a stored public-on-origin response to the exact
// credential context that produced it. Credential material is hashed so it is
// not retained in plaintext inside the cache index. The fingerprint is never
// part of the primary key, which keeps per-user private objects from ever
// populating the shared namespace.
func CredentialFingerprint(req *http.Request) string {
	if !RequestHasCredentials(req) {
		return ""
	}
	parts := append(append([]string{}, req.Header.Values("Authorization")...),
		req.Header.Values("Cookie")...)
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
