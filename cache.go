package netsec

import (
	"net/http"
	"sync"
)

// Entry is one stored response plus the constraints under which it may be
// served again.
type Entry struct {
	Status         int
	Headers        http.Header
	VaryFields     []string
	VariantKey     string
	CredentialHash string
	MustRevalidate bool
	NoTransform    bool
}

// LookupResult is the outcome of a cache probe.
type LookupResult struct {
	Hit bool
	// Entry is populated on a usable hit.
	Entry *Entry
	// Reason explains a miss; on a hit it is ReasonStorable.
	Reason string
	// MustRevalidate indicates the hit cannot be served without contacting
	// the origin again.
	MustRevalidate bool
}

// SharedCache is a concurrency-safe in-memory edge cache. It only ever holds
// responses that passed Decide, and every lookup re-checks the requesting
// user's credential context before a hit is returned.
type SharedCache struct {
	mu      sync.RWMutex
	entries map[string]map[string]*Entry
}

// NewSharedCache constructs an empty SharedCache.
func NewSharedCache() *SharedCache {
	return &SharedCache{entries: map[string]map[string]*Entry{}}
}

// Store evaluates cacheability and, when allowed, records the response. It
// returns the underlying Decision so callers can log why something was
// rejected. Neither the URL extension nor Accept influence the result.
func (c *SharedCache) Store(req *http.Request, status int, headers http.Header) Decision {
	decision := Decide(req, status, headers)
	if !decision.Storable {
		return decision
	}

	entry := &Entry{
		Status:         status,
		Headers:        cloneHeaders(headers),
		VaryFields:     append([]string(nil), decision.VaryFields...),
		VariantKey:     VariantKey(req, decision.VaryFields),
		CredentialHash: CredentialFingerprint(req),
		MustRevalidate: decision.MustRevalidate,
		NoTransform:    decision.NoTransform,
	}

	primary := PrimaryKey(req)
	c.mu.Lock()
	variants := c.entries[primary]
	if variants == nil {
		variants = map[string]*Entry{}
		c.entries[primary] = variants
	}
	variants[entry.VariantKey] = entry
	c.mu.Unlock()
	return decision
}

// Lookup probes the cache for req. A hit requires three things at once:
// a stored primary key, an exact match of every Vary-selected request header,
// and an identical credential context. A mismatch in the last two is a miss,
// never a cross-user or cross-variant response.
func (c *SharedCache) Lookup(req *http.Request) LookupResult {
	primary := PrimaryKey(req)

	c.mu.RLock()
	variants := c.entries[primary]
	c.mu.RUnlock()
	if len(variants) == 0 {
		return LookupResult{Hit: false, Reason: "no-cache-entry"}
	}

	currentCredential := CredentialFingerprint(req)
	for _, candidate := range variants {
		if candidate.VariantKey != VariantKey(req, candidate.VaryFields) {
			continue
		}
		// Re-verify the credential context right before every hit. An
		// anonymous request must never receive a credential-bound response
		// and two different users must never share an entry.
		if candidate.CredentialHash != currentCredential {
			return LookupResult{Hit: false, Reason: "credential-context-mismatch"}
		}
		if candidate.MustRevalidate {
			return LookupResult{
				Hit:            true,
				Entry:          candidate,
				Reason:         "needs-revalidation",
				MustRevalidate: true,
			}
		}
		return LookupResult{Hit: true, Entry: candidate, Reason: ReasonStorable}
	}
	return LookupResult{Hit: false, Reason: "vary-variant-mismatch"}
}

func cloneHeaders(h http.Header) http.Header {
	clone := make(http.Header, len(h))
	for key, values := range h {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
