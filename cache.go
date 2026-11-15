package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxTTL       = 60 * time.Second
	MaxDeniedTTL = 5 * time.Second

	defaultMaxEntries = 4096
)

var ErrNilCache = errors.New("authcache: nil cache")

// Result is the cacheable part of an authorization subrequest response.
type Result struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	ExpiresAt  time.Time
}

// Response is an alias for Result.
type Response = Result

// Clock supplies the current time for cache expiration.
type Clock func() time.Time

// NewResult constructs an authorization result.
func NewResult(statusCode int, header http.Header, body []byte) *Result {
	return &Result{
		StatusCode: statusCode,
		Header:     cloneHeader(header),
		Body:       append([]byte(nil), body...),
	}
}

// New returns an empty authorization cache. An optional clock can be supplied
// to make expiration behavior deterministic in tests.
func New(clock ...Clock) *Cache {
	cache := &Cache{maxEntries: defaultMaxEntries, now: time.Now}
	if len(clock) > 0 && clock[0] != nil {
		cache.now = clock[0]
	}
	return cache
}

// NewCache is an alias for New.
func NewCache(clock ...Clock) *Cache {
	return New(clock...)
}

// NewWithClock is an alias for New.
func NewWithClock(clock Clock) *Cache {
	return New(clock)
}

// NewWithCapacity returns a cache with the given maximum number of entries.
func NewWithCapacity(maxEntries int, clock ...Clock) *Cache {
	cache := New(clock...)
	if maxEntries > 0 {
		cache.maxEntries = maxEntries
	}
	return cache
}

// Cache stores authorization subrequest results with credential- and
// Vary-bound keys. Its zero value is ready for use.
type Cache struct {
	mu         sync.Mutex
	now        func() time.Time
	maxEntries int
	size       int
	sequence   uint64
	variants   map[string][]*entry
}

type entry struct {
	key       string
	result    Result
	vary      []varyValue
	expiresAt time.Time
	sequence  uint64
}

// Store caches a successful or denied authorization response. The response may
// be a Result, *Result, http.Response, or *http.Response. If varyHeaders is
// supplied, it overrides the Vary header in the response.
func (c *Cache) Store(r *http.Request, response any, varyHeaders ...http.Header) (bool, error) {
	if c == nil {
		return false, ErrNilCache
	}
	result, err := resultFromValue(response)
	if err != nil {
		return false, err
	}

	baseKey, err := buildKey(r, nil)
	if err != nil {
		return false, err
	}
	if !hasCredentials(r) {
		return false, nil
	}

	ttl, cacheable := CacheTTL(result.StatusCode, result.Header)
	if !cacheable {
		return false, nil
	}

	names, err := parseVary(varyHeaders...)
	if len(varyHeaders) == 0 {
		names, err = parseVary(result.Header)
	}
	if err != nil {
		if errors.Is(err, ErrVaryStar) {
			return false, nil
		}
		return false, err
	}

	now := c.currentTime()
	expiresAt := now.Add(ttl)
	if !expiresAt.After(now) {
		return false, nil
	}

	key, err := buildKey(r, names)
	if err != nil {
		return false, err
	}
	vary := varyValuesForRequest(r, names)
	result = cloneResult(result)
	result.ExpiresAt = expiresAt

	c.mu.Lock()
	defer c.mu.Unlock()

	c.ensureReadyLocked()
	c.purgeExpiredLocked(now)
	c.removeOtherVaryDeclarationsLocked(baseKey, vary)

	slot := c.variants[baseKey]
	for _, existing := range slot {
		if existing.key == key {
			existing.result = result
			existing.vary = cloneVary(vary)
			existing.expiresAt = expiresAt
			c.sequence++
			existing.sequence = c.sequence
			return true, nil
		}
	}

	c.sequence++
	stored := &entry{
		key:       key,
		result:    result,
		vary:      cloneVary(vary),
		expiresAt: expiresAt,
		sequence:  c.sequence,
	}
	c.variants[baseKey] = append(slot, stored)
	c.size++

	for c.maxEntries > 0 && c.size > c.maxEntries {
		c.evictOldestLocked()
	}
	return true, nil
}

// Put is an alias for Store.
func (c *Cache) Put(r *http.Request, response any, varyHeaders ...http.Header) (bool, error) {
	return c.Store(r, response, varyHeaders...)
}

// PutResult constructs and stores an authorization result.
func (c *Cache) PutResult(r *http.Request, statusCode int, header http.Header, body []byte) (bool, error) {
	return c.Store(r, NewResult(statusCode, header, body))
}

// Get returns a cached response for the request. If a Vary header is supplied,
// only entries with the same Vary declaration are eligible.
func (c *Cache) Get(r *http.Request, varyHeaders ...http.Header) (*Result, bool) {
	result, ok, _ := c.LookupResult(r, varyHeaders...)
	return result, ok
}

// Lookup is an alias for Get.
func (c *Cache) Lookup(r *http.Request, varyHeaders ...http.Header) (*Result, bool) {
	return c.Get(r, varyHeaders...)
}

// LookupResult behaves like Get and also reports request or Vary errors.
func (c *Cache) LookupResult(r *http.Request, varyHeaders ...http.Header) (*Result, bool, error) {
	if c == nil {
		return nil, false, ErrNilCache
	}
	baseKey, err := buildKey(r, nil)
	if err != nil {
		return nil, false, err
	}

	var expectedNames []string
	if len(varyHeaders) > 0 {
		expectedNames, err = parseVary(varyHeaders...)
		if err != nil {
			return nil, false, err
		}
	}

	now := c.currentTime()

	c.mu.Lock()
	defer c.mu.Unlock()

	slot := c.variants[baseKey]
	if len(slot) == 0 {
		return nil, false, nil
	}

	kept := slot[:0]
	var found *Result
	for _, candidate := range slot {
		if !now.Before(candidate.expiresAt) {
			c.size--
			continue
		}
		kept = append(kept, candidate)
		if found != nil {
			continue
		}
		if expectedNames != nil && !sameVaryNames(candidate.vary, varyValuesForRequest(r, expectedNames)) {
			continue
		}
		if !varyMatchesRequest(r, candidate.vary) {
			continue
		}
		cloned := cloneResult(candidate.result)
		found = &cloned
	}

	if len(kept) == 0 {
		delete(c.variants, baseKey)
	} else {
		c.variants[baseKey] = kept
	}
	if found == nil {
		return nil, false, nil
	}
	return found, true, nil
}

// Delete removes all cached variants for the request's method, host, path,
// query, and credentials.
func (c *Cache) Delete(r *http.Request) bool {
	if c == nil {
		return false
	}
	baseKey, err := buildKey(r, nil)
	if err != nil {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	slot := c.variants[baseKey]
	if len(slot) == 0 {
		return false
	}
	delete(c.variants, baseKey)
	c.size -= len(slot)
	return true
}

// PurgeExpired removes all expired entries and returns the number removed.
func (c *Cache) PurgeExpired() int {
	if c == nil {
		return 0
	}
	now := c.currentTime()
	c.mu.Lock()
	defer c.mu.Unlock()
	before := c.size
	c.purgeExpiredLocked(now)
	return before - c.size
}

// Len returns the number of unexpired cached variants.
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.PurgeExpired()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// Clear removes every cached variant.
func (c *Cache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.variants = make(map[string][]*entry)
	c.size = 0
}

// CacheTTL returns the cache lifetime for an authorization status.
func CacheTTL(statusCode int, header http.Header) (time.Duration, bool) {
	directives := parseCacheControl(header)
	switch statusCode {
	case http.StatusOK:
		if directives.noStore || directives.noCache || directives.invalidMaxAge || !directives.hasMaxAge {
			return 0, false
		}
		if directives.maxAge <= 0 {
			return 0, false
		}
		if directives.maxAge > MaxTTL {
			return MaxTTL, true
		}
		return directives.maxAge, true
	case http.StatusForbidden:
		if directives.noStore || directives.noCache {
			return 0, false
		}
		ttl := MaxDeniedTTL
		if directives.hasMaxAge && !directives.invalidMaxAge {
			ttl = directives.maxAge
		}
		if ttl <= 0 {
			return 0, false
		}
		if ttl > MaxDeniedTTL {
			ttl = MaxDeniedTTL
		}
		return ttl, true
	default:
		return 0, false
	}
}

// TTL is an alias for CacheTTL.
func TTL(statusCode int, header http.Header) (time.Duration, bool) {
	return CacheTTL(statusCode, header)
}

type cacheControl struct {
	noStore       bool
	noCache       bool
	hasMaxAge     bool
	invalidMaxAge bool
	maxAge        time.Duration
}

func parseCacheControl(header http.Header) cacheControl {
	var parsed cacheControl
	for _, line := range headerValues(header, "Cache-Control") {
		for _, directive := range splitDirectives(line) {
			name, value, hasValue := splitDirective(directive)
			switch name {
			case "no-store":
				parsed.noStore = true
			case "no-cache":
				parsed.noCache = true
			case "max-age":
				if !hasValue {
					parsed.invalidMaxAge = true
					continue
				}
				seconds, err := parseDeltaSeconds(value)
				if err != nil {
					parsed.invalidMaxAge = true
					continue
				}
				const maxDurationSeconds = int64(^uint64(0)>>1) / int64(time.Second)
				if seconds > maxDurationSeconds {
					seconds = maxDurationSeconds
				}
				age := time.Duration(seconds) * time.Second
				if !parsed.hasMaxAge || age < parsed.maxAge {
					parsed.maxAge = age
				}
				parsed.hasMaxAge = true
			}
		}
	}
	return parsed
}

func splitDirectives(value string) []string {
	var directives []string
	start := 0
	quoted := false
	escaped := false
	for i, r := range value {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			if directive := strings.TrimSpace(value[start:i]); directive != "" {
				directives = append(directives, directive)
			}
			start = i + 1
		}
	}
	if directive := strings.TrimSpace(value[start:]); directive != "" {
		directives = append(directives, directive)
	}
	return directives
}

func splitDirective(value string) (string, string, bool) {
	name, rawValue, hasValue := strings.Cut(value, "=")
	name = strings.ToLower(strings.TrimSpace(name))
	if !hasValue {
		return name, "", false
	}
	rawValue = strings.TrimSpace(rawValue)
	if len(rawValue) >= 2 && rawValue[0] == '"' && rawValue[len(rawValue)-1] == '"' {
		rawValue = rawValue[1 : len(rawValue)-1]
	}
	return name, rawValue, true
}

func parseDeltaSeconds(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, strconv.ErrSyntax
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return seconds, nil
}

func resultFromValue(value any) (Result, error) {
	switch response := value.(type) {
	case Result:
		return cloneResult(response), nil
	case *Result:
		if response == nil {
			return Result{}, errors.New("authcache: nil result")
		}
		return cloneResult(*response), nil
	case http.Response:
		return resultFromHTTPResponse(&response)
	case *http.Response:
		return resultFromHTTPResponse(response)
	default:
		return Result{}, fmt.Errorf("authcache: unsupported response type %T", value)
	}
}

func resultFromHTTPResponse(response *http.Response) (Result, error) {
	if response == nil {
		return Result{}, errors.New("authcache: nil response")
	}

	var body []byte
	if response.Body != nil {
		data, err := io.ReadAll(response.Body)
		if err != nil {
			return Result{}, fmt.Errorf("authcache: read authorization response body: %w", err)
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		body = data
	}

	return Result{
		StatusCode: response.StatusCode,
		Header:     cloneHeader(response.Header),
		Body:       append([]byte(nil), body...),
	}, nil
}

func cloneResult(result Result) Result {
	return Result{
		StatusCode: result.StatusCode,
		Header:     cloneHeader(result.Header),
		Body:       append([]byte(nil), result.Body...),
		ExpiresAt:  result.ExpiresAt,
	}
}

func cloneHeader(header http.Header) http.Header {
	if header == nil {
		return nil
	}
	return header.Clone()
}

func hasCredentials(r *http.Request) bool {
	return anyNonEmpty(requestHeaderValues(r, "Authorization")) ||
		anyNonEmpty(requestHeaderValues(r, "Cookie"))
}

func anyNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func (c *Cache) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cache) ensureReadyLocked() {
	if c.variants == nil {
		c.variants = make(map[string][]*entry)
	}
	if c.maxEntries == 0 {
		c.maxEntries = defaultMaxEntries
	}
}

func (c *Cache) purgeExpiredLocked(now time.Time) {
	for baseKey, slot := range c.variants {
		kept := slot[:0]
		for _, candidate := range slot {
			if now.Before(candidate.expiresAt) {
				kept = append(kept, candidate)
			} else {
				c.size--
			}
		}
		if len(kept) == 0 {
			delete(c.variants, baseKey)
		} else {
			c.variants[baseKey] = kept
		}
	}
}

func (c *Cache) removeOtherVaryDeclarationsLocked(baseKey string, vary []varyValue) {
	slot := c.variants[baseKey]
	if len(slot) == 0 {
		return
	}
	kept := slot[:0]
	for _, candidate := range slot {
		if sameVaryNames(candidate.vary, vary) {
			kept = append(kept, candidate)
		} else {
			c.size--
		}
	}
	if len(kept) == 0 {
		delete(c.variants, baseKey)
	} else {
		c.variants[baseKey] = kept
	}
}

func (c *Cache) evictOldestLocked() {
	var oldestBase string
	var oldest *entry
	for baseKey, slot := range c.variants {
		for _, candidate := range slot {
			if oldest == nil || candidate.sequence < oldest.sequence {
				oldest = candidate
				oldestBase = baseKey
			}
		}
	}
	if oldest == nil {
		c.size = 0
		return
	}

	slot := c.variants[oldestBase]
	kept := slot[:0]
	removed := false
	for _, candidate := range slot {
		if !removed && candidate == oldest {
			removed = true
			c.size--
			continue
		}
		kept = append(kept, candidate)
	}
	if len(kept) == 0 {
		delete(c.variants, oldestBase)
	} else {
		c.variants[oldestBase] = kept
	}
}
