package proxy

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var errRevalidationAborted = errors.New("revalidation aborted by client")

// cachedEntry is one stored response, i.e. one Vary variant of a resource.
type cachedEntry struct {
	status int
	header http.Header
	body   []byte

	requestPath string
	method      string
	host        string

	vary          []string
	variantValues map[string]string
	variantKey    string

	authorized    bool
	credentialKey string

	date       time.Time
	freshUntil time.Time
	// serveStaleUntil is the end of the stale-while-revalidate window.
	// The zero value forbids stale serving.
	serveStaleUntil time.Time
	noStore         bool
	noCache         bool
	mustRevalidate  bool

	reval   *revalidation
	lruElem *list.Element
}

// resourceEntry holds every stored variant of one request-URI.
type resourceEntry struct {
	variants map[string]*cachedEntry
	order    []string
}

type revalidation struct {
	ready chan struct{}
	err   error
	mu    sync.Mutex
	done  bool
}

func newRevalidation() *revalidation {
	return &revalidation{ready: make(chan struct{})}
}

func (r *revalidation) finish(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.done = true
	r.err = err
	close(r.ready)
}

// Cache is a bounded, concurrency-safe shared response cache.
type Cache struct {
	capacity int
	clock    Clock

	mu        sync.Mutex
	resources map[string]*resourceEntry
	lru       *list.List
}

// NewCache creates a cache holding at most capacity entries, one entry per
// Vary variant.
func NewCache(capacity int, clock Clock) *Cache {
	if capacity <= 0 {
		capacity = 1
	}
	return &Cache{
		capacity:  capacity,
		clock:     clock,
		resources: make(map[string]*resourceEntry),
		lru:       list.New(),
	}
}

// Len returns the number of cached entries across all resources.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

func requestResourceKey(req *http.Request) string {
	return req.Method + " " + req.Host + req.URL.RequestURI()
}

func canonicalVary(header http.Header) []string {
	var names []string
	seen := make(map[string]bool)
	for _, field := range header.Values("Vary") {
		for _, name := range strings.Split(field, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			canonical := http.CanonicalHeaderKey(name)
			if !seen[canonical] {
				seen[canonical] = true
				names = append(names, canonical)
			}
		}
	}
	sort.Strings(names)
	return names
}

func joinHeaderValues(header http.Header, name string) string {
	return strings.Join(header.Values(name), ",")
}

func buildVariantKey(vary []string, values map[string]string) string {
	parts := make([]string, 0, len(vary))
	for _, name := range vary {
		parts = append(parts, name+"="+values[name])
	}
	return strings.Join(parts, "|")
}

// storageVariantKey extends the Vary-based key with the authorization
// binding so anonymous and credential-bound responses never overwrite one
// another or get served across credentials.
func storageVariantKey(vary []string, values map[string]string, authorized bool, credential string) string {
	base := buildVariantKey(vary, values)
	if authorized {
		return "auth:" + credential + "|" + base
	}
	return "anon|" + base
}

func headerValuesFor(vary []string, header http.Header) map[string]string {
	values := make(map[string]string, len(vary))
	for _, name := range vary {
		values[name] = joinHeaderValues(header, name)
	}
	return values
}

func credentialKey(authorization string) string {
	sum := sha256.Sum256([]byte(authorization))
	return hex.EncodeToString(sum[:])
}

// Lookup returns the variant matching req, or nil when none is stored.
func (c *Cache) Lookup(req *http.Request) *cachedEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	resource, ok := c.resources[requestResourceKey(req)]
	if !ok {
		return nil
	}
	for _, key := range resource.order {
		entry := resource.variants[key]
		if !entry.matches(req) {
			continue
		}
		if entry.lruElem != nil {
			c.lru.MoveToFront(entry.lruElem)
		}
		return entry
	}
	return nil
}

func (entry *cachedEntry) matches(req *http.Request) bool {
	for _, name := range entry.vary {
		if joinHeaderValues(req.Header, name) != entry.variantValues[name] {
			return false
		}
	}
	authorization := req.Header.Get("Authorization")
	if entry.authorized {
		// A response stored for an authorized request is bound to that exact
		// credential and is never reused anonymously.
		return authorization != "" && credentialKey(authorization) == entry.credentialKey
	}
	return authorization == ""
}

// BuildEntry parses cache metadata out of a response without touching the
// cache. It returns nil and a reason when the response is not cacheable in
// the shared cache.
func (c *Cache) BuildEntry(req *http.Request, status int, header http.Header, body []byte) (*cachedEntry, string) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, "method not cacheable"
	}
	directives := parseCacheControl(header.Get("Cache-Control"))
	if directives.has("no-store") {
		return nil, "no-store"
	}
	if header.Get("Vary") == "*" {
		return nil, "Vary: *"
	}
	if status != http.StatusOK {
		return nil, "status not cacheable"
	}

	authorization := req.Header.Get("Authorization")
	if authorization != "" {
		switch {
		case directives.has("no-cache"), directives.has("private"):
			return nil, "authorized response is not shared-cacheable"
		case directives.has("public"), directives.has("must-revalidate"),
			directives.has("s-maxage"), header.Get("Expires") != "":
			// Explicit shared-cache permission per RFC 7234 section 3.2.
			// The entry stays bound to the credential and is never served
			// anonymously or to another user.
		default:
			return nil, "authorized response without explicit shared-cache permission"
		}
	}

	vary := canonicalVary(header)
	values := headerValuesFor(vary, req.Header)

	now := c.clock.Now()
	date := now
	if parsed, err := http.ParseTime(header.Get("Date")); err == nil {
		date = parsed
	}

	freshFor := time.Duration(0)
	if d, ok := directives.seconds("s-maxage"); ok {
		freshFor = d
	} else if d, ok := directives.seconds("max-age"); ok {
		freshFor = d
	} else if expires := header.Get("Expires"); expires != "" {
		if parsed, err := http.ParseTime(expires); err == nil && parsed.After(date) {
			freshFor = parsed.Sub(date)
		}
	}

	entry := &cachedEntry{
		status:         status,
		header:         header.Clone(),
		body:           append([]byte(nil), body...),
		requestPath:    requestResourceKey(req),
		method:         req.Method,
		host:           req.Host,
		vary:           vary,
		variantValues:  values,
		variantKey:     storageVariantKey(vary, values, authorization != "", credentialKey(authorization)),
		authorized:     authorization != "",
		credentialKey:  credentialKey(authorization),
		date:           date,
		freshUntil:     now.Add(freshFor),
		noStore:        directives.has("no-store"),
		noCache:        directives.has("no-cache"),
		mustRevalidate: directives.has("must-revalidate"),
	}
	if swr, ok := directives.seconds("stale-while-revalidate"); ok && swr > 0 {
		entry.serveStaleUntil = entry.freshUntil.Add(swr)
	}
	return entry, ""
}

// Store inserts or replaces the matching variant. All index changes happen
// under the cache lock so Lookup never observes a partial index.
func (c *Cache) Store(entry *cachedEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resource, ok := c.resources[entry.requestPath]
	if !ok {
		resource = &resourceEntry{variants: make(map[string]*cachedEntry)}
		c.resources[entry.requestPath] = resource
	}

	if old, exists := resource.variants[entry.variantKey]; exists && !sameVary(old.vary, entry.vary) {
		c.removeResourceLocked(entry.requestPath)
		resource = &resourceEntry{variants: make(map[string]*cachedEntry)}
		c.resources[entry.requestPath] = resource
	}

	if old, exists := resource.variants[entry.variantKey]; exists {
		if old.lruElem != nil {
			c.lru.Remove(old.lruElem)
		}
		entry.reval = old.reval
	}

	entry.lruElem = c.lru.PushFront(entry)
	resource.variants[entry.variantKey] = entry
	if !contains(resource.order, entry.variantKey) {
		resource.order = append(resource.order, entry.variantKey)
	}

	for c.lru.Len() > c.capacity {
		c.evictOldestLocked()
	}
}

func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}

func sameVary(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *Cache) evictOldestLocked() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	oldest, ok := elem.Value.(*cachedEntry)
	if !ok {
		c.lru.Remove(elem)
		return
	}
	if resource := c.resources[oldest.requestPath]; resource != nil {
		delete(resource.variants, oldest.variantKey)
		resource.order = removeString(resource.order, oldest.variantKey)
		if len(resource.variants) == 0 {
			delete(c.resources, oldest.requestPath)
		}
	}
	c.lru.Remove(elem)
}

func removeString(slice []string, value string) []string {
	out := slice[:0]
	for _, item := range slice {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}

func (c *Cache) removeResourceLocked(path string) {
	resource, ok := c.resources[path]
	if !ok {
		return
	}
	for key, entry := range resource.variants {
		if entry.lruElem != nil {
			c.lru.Remove(entry.lruElem)
		}
		delete(resource.variants, key)
	}
	delete(c.resources, path)
}

// RevalidateWith304 merges a 304 response into the existing entry. revalReq
// is the conditional request that produced the 304; its headers supply
// values for any Vary dimensions introduced by the 304. When the
// revalidation changes Vary or the caching directives, the resource index is
// rebuilt atomically and incompatible variants are dropped.
func (c *Cache) RevalidateWith304(entry *cachedEntry, revalReq *http.Request, notModifiedHeader http.Header) *cachedEntry {
	merged := entry.header.Clone()
	for key, values := range notModifiedHeader {
		canonical := http.CanonicalHeaderKey(key)
		if hopByHop(canonical) {
			continue
		}
		switch canonical {
		case "Content-Length", "Content-Range", "Content-Encoding",
			"Content-Type", "Transfer-Encoding":
			continue
		default:
			merged[canonical] = append([]string(nil), values...)
		}
	}

	newVary := canonicalVary(merged)
	newValues := headerValuesFor(newVary, revalReq.Header)
	for name, value := range entry.variantValues {
		if _, exists := newValues[name]; !exists {
			newValues[name] = value
		}
	}
	directives := parseCacheControl(merged.Get("Cache-Control"))
	now := c.clock.Now()

	freshFor := time.Duration(0)
	if d, ok := directives.seconds("s-maxage"); ok {
		freshFor = d
	} else if d, ok := directives.seconds("max-age"); ok {
		freshFor = d
	} else if expires := merged.Get("Expires"); expires != "" {
		if parsed, err := http.ParseTime(expires); err == nil && parsed.After(entry.date) {
			freshFor = parsed.Sub(entry.date)
		}
	}

	refreshed := &cachedEntry{
		status:         entry.status,
		header:         merged,
		body:           entry.body,
		requestPath:    entry.requestPath,
		method:         entry.method,
		host:           entry.host,
		vary:           newVary,
		variantValues:  newValues,
		variantKey:     storageVariantKey(newVary, newValues, entry.authorized, entry.credentialKey),
		authorized:     entry.authorized,
		credentialKey:  entry.credentialKey,
		date:           now,
		freshUntil:     now.Add(freshFor),
		noStore:        directives.has("no-store"),
		noCache:        directives.has("no-cache"),
		mustRevalidate: directives.has("must-revalidate"),
	}
	if swr, ok := directives.seconds("stale-while-revalidate"); ok && swr > 0 {
		refreshed.serveStaleUntil = refreshed.freshUntil.Add(swr)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	rebuild := !sameVary(entry.vary, newVary) || directives.has("no-store") || merged.Get("Vary") == "*"
	if rebuild {
		c.removeResourceLocked(entry.requestPath)
		if directives.has("no-store") || merged.Get("Vary") == "*" {
			return nil
		}
		c.resources[entry.requestPath] = &resourceEntry{variants: make(map[string]*cachedEntry)}
	}

	resource := c.resources[entry.requestPath]
	if old, exists := resource.variants[entry.variantKey]; exists && old.lruElem != nil {
		c.lru.Remove(old.lruElem)
	}
	refreshed.lruElem = c.lru.PushFront(refreshed)
	resource.variants[refreshed.variantKey] = refreshed
	if !contains(resource.order, refreshed.variantKey) {
		resource.order = append(resource.order, refreshed.variantKey)
	}
	if entry.variantKey != refreshed.variantKey {
		delete(resource.variants, entry.variantKey)
		resource.order = removeString(resource.order, entry.variantKey)
	}

	for c.lru.Len() > c.capacity {
		c.evictOldestLocked()
	}
	return refreshed
}

// beginRevalidation marks entry as being revalidated. The caller becomes the
// leader when it returns true; concurrent callers coalesce on the same
// in-flight revalidation.
func (c *Cache) beginRevalidation(entry *cachedEntry) (*revalidation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.reval != nil {
		return entry.reval, false
	}
	entry.reval = newRevalidation()
	return entry.reval, true
}

// finishRevalidation clears the in-flight marker and wakes waiters.
func (c *Cache) finishRevalidation(entry *cachedEntry, reval *revalidation, err error) {
	c.mu.Lock()
	if entry.reval == reval {
		entry.reval = nil
	}
	c.mu.Unlock()
	reval.finish(err)
}

// abortRevalidation cancels an in-flight revalidation on behalf of a
// disconnected synchronous caller so waiting requests do not hang.
func (c *Cache) abortRevalidation(entry *cachedEntry, reval *revalidation) {
	c.mu.Lock()
	if entry.reval == reval {
		entry.reval = nil
	}
	c.mu.Unlock()
	reval.finish(errRevalidationAborted)
}

// isFresh reports whether the entry is fresh at now, honoring request
// max-age and min-fresh constraints.
func (entry *cachedEntry) isFresh(now time.Time, requestDirectives cacheDirectives) bool {
	if entry.noCache {
		return false
	}
	freshUntil := entry.freshUntil
	if d, ok := requestDirectives.seconds("max-age"); ok {
		freshUntil = entry.date.Add(d)
	}
	if minFresh, ok := requestDirectives.seconds("min-fresh"); ok {
		if now.Add(minFresh).After(freshUntil) {
			return false
		}
	}
	return now.Before(freshUntil)
}

// staleAllowance returns how long beyond expiry the entry may still be served
// to this request. must-revalidate / no-cache entries never go stale.
func (entry *cachedEntry) staleAllowance(requestDirectives cacheDirectives) time.Duration {
	if entry.mustRevalidate || entry.noCache {
		return 0
	}
	allowance := time.Duration(0)
	if !entry.serveStaleUntil.IsZero() {
		if window := entry.serveStaleUntil.Sub(entry.freshUntil); window > 0 {
			allowance = window
		}
	}
	if maxStale, ok := requestDirectives.seconds("max-stale"); ok {
		if allowance == 0 || maxStale < allowance {
			allowance = maxStale
		}
	}
	return allowance
}

// serveStale reports whether the expired entry is still within its allowance.
func (entry *cachedEntry) serveStale(now time.Time, requestDirectives cacheDirectives) bool {
	allowance := entry.staleAllowance(requestDirectives)
	if allowance <= 0 {
		return false
	}
	return now.Before(entry.freshUntil.Add(allowance))
}
