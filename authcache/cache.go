// Package authcache provides an importable cache for subrequest authorization
// results, modelling the Nginx auth_request directive with result caching.
//
// A cache key is never just the request path. It binds the normalized method,
// host, path and query, a SHA-256 fingerprint of the Authorization header, a
// SHA-256 fingerprint of the session cookie, and the Vary header values
// declared by the authorization service. Requests carrying neither an
// Authorization header nor a session cookie are never cached, which prevents
// one principal from being served another principal's allow decision.
package authcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Authorizer performs the authorization subrequest for r. It mirrors the
// Nginx auth_request contract: the response status decides whether the
// original request is allowed (2xx), rejected (401/403), or failed (5xx).
type Authorizer interface {
	Authorize(r *http.Request) (*http.Response, error)
}

// AuthorizerFunc adapts a function to the Authorizer interface.
type AuthorizerFunc func(r *http.Request) (*http.Response, error)

func (f AuthorizerFunc) Authorize(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Config configures a Cache.
type Config struct {
	// Authorizer performs cache-miss authorization subrequests. Required.
	Authorizer Authorizer
	// SessionCookie is the cookie treated as the session credential.
	// Defaults to "session".
	SessionCookie string
	// Now is the clock used for expiry. Defaults to time.Now.
	Now func() time.Time
}

type entry struct {
	key        string
	status     int
	header     http.Header
	body       []byte
	varyValues []string
	expiresAt  time.Time
}

// Cache caches authorization results keyed by request-bound fingerprints.
// It is safe for concurrent use.
type Cache struct {
	authorizer    Authorizer
	sessionCookie string
	now           func() time.Time

	mu       sync.RWMutex
	variants map[string]map[string]*entry // base key -> full key -> entry
}

// New builds a Cache from cfg.
func New(cfg Config) *Cache {
	if cfg.Authorizer == nil {
		panic("authcache: Config.Authorizer is required")
	}
	sessionCookie := cfg.SessionCookie
	if sessionCookie == "" {
		sessionCookie = "session"
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Cache{
		authorizer:    cfg.Authorizer,
		sessionCookie: sessionCookie,
		now:           now,
		variants:      make(map[string]map[string]*entry),
	}
}

// Authorize returns a cached authorization response when available, otherwise
// invokes the Authorizer and stores any cacheable response. The returned
// response body is always a fresh, non-nil io.ReadCloser.
func (c *Cache) Authorize(r *http.Request) (*http.Response, error) {
	normalized, err := NormalizePath(r.URL.EscapedPath())
	if err != nil {
		return nil, err
	}

	base := c.baseKey(r, normalized)
	if e := c.lookup(base, r); e != nil {
		return e.response(), nil
	}

	resp, err := c.authorizer.Authorize(r)
	if err != nil {
		return nil, err
	}
	c.store(base, r, resp)
	return resp, nil
}

// BaseKey exposes the request portion of the key (everything except the
// authorization-service Vary selection) for diagnostics and tests.
func (c *Cache) BaseKey(r *http.Request) (string, error) {
	normalized, err := NormalizePath(r.URL.EscapedPath())
	if err != nil {
		return "", err
	}
	return c.baseKey(r, normalized), nil
}

// Cleanup removes expired entries and returns how many were evicted.
func (c *Cache) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	evicted := 0
	for base, variants := range c.variants {
		for fullKey, e := range variants {
			if !now.Before(e.expiresAt) {
				delete(variants, fullKey)
				evicted++
			}
		}
		if len(variants) == 0 {
			delete(c.variants, base)
		}
	}
	return evicted
}

// StartCleanup periodically removes expired entries until stop is closed.
// It is intended for long-lived gateways; callers own the returned stop
// channel and close it to terminate the background goroutine.
func (c *Cache) StartCleanup(interval time.Duration) (stop chan<- struct{}) {
	if interval <= 0 {
		panic("authcache: cleanup interval must be positive")
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Cleanup()
			case <-done:
				return
			}
		}
	}()
	return done
}

func (c *Cache) baseKey(r *http.Request, normalizedPath string) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteByte(0)
	b.WriteString(r.Host)
	b.WriteByte(0)
	b.WriteString(normalizedPath)
	b.WriteByte(0)
	b.WriteString(r.URL.RawQuery)
	b.WriteByte(0)
	b.WriteString(fingerprint(r.Header.Get("Authorization")))
	b.WriteByte(0)
	b.WriteString(fingerprint(c.sessionValue(r)))
	return b.String()
}

func (c *Cache) fullKey(base string, vary []string, r *http.Request) string {
	var b strings.Builder
	b.WriteString(base)
	for _, header := range vary {
		b.WriteByte(0)
		b.WriteString(header)
		b.WriteByte(1)
		b.WriteString(r.Header.Get(header))
	}
	return b.String()
}

func (c *Cache) sessionValue(r *http.Request) string {
	cookie, err := r.Cookie(c.sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// hasCredentials reports whether r carries an Authorization header or the
// configured session cookie. Requests with neither are never cached.
func (c *Cache) hasCredentials(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}
	return c.sessionValue(r) != ""
}

func (c *Cache) lookup(base string, r *http.Request) *entry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	variants, ok := c.variants[base]
	if !ok {
		return nil
	}
	now := c.now()
	for _, e := range variants {
		if !now.Before(e.expiresAt) {
			continue
		}
		fullKey := c.fullKey(base, e.varyValues, r)
		if e.key == fullKey {
			return e
		}
	}
	return nil
}

func (c *Cache) store(base string, r *http.Request, resp *http.Response) {
	if !c.hasCredentials(r) {
		return
	}

	ttl, hasMaxAge, vary, wildcard, noStore := cacheControl(resp)
	if wildcard || noStore {
		return
	}
	ttl = ttlForStatus(resp.StatusCode, ttl, hasMaxAge)
	if ttl <= 0 {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))

	fullKey := c.fullKey(base, vary, r)
	e := &entry{
		key:        fullKey,
		status:     resp.StatusCode,
		header:     resp.Header.Clone(),
		body:       body,
		varyValues: vary,
		expiresAt:  c.now().Add(ttl),
	}

	c.mu.Lock()
	variants := c.variants[base]
	if variants == nil {
		variants = make(map[string]*entry)
		c.variants[base] = variants
	}
	variants[fullKey] = e
	c.mu.Unlock()
}

// ttlForStatus encodes the status policy:
//   - 200: the service max-age, capped at 60 seconds; 60 seconds without one.
//   - 403: the service max-age, capped at 5 seconds; 5 seconds without one.
//   - 401 and 5xx: never cached.
//
// An explicit max-age of 0 means "revalidate immediately" and is not replaced
// by the default TTL.
func ttlForStatus(status int, ttl time.Duration, hasMaxAge bool) time.Duration {
	switch status {
	case http.StatusOK:
		if !hasMaxAge {
			return 60 * time.Second
		}
		if ttl > 60*time.Second {
			return 60 * time.Second
		}
		return ttl
	case http.StatusForbidden:
		if !hasMaxAge {
			return 5 * time.Second
		}
		if ttl > 5*time.Second {
			return 5 * time.Second
		}
		return ttl
	default:
		return 0
	}
}

// cacheControl parses the response directives. A no-store/no-cache response,
// or a Vary: * response, is not cacheable. maxAge is zero when no max-age is
// declared.
func cacheControl(resp *http.Response) (maxAge time.Duration, hasMaxAge bool, vary []string, wildcard bool, noStore bool) {
	for _, directive := range splitHeader(resp.Header.Values("Cache-Control")) {
		directive = strings.ToLower(strings.TrimSpace(directive))
		if directive == "no-store" || directive == "no-cache" {
			return 0, false, nil, false, true
		}
		if value, ok := strings.CutPrefix(directive, "max-age="); ok {
			if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
				maxAge = time.Duration(seconds) * time.Second
				hasMaxAge = true
			}
		}
	}

	seen := make(map[string]bool)
	for _, field := range splitHeader(resp.Header.Values("Vary")) {
		field = strings.TrimSpace(field)
		if field == "*" {
			return 0, false, nil, true, false
		}
		canonical := http.CanonicalHeaderKey(field)
		if canonical != "" && !seen[canonical] {
			seen[canonical] = true
			vary = append(vary, canonical)
		}
	}
	return maxAge, hasMaxAge, vary, false, false
}

func splitHeader(values []string) []string {
	var parts []string
	for _, value := range values {
		parts = append(parts, strings.Split(value, ",")...)
	}
	return parts
}

func (e *entry) response() *http.Response {
	return &http.Response{
		StatusCode: e.status,
		Header:     e.header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(e.body)),
	}
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
