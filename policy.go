package netsec

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

var hopByHopHeaders = []string{
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

func requestBypassesCache(req *http.Request) bool {
	if req == nil {
		return true
	}
	if req.Method != "" && req.Method != http.MethodGet {
		return true
	}
	if hasHeader(req.Header, "Range") {
		return true
	}
	for _, name := range []string{
		"If-Match",
		"If-Modified-Since",
		"If-None-Match",
		"If-Range",
		"If-Unmodified-Since",
	} {
		if hasHeader(req.Header, name) {
			return true
		}
	}

	cacheControl := parseCacheControl(headerValuesNoPresence(req.Header, "Cache-Control"))
	if _, ok := cacheControl["no-store"]; ok {
		return true
	}
	if _, ok := cacheControl["no-cache"]; ok {
		return true
	}
	if value, ok := cacheControl["max-age"]; ok {
		seconds, valid := parseDeltaSeconds(value)
		if !valid || seconds == 0 {
			return true
		}
	}
	if strings.EqualFold(firstHeaderValue(req.Header, "Pragma"), "no-cache") {
		return true
	}
	return false
}

func responseStatusCacheable(status int) bool {
	switch status {
	case http.StatusOK,
		http.StatusNonAuthoritativeInfo,
		http.StatusNoContent,
		http.StatusMultipleChoices,
		http.StatusMovedPermanently,
		http.StatusPermanentRedirect,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusGone,
		http.StatusRequestURITooLong,
		http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

func sharedResponsePolicy(header http.Header, now time.Time, defaultTTL time.Duration) (time.Duration, bool) {
	if hasHeader(header, "Set-Cookie") || hasHeader(header, "Set-Cookie2") {
		return 0, false
	}

	cacheControl := parseCacheControl(headerValuesNoPresence(header, "Cache-Control"))
	for _, directive := range []string{"private", "no-store", "no-cache"} {
		if _, ok := cacheControl[directive]; ok {
			return 0, false
		}
	}
	if strings.EqualFold(firstHeaderValue(header, "Pragma"), "no-cache") {
		return 0, false
	}

	ttl := defaultTTL
	if value, ok := cacheControl["s-maxage"]; ok {
		seconds, valid := parseDeltaSeconds(value)
		if !valid {
			return 0, false
		}
		ttl = seconds
	} else if value, ok := cacheControl["max-age"]; ok {
		seconds, valid := parseDeltaSeconds(value)
		if !valid {
			return 0, false
		}
		ttl = seconds
	} else if expiresValue := firstHeaderValue(header, "Expires"); expiresValue != "" {
		expires, err := http.ParseTime(expiresValue)
		if err != nil {
			return 0, false
		}
		ttl = expires.Sub(now)
	}

	if ageValue := firstHeaderValue(header, "Age"); ageValue != "" {
		ageSeconds, err := strconv.ParseInt(strings.TrimSpace(ageValue), 10, 64)
		if err != nil || ageSeconds < 0 || ageSeconds > math.MaxInt64/int64(time.Second) {
			return 0, false
		}
		ttl -= time.Duration(ageSeconds) * time.Second
	}

	if ttl <= 0 {
		return 0, false
	}
	return ttl, true
}

func parseDeltaSeconds(value string) (time.Duration, bool) {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds < 0 {
		return 0, false
	}
	if seconds > math.MaxInt64/int64(time.Second) {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func parseCacheControl(values []string) map[string]string {
	directives := make(map[string]string)
	for _, value := range values {
		for _, part := range splitHTTPList(value) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			name, parameter, found := strings.Cut(part, "=")
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if found {
				parameter = unquoteHTTP(strings.TrimSpace(parameter))
			}
			directives[name] = parameter
		}
	}
	return directives
}

func splitHTTPList(value string) []string {
	var parts []string
	start := 0
	quoted := false
	escaped := false
	for i := 0; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case quoted && value[i] == '\\':
			escaped = true
		case value[i] == '"':
			quoted = !quoted
		case value[i] == ',' && !quoted:
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	parts = append(parts, value[start:])
	return parts
}

func unquoteHTTP(value string) string {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}

	value = value[1 : len(value)-1]
	var builder strings.Builder
	builder.Grow(len(value))
	escaped := false
	for i := 0; i < len(value); i++ {
		if escaped {
			builder.WriteByte(value[i])
			escaped = false
			continue
		}
		if value[i] == '\\' {
			escaped = true
			continue
		}
		builder.WriteByte(value[i])
	}
	if escaped {
		builder.WriteByte('\\')
	}
	return builder.String()
}

func headerValuesNoPresence(header http.Header, name string) []string {
	values, _ := headerValues(header, name)
	return values
}

func canonicalHeaderClone(header http.Header) http.Header {
	cloned := make(http.Header, len(header))
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := header[name]
		canonicalName := http.CanonicalHeaderKey(name)
		if canonicalName == "" {
			canonicalName = name
		}
		cloned[canonicalName] = append(cloned[canonicalName], values...)
	}

	for _, name := range hopByHopHeaders {
		cloned.Del(name)
	}
	for _, name := range headerValuesNoPresence(header, "Connection") {
		for _, connectionHeader := range splitHTTPList(name) {
			connectionHeader = strings.TrimSpace(connectionHeader)
			if connectionHeader != "" {
				cloned.Del(connectionHeader)
			}
		}
	}
	return cloned
}
