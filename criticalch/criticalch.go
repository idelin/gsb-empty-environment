// Package criticalch implements the Critical-CH (critical client hints)
// handling that a Go reverse proxy needs in front of its retry and shared
// cache logic.
//
// The package enforces four security properties:
//
//   - A hint named by Critical-CH is honored only when the server also
//     advertises it in Accept-CH. Any other Critical-CH field is ignored.
//   - A retry happens only when the original request did not carry the hint
//     and the client is able to send it, and at most one retry is made for a
//     single navigation.
//   - A shared-cache key incorporates the values of every request header the
//     response lists in Vary.
//   - A response that depends on a Critical-CH hint missing from Vary is not
//     stored in the shared cache, and a response produced by a retry never
//     overwrites the entry created from the first response.
package criticalch

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// ClientHints describes the client hints a particular client can send. The
// map keys are canonical HTTP header names (see [http.Header.CanonicalHeaderKey])
// and the values are the hint values that the proxy would attach when retrying.
// A key present in the map means the client is capable of sending that hint.
type ClientHints map[string]string

// NewClientHints builds a ClientHints set from hint/value pairs. Hint names
// are canonicalized so callers may use either "Sec-CH-UA" or "sec-ch-ua".
func NewClientHints(pairs map[string]string) ClientHints {
	hints := make(ClientHints, len(pairs))
	for name, value := range pairs {
		hints[http.CanonicalHeaderKey(name)] = value
	}
	return hints
}

// CanSend reports whether the client is capable of sending the hint.
func (h ClientHints) CanSend(name string) bool {
	_, ok := h[http.CanonicalHeaderKey(name)]
	return ok
}

// Value returns the value the proxy should attach for the hint and whether
// the client can send it.
func (h ClientHints) Value(name string) (string, bool) {
	value, ok := h[http.CanonicalHeaderKey(name)]
	return value, ok
}

// Decision is the result of evaluating a response's Critical-CH header
// against the request and the client's capabilities.
type Decision struct {
	// Retry is true when the proxy should reissue the request once, with the
	// hints in Hints attached.
	Retry bool
	// Hints maps each hint that must be attached to the retry to its value.
	Hints map[string]string
	// Uncacheable is true when the response depends on a Critical-CH hint
	// that is not covered by Vary and therefore must not enter a shared cache.
	Uncacheable bool
}

// parseFieldList parses a comma-separated HTTP field list (Accept-CH,
// Critical-CH or Vary). Parameters after ';' are dropped, names are
// canonicalized, blanks are ignored and the first occurrence of each name
// wins so the returned order is stable and de-duplicated.
func parseFieldList(values []string) []string {
	seen := make(map[string]bool)
	var fields []string
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if semi := strings.IndexByte(item, ';'); semi >= 0 {
				item = item[:semi]
			}
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			name := http.CanonicalHeaderKey(item)
			if seen[name] {
				continue
			}
			seen[name] = true
			fields = append(fields, name)
		}
	}
	return fields
}

func headerList(h http.Header, name string) []string {
	if h == nil {
		return nil
	}
	return parseFieldList(h.Values(name))
}

// EffectiveCriticalHints returns the Critical-CH fields that are also
// advertised in Accept-CH. A Critical-CH field without a matching Accept-CH
// field is ignored per the client hints specification.
func EffectiveCriticalHints(resp *http.Response) []string {
	return EffectiveCriticalHintsHeader(headerOf(resp))
}

// EffectiveCriticalHintsHeader is the header-only form of
// EffectiveCriticalHints, convenient for tests and for proxies that have not
// yet assembled an [http.Response].
func EffectiveCriticalHintsHeader(h http.Header) []string {
	accepted := make(map[string]bool)
	for _, hint := range headerList(h, "Accept-CH") {
		accepted[hint] = true
	}
	var effective []string
	for _, hint := range headerList(h, "Critical-CH") {
		if accepted[hint] {
			effective = append(effective, hint)
		}
	}
	return effective
}

// VarySet returns the canonical request-header names listed in Vary. A
// wildcard Vary ("*") is reported as the single element "*".
func VarySet(resp *http.Response) []string {
	return VarySetHeader(headerOf(resp))
}

// VarySetHeader is the header-only form of VarySet.
func VarySetHeader(h http.Header) []string {
	fields := headerList(h, "Vary")
	for _, field := range fields {
		if field == "*" {
			return []string{"*"}
		}
	}
	return fields
}

func headerOf(resp *http.Response) http.Header {
	if resp == nil {
		return nil
	}
	return resp.Header
}

// Evaluate applies the Critical-CH rules to a response.
//
// Critical-CH fields that are not present in Accept-CH are ignored entirely:
// they neither trigger a retry nor make the response uncacheable. A retry is
// requested only for effective hints that the original request did not carry
// and that the client can send, and only when the navigation has not already
// consumed its single retry. The response is marked uncacheable when an
// effective Critical-CH hint is absent from Vary.
func Evaluate(req *http.Request, resp *http.Response, hints ClientHints) Decision {
	return EvaluateHeader(req, headerOf(resp), hints)
}

// EvaluateHeader is the header-only form of Evaluate.
func EvaluateHeader(req *http.Request, h http.Header, hints ClientHints) Decision {
	var decision Decision

	vary := make(map[string]bool)
	for _, field := range VarySetHeader(h) {
		vary[field] = true
	}

	retryAllowed := req != nil && !AlreadyRetried(req.Context())
	decision.Hints = make(map[string]string)

	for _, hint := range EffectiveCriticalHintsHeader(h) {
		if !vary[hint] {
			decision.Uncacheable = true
		}
		if !retryAllowed {
			continue
		}
		if requestCarries(req, hint) {
			continue
		}
		value, ok := hints.Value(hint)
		if !ok {
			continue
		}
		decision.Retry = true
		decision.Hints[hint] = value
	}

	return decision
}

func requestCarries(req *http.Request, name string) bool {
	if req == nil || req.Header == nil {
		return false
	}
	_, present := req.Header[http.CanonicalHeaderKey(name)]
	return present
}

// retryMarkerKey is the context key carrying the per-navigation retry state.
type retryMarkerKey struct{}

// WithRetryMarker returns a context marked as having already performed the
// single Critical-CH retry for a navigation.
func WithRetryMarker(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, retryMarkerKey{}, true)
}

// AlreadyRetried reports whether the context carries the retry marker.
func AlreadyRetried(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	retry, _ := ctx.Value(retryMarkerKey{}).(bool)
	return retry
}

// BuildRetryRequest constructs the single retry request for a navigation. The
// returned request carries the marker that prevents any further Critical-CH
// retry and has every hint from decision attached. The caller must use the
// returned request rather than reusing the original.
func BuildRetryRequest(req *http.Request, decision Decision) *http.Request {
	retry := req.Clone(WithRetryMarker(req.Context()))
	if retry.Header == nil {
		retry.Header = make(http.Header)
	}
	for hint, value := range decision.Hints {
		retry.Header.Set(hint, value)
	}
	return retry
}

// VaryKey builds the cache-key fragment contributed by the request headers
// listed in the response's Vary. Every field is tagged to distinguish a
// header the request does not carry ("-") from a present header ("+"), and
// values are length-prefixed so distinct inputs can never collide. A
// wildcard Vary produces a fragment that incorporates every request header,
// matching its "no shared caching without normalization" semantics.
func VaryKey(req *http.Request, resp *http.Response) string {
	return VaryKeyHeader(req, headerOf(resp))
}

// VaryKeyHeader is the header-only form of VaryKey.
func VaryKeyHeader(req *http.Request, h http.Header) string {
	names := VarySetHeader(h)

	var builder strings.Builder
	if len(names) == 1 && names[0] == "*" {
		names = make([]string, 0, len(req.Header))
		for name := range req.Header {
			names = append(names, name)
		}
	}
	for _, name := range names {
		builder.WriteString(strconv.Itoa(len(name)))
		builder.WriteByte(':')
		builder.WriteString(name)
		values, present := req.Header[http.CanonicalHeaderKey(name)]
		if present {
			builder.WriteString("+")
		} else {
			builder.WriteString("-")
		}
		joined := strings.Join(values, ",")
		builder.WriteString(strconv.Itoa(len(joined)))
		builder.WriteByte(':')
		builder.WriteString(joined)
		builder.WriteByte(';')
	}
	return builder.String()
}

// CacheKey combines a caller-owned base key (typically method and URL) with
// the Vary fragment for the request and response.
func CacheKey(base string, req *http.Request, resp *http.Response) string {
	return base + "|" + VaryKey(req, resp)
}

// SharedCacheable reports whether the response may be stored in a shared
// cache. It is false when the response depends on an effective Critical-CH
// hint that Vary does not list, because other clients could otherwise receive
// a response chosen using a hint value they never sent.
func SharedCacheable(resp *http.Response) bool {
	return SharedCacheableHeader(headerOf(resp))
}

// SharedCacheableHeader is the header-only form of SharedCacheable.
func SharedCacheableHeader(h http.Header) bool {
	vary := make(map[string]bool)
	for _, field := range VarySetHeader(h) {
		vary[field] = true
	}
	for _, hint := range EffectiveCriticalHintsHeader(h) {
		if !vary[hint] {
			return false
		}
	}
	return true
}

// Errors returned by Cache.
var (
	// ErrNotCacheable is returned when a response is forbidden from the
	// shared cache by the Critical-CH/Vary rules.
	ErrNotCacheable = errors.New("criticalch: response is not shared-cacheable")
	// ErrEntryExists is returned from a retry store when an entry for the key
	// already exists, so the retry response cannot overwrite it.
	ErrEntryExists = errors.New("criticalch: cache entry already exists")
)

// CachedResponse is the stored snapshot of a cacheable response. The body is
// not captured: callers persist and replay bodies themselves.
type CachedResponse struct {
	StatusCode int
	Header     http.Header
}

// Cache is a minimal shared cache keyed by the strings produced with CacheKey
// or VaryKey. It enforces the two cache safety rules: responses that fail
// SharedCacheable are rejected, and responses produced by a retry never
// replace an existing entry.
type Cache struct {
	entries map[string]*CachedResponse
}

// NewCache returns an empty shared cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[string]*CachedResponse)}
}

// Get returns the cached response for key and whether an entry exists.
func (c *Cache) Get(key string) (*CachedResponse, bool) {
	entry, ok := c.entries[key]
	return entry, ok
}

// Store caches resp under key. When retried is false (the first response of a
// navigation) an existing entry is replaced. When retried is true the store
// is refused with ErrEntryExists if the key is already present, so the retry
// response can never overwrite the first response's entry. Responses that
// fail SharedCacheable are refused with ErrNotCacheable.
func (c *Cache) Store(key string, resp *http.Response, retried bool) error {
	if !SharedCacheable(resp) {
		return ErrNotCacheable
	}
	if retried {
		if _, exists := c.entries[key]; exists {
			return ErrEntryExists
		}
	}
	c.entries[key] = snapshot(resp)
	return nil
}

// Len reports the number of stored entries.
func (c *Cache) Len() int {
	return len(c.entries)
}

func snapshot(resp *http.Response) *CachedResponse {
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &CachedResponse{
		StatusCode: status,
		Header:     resp.Header.Clone(),
	}
}

// RoundTrip performs the Critical-CH retry loop for a round-tripper. The
// first response is evaluated; when a retry is warranted the request is
// rebuilt once and reissued. Any further Critical-CH demand on the retried
// response is honored only through the cache rules and never triggers another
// retry.
func RoundTrip(transport http.RoundTripper, req *http.Request, hints ClientHints) (*http.Response, error) {
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	decision := Evaluate(req, resp, hints)
	if !decision.Retry {
		return resp, nil
	}

	resp.Body.Close()
	retryReq := BuildRetryRequest(req, decision)
	return transport.RoundTrip(retryReq)
}
