package netsec

import (
	"net/url"
	"strings"
)

var staticExts = []string{
	".css", ".js", ".mjs", ".map",
	".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
	".woff", ".woff2", ".ttf", ".eot",
	".txt", ".xml", ".json", ".pdf",
}

// NormalizePath returns the canonical form of a request target used both as
// the cache key and for suffix classification: percent-decoded, with the
// query string and per-segment semicolon parameters removed.
func NormalizePath(target string) string {
	path := target
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	if strings.IndexByte(path, ';') >= 0 {
		segs := strings.Split(path, "/")
		for i, seg := range segs {
			if j := strings.IndexByte(seg, ';'); j >= 0 {
				segs[i] = seg[:j]
			}
		}
		path = strings.Join(segs, "/")
	}
	return path
}

// IsStaticPath reports whether path carries a static-resource suffix. The
// caller is expected to pass an already-normalized path.
func IsStaticPath(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range staticExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// CacheKey is the key under which a response for target is stored.
func CacheKey(target string) string {
	return NormalizePath(target)
}

// ShouldCache reports whether the cache layer treats target as a static
// resource. Disguised suffixes produced by percent-encoded dots or
// semicolon parameters are classified as static, because classification
// runs on the normalized path.
func ShouldCache(target string) bool {
	return IsStaticPath(NormalizePath(target))
}

// Cache is a minimal shared response cache keyed by CacheKey.
type Cache struct {
	entries map[string][]byte
}

func NewCache() *Cache {
	return &Cache{entries: make(map[string][]byte)}
}

// Serve returns the response body for target. On a cache miss it invokes
// backend with the normalized path and stores the response when ShouldCache
// approves it. The second return value reports a cache hit.
func (c *Cache) Serve(target string, backend func(path string) []byte) ([]byte, bool) {
	key := CacheKey(target)
	if body, ok := c.entries[key]; ok {
		return body, true
	}
	body := backend(NormalizePath(target))
	if ShouldCache(target) {
		c.entries[key] = body
	}
	return body, false
}
