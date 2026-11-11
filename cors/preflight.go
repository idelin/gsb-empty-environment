// Package cors implements a Fetch-standard compliant CORS layer for an
// http.RoundTripper, including the CORS-preflight cache.
//
// The preflight cache is keyed by the full identity tuple described in the
// Fetch standard (source origin, target origin, credentials mode) and each
// cached entry independently records the permitted methods and headers, so a
// decision taken for one identity, method or header set can never be reused by
// another.
package cors

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mode selects the credentials mode of a request, matching the Fetch
// standard's request credentials mode.
type Mode int

const (
	// ModeOmit sends no credentials and treats ACAO "*" as a wildcard.
	ModeOmit Mode = iota
	// ModeInclude sends credentials and requires an explicit, echoed origin.
	ModeInclude
)

// String returns the Fetch-standard name of the credentials mode.
func (m Mode) String() string {
	if m == ModeInclude {
		return "include"
	}
	return "omit"
}

// Policy carries the browsing-context identity associated with a request.
type Policy struct {
	// Origin is the source origin, e.g. "https://example.com".
	Origin string
	// Mode is the credentials mode of the request.
	Mode Mode
}

type policyKeyType struct{}

var policyKey policyKeyType

// WithPolicy attaches a CORS policy to the request context. Requests without
// a policy are executed without any CORS enforcement, matching a request made
// outside a browsing context.
func WithPolicy(ctx context.Context, p Policy) context.Context {
	return context.WithValue(ctx, policyKey, p)
}

func policyFromContext(ctx context.Context) (Policy, bool) {
	p, ok := ctx.Value(policyKey).(Policy)
	return p, ok
}

// maxPreflightTTL is the upper bound placed on Access-Control-Max-Age, per
// the Fetch standard (7200 seconds).
const maxPreflightTTL = 7200 * time.Second

// defaultPreflightTTL is used when Access-Control-Max-Age is absent.
const defaultPreflightTTL = 5 * time.Second

var safelistedMethods = map[string]bool{
	http.MethodGet:  true,
	http.MethodHead: true,
	http.MethodPost: true,
}

var safelistedHeaders = map[string]bool{
	"accept":           true,
	"accept-language":  true,
	"content-language": true,
	"content-type":     true,
	"range":            true,
}

// forbiddenRequestHeaders are headers the user agent controls and removes or
// generates itself (Fetch standard, "forbidden request-header name"). They
// are never part of the CORS check, including the Referer that net/http adds
// when following a cross-origin redirect.
var forbiddenRequestHeaders = map[string]bool{
	"accept-charset":                 true,
	"accept-encoding":                true,
	"access-control-request-headers": true,
	"access-control-request-method":  true,
	"connection":                     true,
	"content-length":                 true,
	"cookie":                         true,
	"cookie2":                        true,
	"date":                           true,
	"dnt":                            true,
	"expect":                         true,
	"feature-policy":                 true,
	"host":                           true,
	"keep-alive":                     true,
	"origin":                         true,
	"referer":                        true,
	"set-cookie":                     true,
	"te":                             true,
	"trailer":                        true,
	"transfer-encoding":              true,
	"upgrade":                        true,
	"via":                            true,
}

var safelistedContentTypes = map[string]bool{
	"application/x-www-form-urlencoded": true,
	"multipart/form-data":               true,
	"text/plain":                        true,
}

// PreflightError describes a failed CORS preflight. Negative results are never
// cached.
type PreflightError struct {
	Reason string
}

func (e *PreflightError) Error() string {
	return "cors: preflight failed: " + e.Reason
}

// cacheKey identifies a preflight cache partition. Two requests share a
// cached decision only when every component matches.
type cacheKey struct {
	origin      string
	target      string
	credentials string
}

type cacheEntry struct {
	methods      map[string]bool
	headers      map[string]bool
	wildcardHead bool
	expiresAt    time.Time
}

func (e *cacheEntry) valid(now time.Time) bool {
	return now.Before(e.expiresAt)
}

// allows reports whether the entry permits the given method and headers,
// evaluated against the credentials mode in effect when the entry was stored.
func (e *cacheEntry) allows(method string, headers []string, credentials Mode) bool {
	if !e.methods[strings.ToUpper(method)] {
		return false
	}
	for _, header := range headers {
		header = strings.ToLower(header)
		if e.wildcardHead && credentials == ModeOmit {
			continue
		}
		if !e.headers[header] {
			return false
		}
	}
	return true
}

type inflight struct {
	done chan struct{}
}

// Config configures a CORSTransport.
type Config struct {
	// Base is the underlying transport. http.DefaultTransport is used when nil.
	Base http.RoundTripper
	// Clock returns the current time. time.Now is used when nil. Tests may
	// inject a controllable clock to exercise expiry deterministically.
	Clock func() time.Time
}

// CORSTransport is an http.RoundTripper that enforces CORS, including
// preflights and the preflight cache, for requests carrying a Policy.
type CORSTransport struct {
	base http.RoundTripper
	now  func() time.Time

	mu       sync.Mutex
	cache    map[cacheKey]*cacheEntry
	inflight map[cacheKey]*inflight
}

// NewCORSTransport builds a CORSTransport from the supplied configuration.
func NewCORSTransport(cfg Config) *CORSTransport {
	base := cfg.Base
	if base == nil {
		base = http.DefaultTransport
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &CORSTransport{
		base:     base,
		now:      clock,
		cache:    make(map[cacheKey]*cacheEntry),
		inflight: make(map[cacheKey]*inflight),
	}
}

// RoundTrip implements http.RoundTripper.
func (t *CORSTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	policy, ok := policyFromContext(req.Context())
	if !ok {
		return t.base.RoundTrip(req)
	}

	targetOrigin := originOf(req.URL)
	if targetOrigin == "" {
		return nil, &PreflightError{Reason: "request URL has no usable origin"}
	}

	// Every request, including each hop of a redirect, carries the source
	// identity. Go's client invokes the transport once per redirect hop, so a
	// redirect to another target is evaluated with its own cache key and can
	// never inherit the prior hop's decision.
	if policy.Origin != "" && !strings.EqualFold(policy.Origin, targetOrigin) {
		req.Header.Set("Origin", policy.Origin)
		if needsPreflight(req) {
			if err := t.ensurePreflight(policy, targetOrigin, req); err != nil {
				return nil, err
			}
		}
	}
	return t.base.RoundTrip(req)
}

func needsPreflight(req *http.Request) bool {
	if !safelistedMethods[req.Method] {
		return true
	}
	return len(nonSafelistedHeaders(req.Header)) > 0
}

func nonSafelistedHeaders(h http.Header) []string {
	seen := make(map[string]bool)
	for key, values := range h {
		name := strings.ToLower(key)
		if forbiddenRequestHeaders[name] {
			continue
		}
		if safelistedHeaders[name] {
			if name == "content-type" && len(values) > 0 {
				media := strings.ToLower(strings.TrimSpace(strings.Split(values[0], ";")[0]))
				if !safelistedContentTypes[media] {
					seen[name] = true
				}
			}
			continue
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		return nil
	}
	headers := make([]string, 0, len(seen))
	for name := range seen {
		headers = append(headers, name)
	}
	sort.Strings(headers)
	return headers
}

func (t *CORSTransport) ensurePreflight(policy Policy, targetOrigin string, req *http.Request) error {
	key := cacheKey{
		origin:      strings.ToLower(policy.Origin),
		target:      strings.ToLower(targetOrigin),
		credentials: policy.Mode.String(),
	}
	method := strings.ToUpper(req.Method)
	headers := nonSafelistedHeaders(req.Header)

	for {
		t.mu.Lock()
		if entry, ok := t.cache[key]; ok {
			if !entry.valid(t.now()) {
				// Lazy eviction keeps the cache bounded across long-lived sessions.
				delete(t.cache, key)
			} else if entry.allows(method, headers, policy.Mode) {
				t.mu.Unlock()
				return nil
			}
		}
		flight, waiting := t.inflight[key]
		if !waiting {
			flight = &inflight{done: make(chan struct{})}
			t.inflight[key] = flight
			t.mu.Unlock()

			entry, err := t.runPreflight(policy, req, method, headers)

			t.mu.Lock()
			delete(t.inflight, key)
			if err == nil {
				t.cache[key] = entry
			}
			close(flight.done)
			t.mu.Unlock()
			return err
		}
		t.mu.Unlock()

		// Coalesce concurrent requests sharing the exact same identity tuple.
		select {
		case <-flight.done:
		case <-req.Context().Done():
			return req.Context().Err()
		}
	}
}

func (t *CORSTransport) runPreflight(policy Policy, req *http.Request, method string, headers []string) (*cacheEntry, error) {
	preflight, err := http.NewRequestWithContext(
		req.Context(),
		http.MethodOptions,
		req.URL.Scheme+"://"+req.URL.Host,
		nil,
	)
	if err != nil {
		return nil, err
	}
	preflight.Header.Set("Origin", policy.Origin)
	preflight.Header.Set("Access-Control-Request-Method", method)
	if len(headers) > 0 {
		preflight.Header.Set("Access-Control-Request-Headers", strings.Join(headers, ", "))
	}

	resp, err := t.base.RoundTrip(preflight)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	entry, err := parsePreflightResponse(resp, policy, headers, t.now())
	if err != nil {
		return nil, err
	}
	if !entry.allows(method, headers, policy.Mode) {
		return nil, &PreflightError{Reason: fmt.Sprintf("method %q or requested headers not permitted", method)}
	}
	return entry, nil
}

func parsePreflightResponse(resp *http.Response, policy Policy, requestedHeaders []string, now time.Time) (*cacheEntry, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &PreflightError{Reason: fmt.Sprintf("server responded with status %d", resp.StatusCode)}
	}

	allowOrigin := resp.Header.Get("Access-Control-Allow-Origin")
	switch {
	case allowOrigin == "*" && policy.Mode == ModeOmit:
	case allowOrigin == policy.Origin && policy.Origin != "":
	default:
		return nil, &PreflightError{Reason: "Access-Control-Allow-Origin does not match the request origin"}
	}
	if policy.Mode == ModeInclude {
		if creds := resp.Header.Get("Access-Control-Allow-Credentials"); !strings.EqualFold(creds, "true") {
			return nil, &PreflightError{Reason: "Access-Control-Allow-Credentials required for credentialed requests"}
		}
	}

	entry := &cacheEntry{
		methods:   make(map[string]bool),
		headers:   make(map[string]bool),
		expiresAt: now.Add(defaultPreflightTTL),
	}

	if methods := resp.Header.Values("Access-Control-Allow-Methods"); len(methods) > 0 {
		for _, allowed := range splitHeaderValues(methods) {
			entry.methods[strings.ToUpper(allowed)] = true
		}
	} else if m := resp.Request.Header.Get("Access-Control-Request-Method"); m != "" {
		// An omitted ACA-methods header grants only the requested method (it has
		// no wildcard form in the Fetch standard).
		entry.methods[strings.ToUpper(m)] = true
	}

	rawHeaders := resp.Header.Values("Access-Control-Allow-Headers")
	if len(rawHeaders) > 0 {
		for _, allowed := range splitHeaderValues(rawHeaders) {
			allowed = strings.ToLower(allowed)
			if allowed == "*" {
				entry.wildcardHead = true
				continue
			}
			entry.headers[allowed] = true
		}
	} else {
		// An omitted ACA-headers header is equivalent to "*" in the Fetch
		// standard, so any header is permitted in anonymous mode.
		entry.wildcardHead = true
	}

	if maxAge := strings.TrimSpace(resp.Header.Get("Access-Control-Max-Age")); maxAge != "" {
		seconds, err := time.ParseDuration(maxAge + "s")
		if err != nil || seconds < 0 {
			return nil, &PreflightError{Reason: "invalid Access-Control-Max-Age"}
		}
		if seconds > maxPreflightTTL {
			seconds = maxPreflightTTL
		}
		entry.expiresAt = now.Add(seconds)
	}

	return entry, nil
}

func splitHeaderValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func originOf(u *url.URL) string {
	if u == nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Invalidate drops the cached preflight decision for one identity tuple. It is
// used to react to a policy revocation so a later request re-validates with
// the server.
func (t *CORSTransport) Invalidate(origin, target string, mode Mode) {
	key := cacheKey{
		origin:      strings.ToLower(origin),
		target:      strings.ToLower(target),
		credentials: mode.String(),
	}
	t.mu.Lock()
	delete(t.cache, key)
	t.mu.Unlock()
}

// InvalidateOrigin drops every cached decision involving the given source
// origin, across targets and credentials modes.
func (t *CORSTransport) InvalidateOrigin(origin string) {
	origin = strings.ToLower(origin)
	t.mu.Lock()
	for key := range t.cache {
		if key.origin == origin {
			delete(t.cache, key)
		}
	}
	t.mu.Unlock()
}

// Clear empties the entire preflight cache.
func (t *CORSTransport) Clear() {
	t.mu.Lock()
	t.cache = make(map[cacheKey]*cacheEntry)
	t.mu.Unlock()
}

// EntryCount reports the number of currently cached entries, including entries
// that have already expired but not yet been evicted. It is primarily intended
// for tests and diagnostics.
func (t *CORSTransport) EntryCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.cache)
}
