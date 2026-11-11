package proxy

import (
	"strconv"
	"strings"
	"time"
)

// cacheDirectives is the parsed token/value map of a Cache-Control header.
// Valueless directives map to the empty string.
type cacheDirectives map[string]string

func parseCacheControl(header string) cacheDirectives {
	directives := cacheDirectives{}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if eq := strings.IndexByte(part, '='); eq >= 0 {
			key := strings.ToLower(strings.TrimSpace(part[:eq]))
			value := strings.TrimSpace(part[eq+1:])
			value = strings.Trim(value, `"`)
			directives[key] = value
		} else {
			directives[strings.ToLower(part)] = ""
		}
	}
	return directives
}

func (d cacheDirectives) has(token string) bool {
	_, ok := d[token]
	return ok
}

// seconds returns the directive value as a duration. It returns false when the
// token is absent or its value is not a non-negative integer.
func (d cacheDirectives) seconds(token string) (time.Duration, bool) {
	value, ok := d[token]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}
