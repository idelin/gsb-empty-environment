package netsec

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// CacheControl is the parsed representation of an HTTP Cache-Control
// header field (RFC 9111 section 5.2). Token directives that do not carry
// a value (e.g. "no-store", "public") are recorded with the value true.
type CacheControl struct {
	NoStore         bool
	NoCache         bool
	Private         bool
	Public          bool
	MustRevalidate  bool
	ProxyRevalidate bool
	MaxAge          int
	HasMaxAge       bool
	SMaxAge         int
	HasSMaxAge      bool
	Immutable       bool
	NoTransform     bool
}

// ParseCacheControl parses every Cache-Control header line in h. Directives
// are case-insensitive; unknown directives are ignored. An explicitly empty
// value for a delta-seconds directive is treated as invalid rather than zero.
func ParseCacheControl(h http.Header) CacheControl {
	var cc CacheControl
	for _, line := range h.Values("Cache-Control") {
		for _, part := range strings.Split(line, ",") {
			token, value, hasValue := splitDirective(part)
			switch token {
			case "no-store":
				cc.NoStore = true
			case "no-cache":
				cc.NoCache = true
			case "private":
				cc.Private = true
			case "public":
				cc.Public = true
			case "must-revalidate":
				cc.MustRevalidate = true
			case "proxy-revalidate":
				cc.ProxyRevalidate = true
			case "immutable":
				cc.Immutable = true
			case "no-transform":
				cc.NoTransform = true
			case "max-age":
				if hasValue {
					if seconds, ok := parseDeltaSeconds(value); ok {
						cc.MaxAge = seconds
						cc.HasMaxAge = true
					}
				}
			case "s-maxage":
				if hasValue {
					if seconds, ok := parseDeltaSeconds(value); ok {
						cc.SMaxAge = seconds
						cc.HasSMaxAge = true
					}
				}
			}
		}
	}
	return cc
}

func splitDirective(part string) (token, value string, hasValue bool) {
	part = strings.TrimSpace(part)
	if part == "" {
		return "", "", false
	}
	if idx := strings.IndexByte(part, '='); idx >= 0 {
		token = strings.ToLower(strings.TrimSpace(part[:idx]))
		value = strings.TrimSpace(part[idx+1:])
		value = strings.Trim(value, `"`)
		return token, value, true
	}
	return strings.ToLower(part), "", false
}

func parseDeltaSeconds(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		return 0, false
	}
	return seconds, true
}

// ParseVary returns the canonicalized list of request header field names the
// response varies on. Field names are lower-cased, de-duplicated and sorted.
// A wildcard Vary ("*") is reported via wildcard; such responses cannot be
// served to any request other than the one that produced them, so the shared
// cache refuses to store them.
func ParseVary(h http.Header) (fields []string, wildcard bool) {
	seen := map[string]bool{}
	for _, line := range h.Values("Vary") {
		for _, name := range strings.Split(line, ",") {
			name = strings.TrimSpace(name)
			if name == "*" {
				return nil, true
			}
			if name == "" {
				continue
			}
			canonical := strings.ToLower(name)
			if !seen[canonical] {
				seen[canonical] = true
				fields = append(fields, canonical)
			}
		}
	}
	sort.Strings(fields)
	return fields, false
}
