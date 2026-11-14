package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Identity identifies the site and tenant whose namespace owns a cache entry.
// Both values are case-sensitive and are always part of a cache key.
type Identity struct {
	Site   string
	Tenant string
}

// DefaultNegotiationHeaders are used when KeyConfig.NegotiationHeaders is nil.
// Pass an explicitly empty slice to disable default content negotiation.
var DefaultNegotiationHeaders = []string{
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
}

// KeyConfig controls cache-key canonicalization.
type KeyConfig struct {
	// Identity applies to requests passed to KeyBuilder.Build.
	Identity Identity

	// NegotiationHeaders is the allow-list of request headers that participate
	// in content negotiation and therefore in the base cache key. Header names
	// are matched case-insensitively. A nil slice selects the defaults.
	NegotiationHeaders []string

	// NormalizationVersion identifies the canonicalization rules in use. It is
	// included in every key. An empty value selects "v1".
	NormalizationVersion string
}

// Key is an immutable, computed cache key. Digest is SHA-256 over a
// length-prefixed canonical representation of all key inputs.
type Key struct {
	Digest [32]byte

	// NormalizedTarget is the frozen external request path (without a query).
	NormalizedTarget string

	// NormalizedQuery contains every query name/value occurrence. Names are
	// sorted, while the values for each name retain their original order.
	NormalizedQuery string

	// Version is the normalization version used to compute Digest.
	Version string
}

// Hex returns the lower-case hexadecimal SHA-256 digest.
func (k Key) Hex() string {
	return hex.EncodeToString(k.Digest[:])
}

// String returns the key's stable digest identifier.
func (k Key) String() string {
	return k.Hex()
}

// Equal reports whether two keys have the same digest and normalization
// metadata.
func (k Key) Equal(other Key) bool {
	return k.Digest == other.Digest &&
		k.Version == other.Version &&
		k.NormalizedTarget == other.NormalizedTarget &&
		k.NormalizedQuery == other.NormalizedQuery
}

// IsZero reports whether k is the zero value.
func (k Key) IsZero() bool {
	return k == Key{}
}

// KeyBuilder computes shared-cache keys. It is safe for concurrent use.
type KeyBuilder struct {
	identity    Identity
	negotiation []string
	version     string
}

// NewKeyBuilder creates a key builder. It never mutates config or the slices
// referenced by it.
func NewKeyBuilder(config KeyConfig) *KeyBuilder {
	headers := config.NegotiationHeaders
	if headers == nil {
		headers = DefaultNegotiationHeaders
	}

	version := config.NormalizationVersion
	if version == "" {
		version = "v1"
	}

	return &KeyBuilder{
		identity:    config.Identity,
		negotiation: normalizeHeaderNames(headers),
		version:     version,
	}
}

// Identity returns the builder's default identity.
func (b *KeyBuilder) Identity() Identity {
	if b == nil {
		return Identity{}
	}
	return b.identity
}

// Version returns the normalization version included in keys.
func (b *KeyBuilder) Version() string {
	if b == nil {
		return ""
	}
	return b.version
}

// Build computes a key for req using the builder's default identity.
func (b *KeyBuilder) Build(req *http.Request) (Key, error) {
	if b == nil {
		return Key{}, errors.New("netsec: nil key builder")
	}
	return b.BuildFor(req, b.identity)
}

// BuildFor computes a key for req in the namespace identified by identity.
func (b *KeyBuilder) BuildFor(req *http.Request, identity Identity) (Key, error) {
	if b == nil {
		return Key{}, errors.New("netsec: nil key builder")
	}
	if req == nil {
		return Key{}, errors.New("netsec: nil request")
	}

	target, rawQuery, err := splitRequestTarget(requestTarget(req))
	if err != nil {
		return Key{}, err
	}
	query, err := CanonicalQuery(rawQuery)
	if err != nil {
		return Key{}, err
	}

	return b.build(identity, target, query, req.Header), nil
}

// BuildTarget computes a key directly from a frozen external request target and
// request headers. It is useful at the edge when an http.Request is not yet
// available.
func (b *KeyBuilder) BuildTarget(identity Identity, target string, header http.Header) (Key, error) {
	if b == nil {
		return Key{}, errors.New("netsec: nil key builder")
	}

	path, rawQuery, err := splitRequestTarget(target)
	if err != nil {
		return Key{}, err
	}
	query, err := CanonicalQuery(rawQuery)
	if err != nil {
		return Key{}, err
	}

	return b.build(identity, path, query, header), nil
}

func (b *KeyBuilder) build(identity Identity, target, query string, header http.Header) Key {
	preimage := make([]byte, 0, 256)
	preimage = append(preimage, "netsec-shared-cache-key\x00"...)
	preimage = appendString(preimage, b.version)
	preimage = appendString(preimage, identity.Site)
	preimage = appendString(preimage, identity.Tenant)
	preimage = appendString(preimage, target)
	preimage = appendString(preimage, query)

	preimage = appendString(preimage, strconv.Itoa(len(b.negotiation)))
	for _, name := range b.negotiation {
		preimage = appendHeader(preimage, name, header)
	}

	// Authorization is never shared, even when the origin neglected to send
	// Vary: Authorization. Keeping it in the base key isolates credentials from
	// both other users and anonymous requests.
	if _, present := headerValues(header, "Authorization"); present {
		preimage = appendHeader(preimage, "authorization", header)
	}

	digest := sha256.Sum256(preimage)
	return Key{
		Digest:           digest,
		NormalizedTarget: target,
		NormalizedQuery:  query,
		Version:          b.version,
	}
}

// CanonicalQuery normalizes a raw query while retaining every occurrence of
// every name. Parameter names are sorted for a stable key, but values belonging
// to the same name remain in their original order. Invalid escapes, semicolons,
// and empty parameter segments are rejected rather than collapsed.
func CanonicalQuery(rawQuery string) (string, error) {
	if rawQuery == "" {
		return "", nil
	}
	if strings.ContainsRune(rawQuery, ';') {
		return "", errors.New("netsec: invalid query: semicolon separators are ambiguous")
	}

	type queryValue struct {
		value     string
		hasEquals bool
	}
	values := make(map[string][]queryValue)
	for _, segment := range strings.Split(rawQuery, "&") {
		if segment == "" {
			return "", errors.New("netsec: invalid query: empty parameter segment")
		}

		rawName, rawValue, hasEquals := strings.Cut(segment, "=")
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			return "", fmt.Errorf("netsec: invalid query name: %w", err)
		}
		value := ""
		if hasEquals {
			value, err = url.QueryUnescape(rawValue)
			if err != nil {
				return "", fmt.Errorf("netsec: invalid query value: %w", err)
			}
		}
		values[name] = append(values[name], queryValue{value: value, hasEquals: hasEquals})
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonical strings.Builder
	first := true
	for _, name := range names {
		escapedName := url.QueryEscape(name)
		for _, value := range values[name] {
			if !first {
				canonical.WriteByte('&')
			}
			first = false
			canonical.WriteString(escapedName)
			if value.hasEquals {
				canonical.WriteByte('=')
				canonical.WriteString(url.QueryEscape(value.value))
			}
		}
	}
	return canonical.String(), nil
}

func requestTarget(req *http.Request) string {
	if req.RequestURI != "" {
		return req.RequestURI
	}
	if req.URL != nil {
		return req.URL.RequestURI()
	}
	return ""
}

func splitRequestTarget(target string) (string, string, error) {
	if target == "" {
		return "", "", errors.New("netsec: empty request target")
	}
	if strings.ContainsRune(target, '#') {
		return "", "", errors.New("netsec: request target must not contain a fragment")
	}

	// Absolute-form targets are legal in HTTP requests. The site identity, not
	// an attacker-controlled authority, remains the namespace boundary.
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		u, err := url.ParseRequestURI(target)
		if err != nil {
			return "", "", fmt.Errorf("netsec: invalid absolute request target: %w", err)
		}
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		return path, u.RawQuery, nil
	}

	path, rawQuery, found := strings.Cut(target, "?")
	if !found {
		rawQuery = ""
	}
	if path == "" {
		path = "/"
	}
	if target != "*" && !strings.HasPrefix(path, "/") {
		return "", "", fmt.Errorf("netsec: unsupported request target %q", target)
	}
	return path, rawQuery, nil
}

func normalizeHeaderNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || name == "authorization" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func appendString(dst []byte, value string) []byte {
	dst = strconv.AppendInt(dst, int64(len(value)), 10)
	dst = append(dst, ':')
	dst = append(dst, value...)
	return dst
}

func appendHeader(dst []byte, name string, header http.Header) []byte {
	dst = appendString(dst, name)
	values, present := headerValues(header, name)
	if present {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	dst = appendString(dst, strconv.Itoa(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

// headerValues performs a case-insensitive lookup even for http.Header maps
// that were populated directly with non-canonical keys. Values retain their
// order within each field occurrence.
func headerValues(header http.Header, name string) ([]string, bool) {
	if header == nil {
		return nil, false
	}

	keys := make([]string, 0, 1)
	for key := range header {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, false
	}
	sort.Strings(keys)

	var values []string
	for _, key := range keys {
		for _, value := range header[key] {
			values = append(values, strings.TrimSpace(value))
		}
	}
	return values, true
}

func firstHeaderValue(header http.Header, name string) string {
	values, _ := headerValues(header, name)
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstHeaderValueAny(header http.Header, names ...string) string {
	for _, name := range names {
		if value := firstHeaderValue(header, name); value != "" {
			return value
		}
	}
	return ""
}

func hasHeader(header http.Header, name string) bool {
	_, present := headerValues(header, name)
	return present
}
