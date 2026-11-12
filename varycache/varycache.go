// Package varycache implements cache admission and variant selection for an
// HTTP proxy whose cache key is built from a fixed set of request headers.
//
// An origin response may depend on request headers that the proxy did not
// include in its cache key. Such a response must never be admitted: serving it
// to another request that differs on an undeclared header would leak one
// client's response to another. Every stored response therefore carries the
// identity header set parsed from the origin's Vary header, and admission is
// granted only when that entire set is covered by the headers used to build
// the cache key.
//
// Each admitted variant keeps an independent snapshot of the original request
// headers, so later mutation of the caller's header map cannot change the
// stored variant. When the origin changes the identity header set for a cache
// key, the previous variants are replaced atomically with a new generation;
// revalidations that reference the superseded generation are rejected instead
// of being installed over the new variants.
package varycache

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrNotCovered indicates that an origin response varies on at least one
// request header that is not part of the proxy's cache-key headers, or that it
// uses the wildcard Vary field-value. The response must not be cached.
var ErrNotCovered = errors.New("varycache: response varies on headers not covered by the cache key")

// ErrStaleGeneration indicates that a revalidation references a variant
// generation that has been superseded because the origin changed its identity
// header set. The revalidated response is discarded.
var ErrStaleGeneration = errors.New("varycache: revalidation targets a superseded variant generation")

// StoredResponse is the cacheable representation of an origin response. The
// Vary field is read from Header and must declare every request header the
// origin used while selecting the response.
type StoredResponse struct {
	Header  http.Header
	Payload []byte
}

// Generation identifies the current variant generation for a cache key. A new
// generation starts whenever the origin changes the identity header set.
type Generation uint64

// variant pairs a stored response with the request-header snapshot that
// selected it. Both maps are owned exclusively by the variant.
type variant struct {
	requestHeader http.Header
	response      *StoredResponse
}

// entry is an immutable variant generation for one cache key. Every mutation
// builds a replacement entry and swaps it in atomically, so concurrent readers
// always observe a coherent generation.
type entry struct {
	generation Generation
	identity   []string
	variants   []*variant
}

// withVariant returns a copy of the entry containing v, replacing an existing
// variant selected by the same identity header values rather than appending.
func (e *entry) withVariant(identity []string, v *variant) *entry {
	next := &entry{
		generation: e.generation,
		identity:   identity,
		variants:   make([]*variant, 0, len(e.variants)+1),
	}
	for _, existing := range e.variants {
		if headersMatch(identity, existing.requestHeader, v.requestHeader) {
			// The new response revalidates an existing variant; it replaces it.
			continue
		}
		next.variants = append(next.variants, existing)
	}
	next.variants = append(next.variants, v)
	return next
}

// Cache stores at most one entry per cache key. The zero value is not usable;
// create instances with New.
type Cache struct {
	keyHeaders []string

	mu      sync.RWMutex
	entries map[string]*atomic.Pointer[entry]
}

// New creates a Cache whose cache keys are derived from keyHeaders. Header
// names are canonicalized and de-duplicated.
func New(keyHeaders []string) *Cache {
	canonical := make([]string, 0, len(keyHeaders))
	for _, name := range keyHeaders {
		canonical = append(canonical, http.CanonicalHeaderKey(name))
	}
	sort.Strings(canonical)
	canonical = slices.Compact(canonical)

	return &Cache{
		keyHeaders: canonical,
		entries:    make(map[string]*atomic.Pointer[entry]),
	}
}

// ParseVary extracts the canonical, sorted and de-duplicated request header
// names declared by a Vary header. The second result is false when the origin
// sends Vary: *, which forbids caching for ordinary requests.
func ParseVary(header http.Header) ([]string, bool) {
	var names []string
	for _, field := range header.Values("Vary") {
		for _, name := range strings.Split(field, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if name == "*" {
				return nil, false
			}
			names = append(names, http.CanonicalHeaderKey(name))
		}
	}
	sort.Strings(names)
	return slices.Compact(names), true
}

// missingHeaders returns the identity headers that are not covered by the
// cache-key header set. Both slices are sorted.
func (c *Cache) missingHeaders(identity []string) []string {
	var missing []string
	for _, name := range identity {
		if _, found := slices.BinarySearch(c.keyHeaders, name); !found {
			missing = append(missing, name)
		}
	}
	return missing
}

// slot returns the atomic entry pointer for cacheKey, creating it when
// create is true.
func (c *Cache) slot(cacheKey string, create bool) *atomic.Pointer[entry] {
	c.mu.RLock()
	slot := c.entries[cacheKey]
	c.mu.RUnlock()
	if slot != nil || !create {
		return slot
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if slot = c.entries[cacheKey]; slot == nil {
		slot = &atomic.Pointer[entry]{}
		c.entries[cacheKey] = slot
	}
	return slot
}

// Admit stores an origin response under cacheKey when the identity header set
// declared by its Vary header is fully covered by the cache's key headers.
// reqHeader is the request header instance that selected the response; it is
// snapshotted and never retained by reference.
//
// When the identity set differs from the generation currently stored for
// cacheKey, all previous variants are atomically invalidated and a new
// generation begins. The returned Generation can later be passed to
// Revalidate.
func (c *Cache) Admit(cacheKey string, reqHeader, respHeader http.Header, payload []byte) (Generation, error) {
	identity, ok := ParseVary(respHeader)
	if !ok {
		return 0, fmt.Errorf("%w: Vary: *", ErrNotCovered)
	}
	if missing := c.missingHeaders(identity); len(missing) > 0 {
		return 0, fmt.Errorf("%w: %s", ErrNotCovered, strings.Join(missing, ", "))
	}

	slot := c.slot(cacheKey, true)
	stored := newVariant(reqHeader, respHeader, payload)

	for {
		current := slot.Load()
		var next *entry
		if current == nil {
			next = (&entry{generation: 1, identity: identity}).withVariant(identity, stored)
		} else if !slices.Equal(current.identity, identity) {
			// Identity set changed: drop every old variant atomically.
			next = (&entry{
				generation: current.generation + 1,
				identity:   identity,
			}).withVariant(identity, stored)
		} else {
			next = current.withVariant(identity, stored)
		}

		if slot.CompareAndSwap(current, next) {
			return next.generation, nil
		}
	}
}

// Revalidate installs a revalidated response for an in-flight generation. The
// generation must match the generation currently stored for cacheKey; a late
// revalidation arriving after the identity set changed fails with
// ErrStaleGeneration and its response is never stored.
func (c *Cache) Revalidate(cacheKey string, generation Generation, reqHeader, respHeader http.Header, payload []byte) error {
	identity, ok := ParseVary(respHeader)
	if !ok {
		return fmt.Errorf("%w: Vary: *", ErrNotCovered)
	}
	if missing := c.missingHeaders(identity); len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrNotCovered, strings.Join(missing, ", "))
	}

	slot := c.slot(cacheKey, false)
	if slot == nil {
		return ErrStaleGeneration
	}
	stored := newVariant(reqHeader, respHeader, payload)

	for {
		current := slot.Load()
		if current == nil || current.generation != generation {
			return ErrStaleGeneration
		}
		// Defense in depth: generation equality implies the same identity.
		if !slices.Equal(current.identity, identity) {
			return ErrStaleGeneration
		}
		next := current.withVariant(identity, stored)
		if slot.CompareAndSwap(current, next) {
			return nil
		}
	}
}

// Lookup returns a copy of the stored response whose variant matches
// reqHeader, or nil when no variant matches. A response whose identity set is
// no longer covered by the cache-key headers is treated as a miss so that an
// undifferentiated response can never be served.
func (c *Cache) Lookup(cacheKey string, reqHeader http.Header) *StoredResponse {
	c.mu.RLock()
	slot := c.entries[cacheKey]
	c.mu.RUnlock()
	if slot == nil {
		return nil
	}

	current := slot.Load()
	if current == nil || len(c.missingHeaders(current.identity)) > 0 {
		return nil
	}

	for _, v := range current.variants {
		if headersMatch(current.identity, reqHeader, v.requestHeader) {
			return cloneStoredResponse(v.response)
		}
	}
	return nil
}

// Generation reports the current variant generation for cacheKey. The boolean
// is false when the key has no admitted entry.
func (c *Cache) Generation(cacheKey string) (Generation, bool) {
	c.mu.RLock()
	slot := c.entries[cacheKey]
	c.mu.RUnlock()
	if slot == nil {
		return 0, false
	}
	current := slot.Load()
	if current == nil {
		return 0, false
	}
	return current.generation, true
}

// newVariant snapshots the request and response data so the cache never shares
// mutable maps or byte slices with its callers.
func newVariant(reqHeader, respHeader http.Header, payload []byte) *variant {
	body := make([]byte, len(payload))
	copy(body, payload)

	return &variant{
		requestHeader: reqHeader.Clone(),
		response: &StoredResponse{
			Header:  respHeader.Clone(),
			Payload: body,
		},
	}
}

func cloneStoredResponse(in *StoredResponse) *StoredResponse {
	body := make([]byte, len(in.Payload))
	copy(body, in.Payload)
	return &StoredResponse{
		Header:  in.Header.Clone(),
		Payload: body,
	}
}

// headersMatch reports whether the identity header values of two request
// header instances are equal. Multi-valued headers are compared as ordered
// field values, matching RFC 9111 variant selection.
func headersMatch(identity []string, a, b http.Header) bool {
	for _, name := range identity {
		if !slices.Equal(a.Values(name), b.Values(name)) {
			return false
		}
	}
	return true
}
