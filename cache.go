package netsec

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxForbiddenTTL is the maximum time a 403 authorization result may be
	// reused.
	MaxForbiddenTTL = 5 * time.Second
	// MaxAllowedTTL is the maximum time a 200 authorization result may be
	// reused.
	MaxAllowedTTL = 60 * time.Second
	// DefaultCapacity is the default maximum number of cached variants.
	DefaultCapacity = 1024
)

// Option configures a Cache.
type Option func(*Cache)

// WithClock replaces the cache's clock. It is useful for deterministic expiry
// tests.
func WithClock(now func() time.Time) Option {
	return func(c *Cache) {
		if now != nil {
			c.now = now
		}
	}
}

// WithCapacity sets the maximum number of cached variants. Non-positive
// values leave the default unchanged.
func WithCapacity(maxEntries int) Option {
	return func(c *Cache) {
		if maxEntries > 0 {
			c.maxEntries = maxEntries
		}
	}
}

// WithMaxEntries is an alias for WithCapacity.
func WithMaxEntries(maxEntries int) Option {
	return WithCapacity(maxEntries)
}

// Result is a cached authorization response.
type Result struct {
	StatusCode int
	Header     http.Header
	CachedAt   time.Time
	ExpiresAt  time.Time
}

// Entry is an alias for Result.
type Entry = Result

// Expired reports whether the result is no longer valid at now. A result is
// expired at the exact instant its TTL ends.
func (r Result) Expired(now time.Time) bool {
	return !now.Before(r.ExpiresAt)
}

// Cache is a concurrency-safe, in-memory authorization result cache.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]cachedEntry
	byBase     map[string]map[string]struct{}
	maxEntries int
	now        func() time.Time
}

type cachedEntry struct {
	key       Key
	base      string
	status    int
	header    http.Header
	cachedAt  time.Time
	expiresAt time.Time
}

// New creates an empty authorization cache.
func New(options ...Option) *Cache {
	c := &Cache{
		entries:    make(map[string]cachedEntry),
		byBase:     make(map[string]map[string]struct{}),
		maxEntries: DefaultCapacity,
		now:        time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(c)
		}
	}
	return c
}

// NewCache is an alias for New.
func NewCache(options ...Option) *Cache {
	return New(options...)
}

// Get returns a cached authorization response matching req. It returns false
// for missing credentials, malformed paths, expired entries, and requests that
// do not match a stored Vary variant.
func (c *Cache) Get(req *http.Request) (Result, bool) {
	baseKey, err := KeyForRequest(req)
	if err != nil {
		return Result{}, false
	}
	base := baseKey.BaseKey().String()
	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	ids := c.byBase[base]
	for id := range ids {
		entry, ok := c.entries[id]
		if !ok {
			delete(ids, id)
			continue
		}
		if !now.Before(entry.expiresAt) {
			c.removeLocked(id, entry.base)
			continue
		}
		if !varyMatches(req, entry.key.Vary) {
			continue
		}
		return resultFrom(entry), true
	}
	if len(ids) == 0 {
		delete(c.byBase, base)
	}
	return Result{}, false
}

// Set stores an authorization response when policy permits it. It returns
// false for 401, 5xx, requests without credentials, responses that disallow
// caching, and responses without a usable freshness lifetime. A non-cacheable
// response invalidates existing variants for the same base key so an older
// allow decision cannot be reused.
func (c *Cache) Set(req *http.Request, statusCode int, header http.Header) bool {
	baseKey, err := KeyForRequest(req)
	if err != nil {
		return false
	}
	base := baseKey.BaseKey().String()

	ttl, cacheable := CacheTTL(statusCode, header)
	if !cacheable {
		c.invalidateBase(base)
		return false
	}

	key, err := BuildKey(req, header)
	if err != nil {
		c.invalidateBase(base)
		return false
	}

	now := c.now()
	entry := cachedEntry{
		key:       key,
		base:      base,
		status:    statusCode,
		header:    cloneHeader(header),
		cachedAt:  now,
		expiresAt: now.Add(ttl),
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeLocked(entry)
	return true
}

// Put is an alias for Set.
func (c *Cache) Put(req *http.Request, statusCode int, header http.Header) bool {
	return c.Set(req, statusCode, header)
}

// Store is an alias for Set.
func (c *Cache) Store(req *http.Request, statusCode int, header http.Header) bool {
	return c.Set(req, statusCode, header)
}

// Invalidate removes every Vary variant for req's base key.
func (c *Cache) Invalidate(req *http.Request) bool {
	key, err := KeyForRequest(req)
	if err != nil {
		return false
	}
	c.invalidateBase(key.BaseKey().String())
	return true
}

// Delete is an alias for Invalidate.
func (c *Cache) Delete(req *http.Request) bool {
	return c.Invalidate(req)
}

// Clear removes all entries.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cachedEntry)
	c.byBase = make(map[string]map[string]struct{})
}

// Len returns the number of live cached variants after removing expired
// entries.
func (c *Cache) Len() int {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			c.removeLocked(id, entry.base)
		}
	}
	return len(c.entries)
}

// CacheTTL returns the freshness lifetime for an authorization response and
// whether the response may be cached. A 403 defaults to five seconds; a 200
// requires a positive max-age. Both are capped at their respective maxima.
func CacheTTL(statusCode int, header http.Header) (time.Duration, bool) {
	if !cacheableStatus(statusCode) {
		return 0, false
	}

	policy := parseCacheControl(header)
	if policy.noStore || policy.noCache || policy.private || policy.invalidMaxAge {
		return 0, false
	}

	var ttl time.Duration
	switch statusCode {
	case http.StatusOK:
		if !policy.hasMaxAge || policy.maxAgeSeconds <= 0 {
			return 0, false
		}
		ttl = durationSeconds(policy.maxAgeSeconds)
		if ttl > MaxAllowedTTL {
			ttl = MaxAllowedTTL
		}
	case http.StatusForbidden:
		ttl = MaxForbiddenTTL
		if policy.hasMaxAge {
			if policy.maxAgeSeconds <= 0 {
				return 0, false
			}
			if maxAge := durationSeconds(policy.maxAgeSeconds); maxAge < ttl {
				ttl = maxAge
			}
		}
	default:
		return 0, false
	}

	age := durationSeconds(policy.ageSeconds)
	if age >= ttl {
		return 0, false
	}
	ttl -= age
	if ttl <= 0 {
		return 0, false
	}
	return ttl, true
}

// TTLFor is an alias for CacheTTL.
func TTLFor(statusCode int, header http.Header) (time.Duration, bool) {
	return CacheTTL(statusCode, header)
}

// CacheableStatus reports whether an authorization status is eligible for
// caching. Freshness directives can still make a 200 or 403 uncacheable.
func CacheableStatus(statusCode int) bool {
	return cacheableStatus(statusCode)
}

func cacheableStatus(statusCode int) bool {
	return statusCode == http.StatusOK || statusCode == http.StatusForbidden
}

func (c *Cache) storeLocked(entry cachedEntry) {
	id := entry.key.String()

	ids := c.byBase[entry.base]
	signature := varySignature(entry.key.Vary)
	for existingID := range ids {
		existing, ok := c.entries[existingID]
		if !ok {
			delete(ids, existingID)
			continue
		}
		if varySignature(existing.key.Vary) != signature {
			delete(c.entries, existingID)
			delete(ids, existingID)
		}
	}

	c.entries[id] = entry
	if ids == nil {
		ids = make(map[string]struct{})
		c.byBase[entry.base] = ids
	}
	ids[id] = struct{}{}
	c.evictLocked()
}

func (c *Cache) invalidateBase(base string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id := range c.byBase[base] {
		entry, ok := c.entries[id]
		if !ok {
			delete(c.byBase[base], id)
			continue
		}
		c.removeLocked(id, entry.base)
	}
	delete(c.byBase, base)
}

func (c *Cache) removeLocked(id, base string) {
	delete(c.entries, id)
	ids := c.byBase[base]
	delete(ids, id)
	if len(ids) == 0 {
		delete(c.byBase, base)
	}
}

func (c *Cache) evictLocked() {
	for len(c.entries) > c.maxEntries {
		now := c.now()
		removed := false
		for id, entry := range c.entries {
			if !now.Before(entry.expiresAt) {
				c.removeLocked(id, entry.base)
				removed = true
				break
			}
		}
		if removed {
			continue
		}
		for id, entry := range c.entries {
			c.removeLocked(id, entry.base)
			break
		}
	}
}

func resultFrom(entry cachedEntry) Result {
	return Result{
		StatusCode: entry.status,
		Header:     cloneHeader(entry.header),
		CachedAt:   entry.cachedAt,
		ExpiresAt:  entry.expiresAt,
	}
}

func cloneHeader(header http.Header) http.Header {
	if header == nil {
		return make(http.Header)
	}
	return header.Clone()
}

func varyMatches(req *http.Request, vary []VaryValue) bool {
	for _, expected := range vary {
		if requestHeaderValue(req, expected.Name) != expected.Value {
			return false
		}
	}
	return true
}

func varySignature(vary []VaryValue) string {
	names := make([]string, len(vary))
	for i, value := range vary {
		names[i] = value.Name
	}
	return strings.Join(names, "\x00")
}

type cacheControlPolicy struct {
	noStore       bool
	noCache       bool
	private       bool
	hasMaxAge     bool
	invalidMaxAge bool
	maxAgeSeconds int64
	ageSeconds    int64
}

const maxDurationSeconds = int64(1 << 31)

func parseCacheControl(header http.Header) cacheControlPolicy {
	var policy cacheControlPolicy

	for _, directive := range splitDirectives(header.Values("Cache-Control")) {
		name, value, hasValue := strings.Cut(directive, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.Trim(strings.TrimSpace(value), `"`)

		switch name {
		case "no-store":
			policy.noStore = true
		case "no-cache":
			policy.noCache = true
		case "private":
			policy.private = true
		case "max-age":
			if !hasValue {
				policy.invalidMaxAge = true
				continue
			}
			seconds, ok := parseDeltaSeconds(value)
			if !ok {
				policy.invalidMaxAge = true
				continue
			}
			if !policy.hasMaxAge || seconds < policy.maxAgeSeconds {
				policy.maxAgeSeconds = seconds
			}
			policy.hasMaxAge = true
		}
	}

	if seconds, ok := parseDeltaSeconds(header.Get("Age")); ok {
		policy.ageSeconds = seconds
	}
	return policy
}

func splitDirectives(lines []string) []string {
	var directives []string
	for _, line := range lines {
		start := 0
		quoted := false
		escaped := false
		for i := 0; i < len(line); i++ {
			switch line[i] {
			case '\\':
				if quoted {
					escaped = !escaped
				}
			case '"':
				if !escaped {
					quoted = !quoted
				}
				escaped = false
			case ',':
				if !quoted {
					directives = append(directives, line[start:i])
					start = i + 1
				}
				escaped = false
			default:
				escaped = false
			}
		}
		directives = append(directives, line[start:])
	}
	return directives
}

func parseDeltaSeconds(value string) (int64, bool) {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, false
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 {
		return 0, false
	}
	if seconds > maxDurationSeconds {
		seconds = maxDurationSeconds
	}
	return seconds, true
}

func durationSeconds(seconds int64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	if seconds > maxDurationSeconds {
		seconds = maxDurationSeconds
	}
	return time.Duration(seconds) * time.Second
}
