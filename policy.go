package netsec

import (
	"net/http"
	"strconv"
	"strings"
)

const absentHeaderValue = "\x00absent"

// Policy controls both cache-key construction and which forwarded headers may
// reach the origin.
type Policy struct {
	// SchemeHeader is the forwarding header carrying the original request
	// scheme. Empty defaults to HeaderXForwardedProto.
	SchemeHeader string

	// OriginUsesSchemeHeader says the origin varies its response on
	// SchemeHeader. When true, the header is included in the cache key and is
	// allowed through to the origin. When false, the header is stripped unless
	// an origin response explicitly lists it in Vary.
	OriginUsesSchemeHeader bool

	// KeyedHeaders contains additional request headers that are part of the
	// cache key. Forwarding headers in this list are also forwarded to the
	// origin. Headers named by Vary are added to this set automatically.
	KeyedHeaders []string
}

// Config is an alias for Policy.
type Config = Policy

// Decision is the complete result of applying a Policy to a request.
type Decision struct {
	// Key is the cache key including all configured and Vary-selected headers.
	Key string

	// KeyedHeaders is the canonical, sorted list of headers that contribute to
	// Key.
	KeyedHeaders []string

	// ForwardedHeaders lists forwarding headers that will be sent to the origin.
	ForwardedHeaders []string

	// StrippedHeaders lists forwarding headers that are present on the request
	// but are not part of the key and therefore must not be forwarded.
	StrippedHeaders []string

	// VaryHeaders is the parsed Vary list supplied by the caller.
	VaryHeaders []string

	// CanStore is false when Vary contains "*".
	CanStore bool
}

// Cacheable reports whether the response may be stored in a shared cache.
func (d Decision) Cacheable() bool {
	return d.CanStore
}

// DefaultPolicy is the secure default: X-Forwarded-Proto is used by the origin,
// is part of the cache key, and is forwarded to the origin. All other
// forwarding headers are stripped unless explicitly keyed.
func DefaultPolicy() Policy {
	return Policy{
		SchemeHeader:           HeaderXForwardedProto,
		OriginUsesSchemeHeader: true,
	}
}

// DefaultConfig is an alias for DefaultPolicy.
func DefaultConfig() Config {
	return DefaultPolicy()
}

// NewPolicy constructs a Policy. An empty schemeHeader selects
// HeaderXForwardedProto. Additional keyed headers are matched case-insensitively.
func NewPolicy(schemeHeader string, originUsesSchemeHeader bool, keyed ...string) Policy {
	return Policy{
		SchemeHeader:           schemeHeader,
		OriginUsesSchemeHeader: originUsesSchemeHeader,
		KeyedHeaders:           keyed,
	}.normalized()
}

// NewConfig is an alias for NewPolicy.
func NewConfig(schemeHeader string, originUsesSchemeHeader bool, keyed ...string) Config {
	return NewPolicy(schemeHeader, originUsesSchemeHeader, keyed...)
}

// Scheme returns the effective request scheme. URL scheme and TLS state take
// precedence; an explicitly supplied forwarding header is consulted only when
// the request itself does not carry a scheme. Header names are case-insensitive.
func Scheme(r *http.Request, schemeHeader ...string) string {
	if r == nil {
		return ""
	}
	if r.URL != nil && r.URL.Scheme != "" {
		return strings.ToLower(r.URL.Scheme)
	}
	if r.TLS != nil {
		return "https"
	}
	if len(schemeHeader) > 0 && schemeHeader[0] != "" {
		values := headerValues(r.Header, schemeHeader[0])
		if len(values) > 0 {
			return normalizeSchemeValue(values[0])
		}
	}
	return "http"
}

// KeyedHeaderNames returns the canonical, sorted header names that contribute
// to the key for the supplied Vary values.
func (p Policy) KeyedHeaderNames(vary []string) []string {
	p = p.normalized()
	return sortedNames(p.keyedSet(ParseVary(vary...)))
}

// Key builds a deterministic cache key for r. The key always contains method,
// effective scheme, host, path, and query, plus every configured keyed header
// and every header named by vary.
//
// A response with Vary: "*" must not be stored. Key still returns a
// deterministic value, but callers must check Decision.CanStore or use
// VaryAllowsStorage before sharing it.
func (p Policy) Key(r *http.Request, vary []string) string {
	p = p.normalized()
	key, _, _ := p.resolve(r, vary)
	return key
}

// CacheKey is a variadic convenience wrapper around Policy.Key.
func (p Policy) CacheKey(r *http.Request, vary ...string) string {
	return p.Key(r, vary)
}

// ForwardHeaders returns a copy of the request headers suitable for the origin.
// Hop-by-hop headers and forwarding headers not present in the key are removed.
func (p Policy) ForwardHeaders(r *http.Request, vary []string) http.Header {
	if r == nil {
		return make(http.Header)
	}
	p = p.normalized()
	return filterForwardingHeaders(r.Header, p.keyedSet(ParseVary(vary...)))
}

// PrepareRequest returns a shallow clone of r with sanitized headers. The
// original request is not modified.
func (p Policy) PrepareRequest(r *http.Request, vary []string) *http.Request {
	if r == nil {
		return nil
	}
	clone := r.Clone(r.Context())
	clone.Header = p.ForwardHeaders(r, vary)
	return clone
}

// Sanitize removes headers from r in place and returns r.
func (p Policy) Sanitize(r *http.Request, vary []string) *http.Request {
	if r == nil {
		return nil
	}
	r.Header = p.ForwardHeaders(r, vary)
	return r
}

// Decide returns the cache key and the forwarding decision for r in one pass.
func (p Policy) Decide(r *http.Request, vary []string) Decision {
	p = p.normalized()
	parsedVary := ParseVary(vary...)
	keyedSet := p.keyedSet(parsedVary)
	key, keyedNames, canStore := p.resolveParsed(r, parsedVary, keyedSet)

	var forwarded, stripped []string
	if r != nil {
		for name := range r.Header {
			name = canonicalHeaderName(name)
			if name == "" || !IsForwardingHeader(name) {
				continue
			}
			if _, ok := keyedSet[name]; ok {
				forwarded = append(forwarded, name)
			} else {
				stripped = append(stripped, name)
			}
		}
	}

	return Decision{
		Key:              key,
		KeyedHeaders:     keyedNames,
		ForwardedHeaders: uniqueSorted(forwarded),
		StrippedHeaders:  uniqueSorted(stripped),
		VaryHeaders:      parsedVary,
		CanStore:         canStore,
	}
}

// CacheKey builds a key using the default policy.
func CacheKey(r *http.Request, vary ...string) string {
	return DefaultPolicy().Key(r, vary)
}

// Key is an alias for CacheKey.
func Key(r *http.Request, vary ...string) string {
	return CacheKey(r, vary...)
}

// BuildCacheKey is an alias for CacheKey.
func BuildCacheKey(r *http.Request, vary ...string) string {
	return CacheKey(r, vary...)
}

// ComputeCacheKey is an alias for CacheKey.
func ComputeCacheKey(r *http.Request, vary ...string) string {
	return CacheKey(r, vary...)
}

// CacheKeyWithVary builds a key from an explicit Vary slice.
func CacheKeyWithVary(r *http.Request, vary []string) string {
	return CacheKey(r, vary...)
}

// CacheKeyWithPolicy builds a key using p.
func CacheKeyWithPolicy(p Policy, r *http.Request, vary ...string) string {
	return p.Key(r, vary)
}

// BuildCacheKeyWithPolicy is an alias for CacheKeyWithPolicy.
func BuildCacheKeyWithPolicy(p Policy, r *http.Request, vary ...string) string {
	return CacheKeyWithPolicy(p, r, vary...)
}

// ForwardHeaders filters a request's headers using the default policy plus the
// supplied keyed header names. Keyed forwarding headers are forwarded; all
// other forwarding headers are removed. Use a Policy with an explicit
// KeyedHeaders list when the default scheme-header policy is not wanted.
func ForwardHeaders(r *http.Request, keyed ...string) http.Header {
	if r == nil {
		return make(http.Header)
	}
	p := DefaultPolicy()
	p.KeyedHeaders = append(p.KeyedHeaders, keyed...)
	return p.ForwardHeaders(r, nil)
}

// ForwardedHeaders is an alias for ForwardHeaders.
func ForwardedHeaders(r *http.Request, keyed ...string) http.Header {
	return ForwardHeaders(r, keyed...)
}

// ForwardHeadersForVary filters a request using the default policy and the
// supplied Vary values. Vary-listed forwarding headers are therefore forwarded.
func ForwardHeadersForVary(r *http.Request, vary []string) http.Header {
	return DefaultPolicy().ForwardHeaders(r, vary)
}

// FilterForwardHeaders filters h using the default policy plus keyed. The input
// header map is not modified.
func FilterForwardHeaders(h http.Header, keyed ...string) http.Header {
	p := DefaultPolicy()
	p.KeyedHeaders = append(p.KeyedHeaders, keyed...)
	return p.ForwardHeaders(&http.Request{Header: h}, nil)
}

// FilterForwardedHeaders is an alias for FilterForwardHeaders.
func FilterForwardedHeaders(h http.Header, keyed ...string) http.Header {
	return FilterForwardHeaders(h, keyed...)
}

// SanitizeForwardHeaders removes unkeyed forwarding headers from r in place.
func SanitizeForwardHeaders(r *http.Request, keyed ...string) *http.Request {
	if r == nil {
		return nil
	}
	p := DefaultPolicy()
	p.KeyedHeaders = append(p.KeyedHeaders, keyed...)
	return p.Sanitize(r, nil)
}

// SanitizeForwardedHeaders is an alias for SanitizeForwardHeaders.
func SanitizeForwardedHeaders(r *http.Request, keyed ...string) *http.Request {
	return SanitizeForwardHeaders(r, keyed...)
}

// ShouldForwardHeader reports whether name may be sent to the origin. A
// forwarding header must be in keyed to be forwarded; non-forwarding headers
// pass unless they are hop-by-hop.
func ShouldForwardHeader(name string, keyed []string) bool {
	name = canonicalHeaderName(name)
	if name == "" || IsHopByHopHeader(name) {
		return false
	}
	if !IsForwardingHeader(name) {
		return true
	}
	_, ok := canonicalNameSet(keyed)[name]
	return ok
}

// IsKeyedHeader reports whether name contributes to the key under policy p.
func IsKeyedHeader(p Policy, name string, vary ...string) bool {
	name = canonicalHeaderName(name)
	_, ok := p.normalized().keyedSet(ParseVary(vary...))[name]
	return ok
}

func (p Policy) normalized() Policy {
	p.SchemeHeader = canonicalHeaderName(p.SchemeHeader)
	if p.SchemeHeader == "" {
		p.SchemeHeader = HeaderXForwardedProto
	}
	keyed := canonicalNameSet(p.KeyedHeaders)
	p.KeyedHeaders = sortedNames(keyed)
	return p
}

func (p Policy) keyedSet(vary []string) map[string]struct{} {
	set := canonicalNameSet(p.KeyedHeaders)
	if p.OriginUsesSchemeHeader {
		set[canonicalHeaderName(p.SchemeHeader)] = struct{}{}
	}
	for _, name := range vary {
		name = canonicalHeaderName(name)
		if name == "" || name == "*" {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func (p Policy) resolve(r *http.Request, vary []string) (key string, keyed []string, canStore bool) {
	parsedVary := ParseVary(vary...)
	return p.resolveParsed(r, parsedVary, p.keyedSet(parsedVary))
}

func (p Policy) resolveParsed(r *http.Request, parsedVary []string, keyedSet map[string]struct{}) (string, []string, bool) {
	canStore := true
	for _, name := range parsedVary {
		if name == "*" {
			canStore = false
			break
		}
	}
	if !canStore {
		if r != nil {
			for _, name := range allHeaderNames(r.Header) {
				keyedSet[name] = struct{}{}
			}
		}
	}
	keyed := sortedNames(keyedSet)
	return buildKey(r, keyed, !canStore), keyed, canStore
}

func buildKey(r *http.Request, keyed []string, varyAll bool) string {
	var b strings.Builder
	b.WriteString("netsec-v1\n")
	if varyAll {
		appendKeyPart(&b, "vary", "*")
	}

	if r == nil {
		appendKeyPart(&b, "request", absentHeaderValue)
		return b.String()
	}

	appendKeyPart(&b, "method", r.Method)
	appendKeyPart(&b, "scheme", requestScheme(r))
	appendKeyPart(&b, "host", strings.ToLower(requestHost(r)))
	appendKeyPart(&b, "path", requestPath(r))
	appendKeyPart(&b, "query", requestQuery(r))

	for _, name := range keyed {
		values := headerValues(r.Header, name)
		if len(values) == 0 {
			appendKeyPart(&b, "header:"+strings.ToLower(name), absentHeaderValue)
			continue
		}
		for _, value := range values {
			appendKeyPart(&b, "header:"+strings.ToLower(name), keyHeaderValue(name, value))
		}
	}

	return b.String()
}

func requestScheme(r *http.Request) string {
	if r.URL != nil && r.URL.Scheme != "" {
		return strings.ToLower(r.URL.Scheme)
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func requestHost(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	if r.URL != nil {
		return r.URL.Host
	}
	return ""
}

func requestPath(r *http.Request) string {
	if r.URL == nil {
		return "/"
	}
	if path := r.URL.EscapedPath(); path != "" {
		return path
	}
	return "/"
}

func requestQuery(r *http.Request) string {
	if r.URL == nil {
		return ""
	}
	return r.URL.RawQuery
}

func keyHeaderValue(name, value string) string {
	if IsSchemeHeader(name) {
		return normalizeSchemeValue(value)
	}
	return strings.TrimSpace(value)
}

func normalizeSchemeValue(value string) string {
	parts := strings.Split(value, ",")
	for i, part := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(part))
	}
	return strings.Join(parts, ",")
}

func appendKeyPart(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteByte('=')
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteByte('\n')
}

func filterForwardingHeaders(src http.Header, keyed map[string]struct{}) http.Header {
	dst := make(http.Header, len(src))
	for name, values := range src {
		canonical := canonicalHeaderName(name)
		if canonical == "" || IsHopByHopHeader(canonical) {
			continue
		}
		if IsForwardingHeader(canonical) {
			if _, ok := keyed[canonical]; !ok {
				continue
			}
		}
		dst[canonical] = append(dst[canonical], values...)
	}
	for name := range connectionHeaders(src) {
		deleteHeader(dst, name)
	}
	return dst
}

func allHeaderNames(h http.Header) []string {
	set := make(map[string]struct{}, len(h))
	for name := range h {
		name = canonicalHeaderName(name)
		if name != "" {
			set[name] = struct{}{}
		}
	}
	return sortedNames(set)
}

func uniqueSorted(items []string) []string {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = canonicalHeaderName(item)
		if item != "" {
			set[item] = struct{}{}
		}
	}
	return sortedNames(set)
}
