package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
)

var (
	// ErrEncodedSlash reports a path containing a percent-encoded slash at any
	// decoding layer. Such paths are rejected instead of normalized.
	ErrEncodedSlash = errors.New("authcache: percent-encoded slash in path")
	// ErrInvalidPath reports a malformed percent escape in a path.
	ErrInvalidPath = errors.New("authcache: invalid path")
	// ErrInvalidQuery reports a malformed percent escape in a query.
	ErrInvalidQuery = errors.New("authcache: invalid query")
	// ErrNoCredentials reports a request with neither Authorization nor Cookie.
	ErrNoCredentials = errors.New("authcache: request has no authorization or cookie")
	// ErrVaryStar reports a Vary header containing "*".
	ErrVaryStar = errors.New("authcache: Vary contains *")
	// ErrInvalidVary reports an invalid field name in a Vary header.
	ErrInvalidVary = errors.New("authcache: invalid Vary field name")
)

// VaryValue is one request-header value selected by the authorization
// service's Vary response header.
type VaryValue struct {
	Name  string
	Value string
}

// Key binds every request property that can affect an authorization result.
// AuthorizationSHA256 and CookieSHA256 are hashes of the corresponding request
// header values; credentials themselves are never retained in a Key.
type Key struct {
	Scheme string
	Method string
	Host   string
	Path   string
	Query  string

	AuthorizationSHA256 [sha256.Size]byte
	CookieSHA256        [sha256.Size]byte

	// Vary is sorted by Name and contains the request values for the fields
	// named by the authorization response's Vary header.
	Vary []VaryValue
}

// String returns an unambiguous SHA-256 digest of the complete key. The digest
// includes the authorization and cookie digests as well as the Vary values.
func (k Key) String() string {
	h := sha256.New()
	writeKeyField(h, "authcache-key-v1")
	writeKeyField(h, k.Scheme)
	writeKeyField(h, k.Method)
	writeKeyField(h, k.Host)
	writeKeyField(h, k.Path)
	writeKeyField(h, k.Query)
	writeKeyField(h, string(k.AuthorizationSHA256[:]))
	writeKeyField(h, string(k.CookieSHA256[:]))
	for _, value := range k.Vary {
		writeKeyField(h, value.Name)
		writeKeyField(h, value.Value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BaseKey returns a key without Vary values. It is primarily useful for
// inspecting which part of a key is independent of the authorization response.
func (k Key) BaseKey() Key {
	k.Vary = nil
	return k
}

// BuildKey constructs the cache key for req using the Vary header returned by
// the authorization service. It fails closed when credentials are absent, the
// path or query is malformed, an encoded slash is present, or Vary is "*".
func BuildKey(req *http.Request, vary http.Header) (Key, error) {
	key, err := baseKey(req)
	if err != nil {
		return Key{}, err
	}

	names, err := parseVary(vary)
	if err != nil {
		return Key{}, err
	}
	if len(names) == 0 {
		return key, nil
	}

	key.Vary = make([]VaryValue, 0, len(names))
	for _, name := range names {
		key.Vary = append(key.Vary, VaryValue{
			Name:  name,
			Value: requestHeaderValue(req, name),
		})
	}
	return key, nil
}

// KeyForRequest constructs a base key without response Vary values.
func KeyForRequest(req *http.Request) (Key, error) {
	return BuildKey(req, nil)
}

// NormalizeMethod uppercases a method and defaults an empty method to GET.
func NormalizeMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet
	}
	return method
}

// NormalizePath decodes percent escapes, rejects %2f at every decoding layer,
// and removes "." and ".." segments. The result is rooted and cleaned.
func NormalizePath(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}

	decoded := raw
	// A path can be encoded more than once before it reaches an upstream
	// application. Keep decoding while it shrinks so double-encoded dots and
	// slashes cannot evade the checks below. The loop is bounded by the input
	// length because every successful pass consumes at least one "%xx".
	for passes := 0; passes <= len(raw); passes++ {
		if containsEncodedSlash(decoded) {
			return "", ErrEncodedSlash
		}
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
		}
		if next == decoded {
			break
		}
		decoded = next
	}

	// Defend against a decoding loop that somehow did not converge.
	if containsEncodedSlash(decoded) {
		return "", ErrEncodedSlash
	}

	if !strings.HasPrefix(decoded, "/") {
		decoded = "/" + decoded
	}
	cleaned := path.Clean(decoded)
	if cleaned == "." || cleaned == "" {
		return "/", nil
	}
	return cleaned, nil
}

// NormalizeQuery percent-decodes and re-encodes each query component. It
// preserves component order and repeated parameters, which avoids merging
// requests that an application may interpret differently.
func NormalizeQuery(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	components := strings.Split(raw, "&")
	for i, component := range components {
		name, value, hasValue := strings.Cut(component, "=")

		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidQuery, err)
		}
		normalized := url.QueryEscape(decodedName)
		if hasValue {
			decodedValue, err := url.QueryUnescape(value)
			if err != nil {
				return "", fmt.Errorf("%w: %v", ErrInvalidQuery, err)
			}
			normalized += "=" + url.QueryEscape(decodedValue)
		}
		components[i] = normalized
	}
	return strings.Join(components, "&"), nil
}

func baseKey(req *http.Request) (Key, error) {
	if req == nil || req.URL == nil {
		return Key{}, ErrInvalidPath
	}

	normalizedPath, err := NormalizePath(req.URL.EscapedPath())
	if err != nil {
		return Key{}, err
	}
	normalizedQuery, err := NormalizeQuery(req.URL.RawQuery)
	if err != nil {
		return Key{}, err
	}

	authorization := req.Header.Values("Authorization")
	cookies := req.Header.Values("Cookie")
	if !hasCredential(authorization) && !hasCredential(cookies) {
		return Key{}, ErrNoCredentials
	}

	return Key{
		Scheme: stringScheme(req),
		Method: NormalizeMethod(req.Method),
		Host:   normalizeHost(req),
		Path:   normalizedPath,
		Query:  normalizedQuery,
		AuthorizationSHA256: sha256.Sum256(
			[]byte(strings.Join(authorization, "\x00")),
		),
		CookieSHA256: sha256.Sum256(
			[]byte(strings.Join(cookies, "\x00")),
		),
	}, nil
}

func normalizeHost(req *http.Request) string {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	return strings.ToLower(strings.TrimSpace(host))
}

func stringScheme(req *http.Request) string {
	if req.URL.Scheme != "" {
		return strings.ToLower(req.URL.Scheme)
	}
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

func hasCredential(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func containsEncodedSlash(value string) bool {
	return strings.Contains(strings.ToLower(value), "%2f")
}

func parseVary(header http.Header) ([]string, error) {
	if len(header) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{})
	for _, line := range header.Values("Vary") {
		for _, field := range strings.Split(line, ",") {
			field = strings.ToLower(strings.TrimSpace(field))
			if field == "" {
				continue
			}
			if field == "*" {
				return nil, ErrVaryStar
			}
			if !validHeaderFieldName(field) {
				return nil, fmt.Errorf("%w: %q", ErrInvalidVary, field)
			}
			seen[field] = struct{}{}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func requestHeaderValue(req *http.Request, name string) string {
	if name == "host" {
		return normalizeHost(req)
	}

	values := req.Header.Values(http.CanonicalHeaderKey(name))
	for i, value := range values {
		values[i] = strings.TrimSpace(value)
	}
	return strings.Join(values, "\x00")
}

func validHeaderFieldName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(c)) {
			return false
		}
	}
	return true
}

func writeKeyField(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}
