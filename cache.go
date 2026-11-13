package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrCacheClosed indicates use of a closed cache.
	ErrCacheClosed = errors.New("netsec: credential cache closed")
	// ErrRoleRevoked indicates that new credentials may not be issued for the
	// identity's role.
	ErrRoleRevoked = errors.New("netsec: role revoked")
	// ErrPolicyMismatch indicates that the requested policy digest is no
	// longer the current digest for the tenant and role.
	ErrPolicyMismatch = errors.New("netsec: policy digest is no longer current")
	// ErrStaleGeneration indicates that the request belongs to an old
	// credential-provider configuration generation.
	ErrStaleGeneration = errors.New("netsec: stale provider configuration generation")
	// ErrGenerationRollback indicates an attempt to move the configuration
	// generation backwards.
	ErrGenerationRollback = errors.New("netsec: provider configuration generation rollback")
	// ErrCacheKeyCollision indicates that two distinct canonical identities
	// produced the same cache key. The cache fails closed.
	ErrCacheKeyCollision = errors.New("netsec: canonical identity cache key collision")
	// ErrCredentialUnavailable indicates that the cached secret handle can no
	// longer be cloned, typically because the entry was invalidated.
	ErrCredentialUnavailable = errors.New("netsec: credential no longer available")
	// ErrRefreshAbandoned indicates that a result arrived after the refresh
	// lost its last waiter or was invalidated.
	ErrRefreshAbandoned = errors.New("netsec: refresh result abandoned")
)

const (
	// DefaultSafetyMargin is how long before STS expiry a cached credential
	// stops being lent.
	DefaultSafetyMargin = 30 * time.Second
	// DefaultClockSkew is the tolerated amount by which an STS issue time may
	// lead the host clock.
	DefaultClockSkew = 2 * time.Minute
)

type cacheConfig struct {
	clock        func() time.Time
	safetyMargin time.Duration
	clockSkew    time.Duration
	maxTTL       time.Duration
	generation   uint64
}

// Option configures a Cache.
type Option func(*cacheConfig) error

// WithClock sets the cache's clock. It is primarily useful for tests.
func WithClock(clock func() time.Time) Option {
	return func(c *cacheConfig) error {
		if clock == nil {
			return errors.New("netsec: nil clock")
		}
		c.clock = clock
		return nil
	}
}

// WithSafetyMargin sets the host safety margin subtracted from STS expiry.
func WithSafetyMargin(margin time.Duration) Option {
	return func(c *cacheConfig) error {
		if margin < 0 {
			return errors.New("netsec: negative safety margin")
		}
		c.safetyMargin = margin
		return nil
	}
}

// WithClockSkew sets the tolerated future skew of an STS issue time.
func WithClockSkew(skew time.Duration) Option {
	return func(c *cacheConfig) error {
		if skew < 0 {
			return errors.New("netsec: negative clock skew")
		}
		c.clockSkew = skew
		return nil
	}
}

// WithMaxTTL caps how long a fresh result may be cached, independent of the
// STS expiry. A zero value disables the cap.
func WithMaxTTL(ttl time.Duration) Option {
	return func(c *cacheConfig) error {
		if ttl < 0 {
			return errors.New("netsec: negative max TTL")
		}
		c.maxTTL = ttl
		return nil
	}
}

// WithGeneration sets the initial credential-provider configuration
// generation.
func WithGeneration(generation uint64) Option {
	return func(c *cacheConfig) error {
		c.generation = generation
		return nil
	}
}

type cacheEntry struct {
	identity     Identity
	accessKeyID  string
	sessionToken string
	issuedAt     time.Time
	expiresAt    time.Time
	usableUntil  time.Time
	secret       SecretHandle
}

type flight struct {
	key      Digest
	identity Identity
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}

	waiters   int
	completed bool
	err       error
	entry     *cacheEntry
}

type roleRef struct {
	tenantID string
	roleID   string
}

// Cache caches STS results by canonical Identity. A cached entry is never lent
// outside its safety-margin-adjusted lifetime, and each borrow receives an
// independent lease over a clone of the secret handle.
type Cache struct {
	mu sync.Mutex

	provider     Provider
	clock        func() time.Time
	safetyMargin time.Duration
	clockSkew    time.Duration
	maxTTL       time.Duration
	generation   uint64

	entries        map[Digest]*cacheEntry
	flights        map[Digest]*flight
	revoked        map[roleRef]struct{}
	revokedRoleIDs map[string]struct{}
	policies       map[roleRef]Digest
	closed         bool
	closedCh       chan struct{}
}

// NewCache constructs a credential cache for provider.
func NewCache(provider Provider, options ...Option) (*Cache, error) {
	if provider == nil {
		return nil, errors.New("netsec: nil credential provider")
	}
	cfg := cacheConfig{
		clock:        time.Now,
		safetyMargin: DefaultSafetyMargin,
		clockSkew:    DefaultClockSkew,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}
	return &Cache{
		provider:       provider,
		clock:          cfg.clock,
		safetyMargin:   cfg.safetyMargin,
		clockSkew:      cfg.clockSkew,
		maxTTL:         cfg.maxTTL,
		generation:     cfg.generation,
		entries:        make(map[Digest]*cacheEntry),
		flights:        make(map[Digest]*flight),
		revoked:        make(map[roleRef]struct{}),
		revokedRoleIDs: make(map[string]struct{}),
		policies:       make(map[roleRef]Digest),
		closedCh:       make(chan struct{}),
	}, nil
}

// New is an alias for NewCache.
func New(provider Provider, options ...Option) (*Cache, error) {
	return NewCache(provider, options...)
}

// Generation returns the current provider configuration generation.
func (c *Cache) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// SetGeneration advances the provider configuration generation and
// immediately invalidates all entries and in-flight refreshes from the old
// generation. Moving backwards is rejected.
func (c *Cache) SetGeneration(generation uint64) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCacheClosed
	}
	if generation < c.generation {
		c.mu.Unlock()
		return fmt.Errorf("%w: %d -> %d", ErrGenerationRollback, c.generation, generation)
	}
	if generation == c.generation {
		c.mu.Unlock()
		return nil
	}
	c.generation = generation
	secrets := c.removeEntriesLocked(func(*cacheEntry) bool { return true })
	c.cancelAllFlightsLocked()
	c.mu.Unlock()
	destroySecrets(secrets)
	return nil
}

// BumpGeneration advances the configuration generation by one and returns the
// new value.
func (c *Cache) BumpGeneration() uint64 {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.generation
	}
	if c.generation != ^uint64(0) {
		c.generation++
	}
	generation := c.generation
	secrets := c.removeEntriesLocked(func(*cacheEntry) bool { return true })
	c.cancelAllFlightsLocked()
	c.mu.Unlock()
	destroySecrets(secrets)
	return generation
}

// RevokeRole makes the tenant role unavailable immediately. Existing leases
// remain valid, but no new borrow may succeed and cached master handles are
// destroyed.
func (c *Cache) RevokeRole(tenantID, roleID string) {
	ref := roleRef{tenantID: tenantID, roleID: roleID}
	c.mu.Lock()
	c.revoked[ref] = struct{}{}
	secrets := c.removeEntriesLocked(func(entry *cacheEntry) bool {
		return entry.identity.TenantID == tenantID && entry.identity.RoleID == roleID
	})
	c.cancelFlightsLocked(func(f *flight) bool {
		return f.identity.TenantID == tenantID && f.identity.RoleID == roleID
	})
	c.mu.Unlock()
	destroySecrets(secrets)
}

// IsRoleRevoked reports whether RevokeRole has marked the tenant role.
func (c *Cache) IsRoleRevoked(tenantID, roleID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.revokedRoleIDs[roleID]; ok {
		return true
	}
	_, ok := c.revoked[roleRef{tenantID: tenantID, roleID: roleID}]
	return ok
}

// RevokeRoleID revokes roleID for every tenant. Existing leases remain valid,
// but cached entries and in-flight refreshes for the role are invalidated.
func (c *Cache) RevokeRoleID(roleID string) {
	c.mu.Lock()
	c.revokedRoleIDs[roleID] = struct{}{}
	secrets := c.removeEntriesLocked(func(entry *cacheEntry) bool {
		return entry.identity.RoleID == roleID
	})
	c.cancelFlightsLocked(func(f *flight) bool {
		return f.identity.RoleID == roleID
	})
	c.mu.Unlock()
	destroySecrets(secrets)
}

// TightenPolicy records digest as the current policy digest for the tenant
// role and immediately invalidates every cached entry and refresh for that
// role. New requests for an older digest fail with ErrPolicyMismatch.
func (c *Cache) TightenPolicy(tenantID, roleID string, digest Digest) {
	ref := roleRef{tenantID: tenantID, roleID: roleID}
	c.mu.Lock()
	if old, ok := c.policies[ref]; ok && old == digest {
		c.mu.Unlock()
		return
	}
	c.policies[ref] = digest
	secrets := c.removeEntriesLocked(func(entry *cacheEntry) bool {
		return entry.identity.TenantID == tenantID && entry.identity.RoleID == roleID
	})
	c.cancelFlightsLocked(func(f *flight) bool {
		return f.identity.TenantID == tenantID && f.identity.RoleID == roleID
	})
	c.mu.Unlock()
	destroySecrets(secrets)
}

// InvalidatePolicy removes all cached entries and in-flight refreshes for the
// tenant role without changing the recorded current policy digest.
func (c *Cache) InvalidatePolicy(tenantID, roleID string) {
	c.mu.Lock()
	secrets := c.removeEntriesLocked(func(entry *cacheEntry) bool {
		return entry.identity.TenantID == tenantID && entry.identity.RoleID == roleID
	})
	c.cancelFlightsLocked(func(f *flight) bool {
		return f.identity.TenantID == tenantID && f.identity.RoleID == roleID
	})
	c.mu.Unlock()
	destroySecrets(secrets)
}

// InvalidateIdentity removes one exact canonical identity.
func (c *Cache) InvalidateIdentity(identity Identity) {
	key := identity.CacheKey()
	c.mu.Lock()
	entry := c.entries[key]
	if entry != nil && entry.identity != identity {
		entry = nil
	}
	if entry != nil {
		delete(c.entries, key)
	}
	if f := c.flights[key]; f != nil && f.identity == identity {
		delete(c.flights, key)
		f.cancel()
	}
	c.mu.Unlock()
	if entry != nil && entry.secret != nil {
		_ = entry.secret.Destroy()
	}
}

// Len returns the number of cached entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Borrow returns a lease for identity, joining an in-flight refresh when one
// already exists. Cancellation by one waiter never cancels the shared refresh
// unless it is the last waiter.
func (c *Cache) Borrow(ctx context.Context, identity Identity) (*Lease, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	key := identity.CacheKey()
	for {
		c.mu.Lock()
		if err := c.checkIdentityLocked(identity); err != nil {
			c.mu.Unlock()
			return nil, err
		}

		now := c.clock()
		if entry := c.entries[key]; entry != nil {
			if entry.identity != identity {
				c.mu.Unlock()
				return nil, ErrCacheKeyCollision
			}
			if now.Before(entry.usableUntil) && now.Before(entry.expiresAt) {
				lease, err := c.cloneLeaseLocked(entry)
				c.mu.Unlock()
				return lease, err
			}
			delete(c.entries, key)
			c.mu.Unlock()
			if entry.secret != nil {
				_ = entry.secret.Destroy()
			}
			continue
		}

		if existingFlight := c.flights[key]; existingFlight != nil {
			if existingFlight.identity != identity {
				c.mu.Unlock()
				return nil, ErrCacheKeyCollision
			}
			existingFlight.waiters++
			c.mu.Unlock()
			return c.await(ctx, existingFlight)
		}

		f := c.newFlightLocked(identity)
		f.waiters = 1
		c.flights[key] = f
		c.mu.Unlock()
		go c.runFlight(f)
		return c.await(ctx, f)
	}
}

// Get is an alias for Borrow.
func (c *Cache) Get(ctx context.Context, identity Identity) (*Lease, error) {
	return c.Borrow(ctx, identity)
}

func (c *Cache) checkIdentityLocked(identity Identity) error {
	if c.closed {
		return ErrCacheClosed
	}
	if identity.ProviderGeneration != c.generation {
		return fmt.Errorf("%w: identity has %d, cache has %d", ErrStaleGeneration, identity.ProviderGeneration, c.generation)
	}
	ref := roleRef{tenantID: identity.TenantID, roleID: identity.RoleID}
	if _, ok := c.revokedRoleIDs[identity.RoleID]; ok {
		return fmt.Errorf("%w: role %q", ErrRoleRevoked, identity.RoleID)
	}
	if _, ok := c.revoked[ref]; ok {
		return fmt.Errorf("%w: tenant %q role %q", ErrRoleRevoked, identity.TenantID, identity.RoleID)
	}
	if policy, ok := c.policies[ref]; ok && policy != identity.PolicyDigest {
		return fmt.Errorf("%w: tenant %q role %q", ErrPolicyMismatch, identity.TenantID, identity.RoleID)
	}
	return nil
}

func (c *Cache) newFlightLocked(identity Identity) *flight {
	ctx, cancel := context.WithCancel(context.Background())
	return &flight{
		key:      identity.CacheKey(),
		identity: identity,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
}

func (c *Cache) cloneLeaseLocked(entry *cacheEntry) (*Lease, error) {
	if entry == nil || entry.secret == nil {
		return nil, ErrCredentialUnavailable
	}
	now := c.clock()
	if !now.Before(entry.usableUntil) || !now.Before(entry.expiresAt) {
		return nil, ErrCredentialExpired
	}
	secret, err := entry.secret.Clone()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCredentialUnavailable, err)
	}
	return &Lease{
		identity:     entry.identity,
		accessKeyID:  entry.accessKeyID,
		sessionToken: entry.sessionToken,
		issuedAt:     entry.issuedAt,
		expiresAt:    entry.expiresAt,
		secret:       secret,
	}, nil
}

func (c *Cache) await(ctx context.Context, f *flight) (*Lease, error) {
	select {
	case <-c.closedCh:
		return nil, ErrCacheClosed

	case <-ctx.Done():
		c.mu.Lock()
		if !f.completed {
			f.waiters--
			if f.waiters <= 0 {
				f.waiters = 0
				f.cancel()
				if c.flights[f.key] == f {
					delete(c.flights, f.key)
				}
			}
		}
		c.mu.Unlock()
		return nil, ctx.Err()

	case <-f.done:
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, ErrCacheClosed
		}
		err := f.err
		entry := f.entry
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		lease, err := c.cloneLeaseLocked(entry)
		c.mu.Unlock()
		return lease, err
	}
}

func (c *Cache) runFlight(f *flight) {
	result, fetchErr := c.provider.Fetch(f.ctx, f.identity)
	if fetchErr != nil {
		if result != nil && result.Secret != nil {
			_ = result.Secret.Destroy()
		}
		c.mu.Lock()
		abandoned := c.flights[f.key] != f
		if abandoned {
			f.err = fmt.Errorf("%w: %w", ErrRefreshAbandoned, fetchErr)
		} else {
			f.err = fetchErr
		}
		c.finishFlightLocked(f)
		c.mu.Unlock()
		return
	}

	now := c.clock()
	credential, validationErr := validateSTSResult(
		f.identity,
		result,
		now,
		c.clockSkew,
		c.safetyMargin,
		c.maxTTL,
	)

	c.mu.Lock()
	if validationErr != nil {
		f.err = validationErr
		c.finishFlightLocked(f)
		c.mu.Unlock()
		return
	}

	if c.flights[f.key] != f {
		f.err = ErrRefreshAbandoned
		c.finishFlightLocked(f)
		c.mu.Unlock()
		_ = credential.secret.Destroy()
		return
	}

	if err := c.checkIdentityLocked(f.identity); err != nil {
		f.err = err
		c.finishFlightLocked(f)
		c.mu.Unlock()
		_ = credential.secret.Destroy()
		return
	}

	var destroy []SecretHandle
	if existing := c.entries[f.key]; existing != nil {
		if existing.identity != f.identity {
			f.err = ErrCacheKeyCollision
			c.finishFlightLocked(f)
			c.mu.Unlock()
			_ = credential.secret.Destroy()
			return
		}
		if now.Before(existing.usableUntil) && now.Before(existing.expiresAt) {
			f.entry = existing
			c.finishFlightLocked(f)
			c.mu.Unlock()
			_ = credential.secret.Destroy()
			return
		}
		delete(c.entries, f.key)
		if existing.secret != nil {
			destroy = append(destroy, existing.secret)
		}
	}

	entry := &cacheEntry{
		identity:     f.identity,
		accessKeyID:  credential.accessKeyID,
		sessionToken: credential.sessionToken,
		issuedAt:     credential.issuedAt,
		expiresAt:    credential.expiresAt,
		usableUntil:  credential.usableUntil,
		secret:       credential.secret,
	}
	c.entries[f.key] = entry
	f.entry = entry
	c.finishFlightLocked(f)
	c.mu.Unlock()
	destroySecrets(destroy)
}

func (c *Cache) finishFlightLocked(f *flight) {
	f.completed = true
	if c.flights[f.key] == f {
		delete(c.flights, f.key)
	}
	f.cancel()
	close(f.done)
}

func (c *Cache) removeEntriesLocked(predicate func(*cacheEntry) bool) []SecretHandle {
	var secrets []SecretHandle
	for key, entry := range c.entries {
		if predicate(entry) {
			delete(c.entries, key)
			if entry.secret != nil {
				secrets = append(secrets, entry.secret)
			}
		}
	}
	return secrets
}

func (c *Cache) cancelFlightsLocked(predicate func(*flight) bool) {
	for key, f := range c.flights {
		if predicate(f) {
			delete(c.flights, key)
			f.cancel()
		}
	}
}

func (c *Cache) cancelAllFlightsLocked() {
	c.cancelFlightsLocked(func(*flight) bool { return true })
}

// Close invalidates every entry, cancels all refreshes, and rejects future
// calls. Existing leases continue to own their independent secret clones.
func (c *Cache) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.closedCh)
	secrets := c.removeEntriesLocked(func(*cacheEntry) bool { return true })
	c.cancelAllFlightsLocked()
	c.mu.Unlock()
	destroySecrets(secrets)
	return nil
}

func destroySecrets(secrets []SecretHandle) {
	for _, secret := range secrets {
		if secret != nil {
			_ = secret.Destroy()
		}
	}
}
