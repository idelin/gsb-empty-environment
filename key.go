package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

var ErrVaryStar = errors.New("authcache: Vary * response is not cacheable")

type varyValue struct {
	name   string
	values []string
}

// BuildKey returns a cache key bound to the normalized request identity,
// request target, credentials, and values of headers named by Vary.
func BuildKey(r *http.Request, varyHeaders ...http.Header) (string, error) {
	names, err := parseVary(varyHeaders...)
	if err != nil {
		return "", err
	}
	return buildKey(r, names)
}

// CacheKey is an alias for BuildKey.
func CacheKey(r *http.Request, varyHeaders ...http.Header) (string, error) {
	return BuildKey(r, varyHeaders...)
}

func buildKey(r *http.Request, varyNames []string) (string, error) {
	if r == nil {
		return "", ErrNilRequest
	}
	if r.URL == nil {
		return "", ErrNilRequest
	}

	path, err := requestPath(r)
	if err != nil {
		return "", err
	}

	vary := varyValuesForRequest(r, varyNames)
	authHash := hashHeaderValues(requestHeaderValues(r, "Authorization"))
	cookieHash := hashHeaderValues(requestHeaderValues(r, "Cookie"))

	var material strings.Builder
	writeKeyField(&material, "auth-cache-key-v1")
	writeKeyField(&material, canonicalMethod(r.Method))
	writeKeyField(&material, canonicalScheme(r))
	writeKeyField(&material, canonicalHost(r))
	writeKeyField(&material, path)
	writeKeyField(&material, r.URL.RawQuery)
	writeKeyField(&material, strconv.FormatBool(r.URL.ForceQuery))
	writeKeyField(&material, authHash)
	writeKeyField(&material, cookieHash)
	writeKeyField(&material, strconv.Itoa(len(vary)))
	for _, item := range vary {
		writeKeyField(&material, item.name)
		writeKeyField(&material, strconv.Itoa(len(item.values)))
		for _, value := range item.values {
			writeKeyField(&material, value)
		}
	}

	sum := sha256.Sum256([]byte(material.String()))
	return "v1:" + hex.EncodeToString(sum[:]), nil
}

func canonicalMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet
	}
	return method
}

func canonicalHost(r *http.Request) string {
	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	return strings.ToLower(strings.TrimSpace(host))
}

func canonicalScheme(r *http.Request) string {
	if r.URL != nil && r.URL.Scheme != "" {
		return strings.ToLower(r.URL.Scheme)
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func writeKeyField(builder *strings.Builder, value string) {
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
}

func hashHeaderValues(values []string) string {
	var material strings.Builder
	writeKeyField(&material, strconv.Itoa(len(values)))
	for _, value := range values {
		writeKeyField(&material, value)
	}
	sum := sha256.Sum256([]byte(material.String()))
	return hex.EncodeToString(sum[:])
}

func requestHeaderValues(r *http.Request, name string) []string {
	if r == nil {
		return nil
	}
	if strings.EqualFold(name, "Host") {
		host := canonicalHost(r)
		if host == "" {
			return nil
		}
		return []string{host}
	}

	keys := make([]string, 0, len(r.Header))
	for key := range r.Header {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	var values []string
	for _, key := range keys {
		values = append(values, r.Header[key]...)
	}
	return values
}

func headerValues(header http.Header, name string) []string {
	keys := make([]string, 0, len(header))
	for key := range header {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	var values []string
	for _, key := range keys {
		values = append(values, header[key]...)
	}
	return values
}

func parseVary(headers ...http.Header) ([]string, error) {
	seen := make(map[string]string)
	for _, header := range headers {
		for _, line := range headerValues(header, "Vary") {
			for _, part := range strings.Split(line, ",") {
				name := strings.TrimSpace(part)
				if name == "" {
					continue
				}
				if name == "*" {
					return nil, ErrVaryStar
				}
				canonical := http.CanonicalHeaderKey(name)
				if canonical == "" {
					canonical = name
				}
				seen[strings.ToLower(canonical)] = canonical
			}
		}
	}

	names := make([]string, 0, len(seen))
	for _, name := range seen {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names, nil
}

func varyValuesForRequest(r *http.Request, names []string) []varyValue {
	values := make([]varyValue, 0, len(names))
	for _, name := range names {
		headerValues := requestHeaderValues(r, name)
		copied := append([]string(nil), headerValues...)
		values = append(values, varyValue{name: name, values: copied})
	}
	return values
}

func varyMatchesRequest(r *http.Request, expected []varyValue) bool {
	for _, item := range expected {
		actual := requestHeaderValues(r, item.name)
		if !equalStrings(actual, item.values) {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameVaryNames(left, right []varyValue) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !strings.EqualFold(left[i].name, right[i].name) {
			return false
		}
	}
	return true
}

func cloneVary(values []varyValue) []varyValue {
	copied := make([]varyValue, len(values))
	for i, item := range values {
		copied[i] = varyValue{name: item.name, values: append([]string(nil), item.values...)}
	}
	return copied
}
