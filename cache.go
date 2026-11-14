package netsec

import (
	"fmt"
	"sync"
	"time"
)

// Clock supplies the cache's controllable notion of time.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Option configures a Cache.
type Option func(*Cache)

// WithMaxEntries bounds the number of stored entries. A non-positive value
// means unbounded. Eviction is by earliest insertion time.
func WithMaxEntries(n int) Option {
	return func(c *Cache) {
		if n > 0 {
			c.maxEntries = n
		}
	}
}

type cacheEntry struct {
	key        CacheKey
	proof      *validatedProof
	insertedAt time.Time
}

// Cache is a concurrency-safe, per-view aggressive negative cache.
type Cache struct {
	mu         sync.Mutex
	clock      Clock
	policies   map[TenantView]Policy
	entries    map[string]*cacheEntry
	lastSeen   time.Time
	clockSeen  bool
	maxEntries int
	rollbacks  uint64
}

// NewCache creates an empty cache. A nil clock uses the system wall clock.
func NewCache(clock Clock, opts ...Option) *Cache {
	if clock == nil {
		clock = systemClock{}
	}
	c := &Cache{
		clock:    clock,
		policies: make(map[TenantView]Policy),
		entries:  make(map[string]*cacheEntry),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// SetPolicy installs or replaces a view policy. Any change invalidates the
// view's entries. Policy and key generations may not move backwards.
func (c *Cache) SetPolicy(policy Policy) error {
	policy = policy.withDefaults()
	if err := validatePolicy(policy); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.policies[policy.View]; ok {
		if policy.Generation < old.Generation {
			return fmt.Errorf("%w: policy generation moved backwards", ErrPolicyMismatch)
		}
		if policy.KeyGeneration < old.KeyGeneration {
			return fmt.Errorf("%w: key generation moved backwards", ErrKeyGeneration)
		}
	}
	c.policies[policy.View] = policy
	c.invalidateViewLocked(policy.View)
	return nil
}

// Policy returns a deep copy of the current view policy.
func (c *Cache) Policy(view TenantView) (Policy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.policies[view]
	if !ok {
		return Policy{}, false
	}
	p.AllowedParameters = cloneParameters(p.AllowedParameters)
	p.AllowedAlgorithms = append([]uint8(nil), p.AllowedAlgorithms...)
	p.Apexes = append([]Name(nil), p.Apexes...)
	return p, true
}

// RevokeKeys advances the key generation for a view and immediately drops all
// cached proofs from the old epoch.
func (c *Cache) RevokeKeys(view TenantView, newGeneration uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	old, ok := c.policies[view]
	if !ok {
		return ErrNoPolicy
	}
	if newGeneration <= old.KeyGeneration {
		return fmt.Errorf("%w: new key generation must advance", ErrKeyGeneration)
	}
	old.KeyGeneration = newGeneration
	c.policies[view] = old
	c.invalidateViewLocked(view)
	return nil
}

// InvalidateView drops all entries for a view without changing its policy.
func (c *Cache) InvalidateView(view TenantView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidateViewLocked(view)
}

// Clear drops all entries but keeps policies.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

// Len returns the number of stored entries. Expired entries are removed lazily
// on lookup.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Rollbacks reports how many clock rollbacks cleared the cache.
func (c *Cache) Rollbacks() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rollbacks
}

func (c *Cache) invalidateViewLocked(view TenantView) {
	for id, entry := range c.entries {
		if entry.key.View == view {
			delete(c.entries, id)
		}
	}
}

func (c *Cache) observeClockLocked() (time.Time, bool) {
	// Strip the monotonic reading so a wall-clock rollback is visible even when
	// the process monotonic clock keeps advancing.
	now := c.clock.Now().Round(0)
	rolledBack := false
	if c.clockSeen && now.Before(c.lastSeen) {
		c.entries = make(map[string]*cacheEntry)
		c.rollbacks++
		rolledBack = true
	}
	c.lastSeen = now
	c.clockSeen = true
	return now, rolledBack
}

// CheckClock advances the cache's clock observation. It returns
// ErrClockRollback when time moved backwards, in which case every cached
// entry has already been invalidated.
func (c *Cache) CheckClock() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, rolledBack := c.observeClockLocked(); rolledBack {
		return ErrClockRollback
	}
	return nil
}

// Insert is a convenience pointer form of Ingest.
func (c *Cache) Insert(resp *Response) (Answer, error) {
	if resp == nil {
		return Answer{}, fmt.Errorf("%w: nil response", ErrMalformed)
	}
	return c.Ingest(*resp)
}

// Ingest validates a per-view authenticated denial proof, stores it, and
// returns the synthesized negative answer.
func (c *Cache) Ingest(resp Response) (Answer, error) {
	if resp.Attestation.View == "" {
		return Answer{}, fmt.Errorf("%w: empty view", ErrViewMismatch)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now, _ := c.observeClockLocked()
	policy, ok := c.policies[resp.Attestation.View]
	if !ok {
		return Answer{}, ErrNoPolicy
	}
	answer, proof, err := evaluateResponse(&resp, policy, now)
	if err != nil {
		return Answer{}, err
	}

	id := proof.key.id()
	if old, exists := c.entries[id]; exists {
		if old.proof.fact.kind != proof.fact.kind {
			delete(c.entries, id)
			return Answer{}, fmt.Errorf("%w: contradictory negative conclusions for the same key", ErrAmbiguous)
		}
	}
	c.entries[id] = &cacheEntry{
		key:        proof.key,
		proof:      proof,
		insertedAt: now,
	}
	c.evictLocked()
	return answer, nil
}

func (c *Cache) evictLocked() {
	if c.maxEntries <= 0 {
		return
	}
	for len(c.entries) > c.maxEntries {
		var oldestID string
		var oldest *cacheEntry
		for id, entry := range c.entries {
			if oldest == nil || entry.insertedAt.Before(oldest.insertedAt) {
				oldestID, oldest = id, entry
			}
		}
		if oldest == nil {
			return
		}
		delete(c.entries, oldestID)
	}
}

func normalizeQuery(q Query) (Query, error) {
	if q.View == "" {
		return Query{}, fmt.Errorf("%w: empty view", ErrViewMismatch)
	}
	if q.Name.IsZero() {
		return Query{}, fmt.Errorf("%w: zero query name", ErrMalformed)
	}
	if !validQuestionType(q.Type) {
		return Query{}, fmt.Errorf("%w: unsupported query type %d", ErrMalformed, q.Type)
	}
	if q.Class == 0 {
		q.Class = ClassIN
	}
	if q.Apex.IsZero() {
		// No narrowing filter.
	} else if !q.Name.IsSubdomainOf(q.Apex) {
		return Query{}, fmt.Errorf("%w: query name outside requested apex", ErrMalformed)
	}
	return q, nil
}

// Lookup returns a cached negative answer for q, if one is live and its
// authenticated bitmap supports q.Type.
func (c *Cache) Lookup(q Query) (Answer, bool) {
	q, err := normalizeQuery(q)
	if err != nil {
		return Answer{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now, _ := c.observeClockLocked()
	policy, ok := c.policies[q.View]
	if !ok {
		return Answer{}, false
	}
	fingerprint := policy.fingerprint()

	var candidates []*cacheEntry
	for id, entry := range c.entries {
		if entry.key.View != q.View || entry.key.Class != q.Class || !entry.key.QueryName.Equal(q.Name) {
			continue
		}
		if !q.Apex.IsZero() && !entry.key.Apex.Equal(q.Apex) {
			continue
		}
		if q.Parameters != nil && !entry.key.Parameters.Equal(*q.Parameters) {
			continue
		}
		if entry.key.KeyGeneration != policy.KeyGeneration ||
			entry.proof.policyFingerprint != fingerprint ||
			entry.proof.policyGeneration != policy.Generation {
			delete(c.entries, id)
			continue
		}
		if now.Before(entry.proof.validFrom) || !now.Before(entry.proof.expiresAt) ||
			now.Before(entry.insertedAt) {
			delete(c.entries, id)
			continue
		}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 {
		return Answer{}, false
	}

	bestDepth := -1
	var top []*cacheEntry
	for _, entry := range candidates {
		depth := entry.key.Apex.LabelCount()
		switch {
		case depth > bestDepth:
			bestDepth = depth
			top = []*cacheEntry{entry}
		case depth == bestDepth:
			top = append(top, entry)
		}
	}
	if len(top) != 1 {
		// Multiple parameter sets/NSEC families at the same apex are not
		// interchangeable. Refuse to guess.
		return Answer{}, false
	}
	entry := top[0]
	if !entry.proof.fact.supports(q.Type) {
		return Answer{}, false
	}
	answer := Answer{
		Result:           entry.proof.fact.kind,
		View:             entry.key.View,
		Apex:             entry.key.Apex,
		Class:            entry.key.Class,
		QueryName:        q.Name,
		QueryType:        q.Type,
		Parameters:       entry.key.Parameters.Clone(),
		KeyGeneration:    entry.key.KeyGeneration,
		PolicyGeneration: entry.proof.policyGeneration,
		ValidFrom:        entry.proof.validFrom,
		ValidUntil:       entry.proof.expiresAt,
		Witnesses:        cloneWitnesses(entry.proof.witnesses),
		CacheHit:         true,
	}
	return answer, true
}
