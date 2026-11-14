package netsec

import (
	"crypto/sha256"
	"crypto/x509"
	"math/big"
	"sync"
	"time"
)

const DefaultCacheTTL = 5 * time.Minute

// CacheKey identifies a timestamp token by message imprint and TSA identity.
type CacheKey struct {
	Imprint string
	Issuer  string
}

func (k CacheKey) String() string {
	return k.Imprint + "|" + k.Issuer
}

// NewCacheKey builds the required imprint-plus-issuer cache key. The imprint
// component includes the hash algorithm OID to avoid cross-algorithm collisions.
func NewCacheKey(imprint MessageImprint, issuer IssuerIdentity) CacheKey {
	return CacheKey{
		Imprint: imprint.HashAlgorithm.String() + ":" + hexString(imprint.HashedMessage),
		Issuer:  issuer.String(),
	}
}

// CacheEntry is one validated timestamp binding held by TokenCache.
type CacheEntry struct {
	Key               CacheKey
	TokenDigest       [32]byte
	Imprint           MessageImprint
	Issuer            IssuerIdentity
	SerialNumber      *big.Int
	GenTime           time.Time
	CreatedAt         time.Time
	ExpiresAt         time.Time
	TimestampSigner   *x509.Certificate
	SignerCertificate *x509.Certificate
	Chain             []*x509.Certificate
	Binding           *Binding
}

// TokenCache caches successful timestamp binding results. Entries are keyed by
// message imprint and issuer identity, never by artifact or signature alone.
type TokenCache struct {
	mu      sync.RWMutex
	entries map[CacheKey]CacheEntry
	ttl     time.Duration
}

// NewTokenCache creates a cache. A zero or negative TTL uses DefaultCacheTTL.
func NewTokenCache(ttl time.Duration) *TokenCache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &TokenCache{
		entries: make(map[CacheKey]CacheEntry),
		ttl:     ttl,
	}
}

// TTL returns the cache lifetime applied to new entries.
func (c *TokenCache) TTL() time.Duration {
	if c == nil {
		return 0
	}
	return c.ttl
}

// Get returns a live entry. Expired entries are deleted and reported as misses.
func (c *TokenCache) Get(key CacheKey, now time.Time) (CacheEntry, bool) {
	if c == nil {
		return CacheEntry{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return CacheEntry{}, false
	}
	if !entry.ExpiresAt.IsZero() && !now.Before(entry.ExpiresAt) {
		c.Invalidate(key)
		return CacheEntry{}, false
	}
	return entry, true
}

// Put stores a binding and computes its expiry from the cache TTL and the
// supplied hard deadline, if any.
func (c *TokenCache) Put(entry CacheEntry, now time.Time, hardDeadline time.Time) CacheEntry {
	if c == nil {
		return entry
	}
	entry.Key = NewCacheKey(entry.Imprint, entry.Issuer)
	entry.CreatedAt = now
	entry.ExpiresAt = now.Add(c.ttl)
	if !hardDeadline.IsZero() && hardDeadline.Before(entry.ExpiresAt) {
		entry.ExpiresAt = hardDeadline
	}
	if entry.Binding != nil {
		entry.Binding.CacheHit = false
	}
	c.mu.Lock()
	c.entries[entry.Key] = entry
	c.mu.Unlock()
	return entry
}

// Invalidate deletes one cache entry.
func (c *TokenCache) Invalidate(key CacheKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// InvalidateIssuer deletes all entries signed by a TSA identity. It is useful
// immediately after learning that the TSA certificate was revoked.
func (c *TokenCache) InvalidateIssuer(issuer IssuerIdentity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	for key := range c.entries {
		if key.Issuer == issuer.String() {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
}

// Clear removes all cached entries.
func (c *TokenCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

// Len returns the number of currently stored entries, including entries that
// may have expired but have not been read since expiry.
func (c *TokenCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

type serialKey struct {
	Issuer string
	Serial string
}

type serialRecord struct {
	TokenDigest [32]byte
	FirstSeen   time.Time
}

// SerialRegistry detects RFC 3161 serial-number replay for one TSA. Seeing the
// same serial for the same token is harmless; seeing it on a different token is
// treated as a duplicate.
type SerialRegistry struct {
	mu      sync.Mutex
	records map[serialKey]serialRecord
}

func NewSerialRegistry() *SerialRegistry {
	return &SerialRegistry{records: make(map[serialKey]serialRecord)}
}

func (r *SerialRegistry) CheckAndRecord(issuer IssuerIdentity, serial *big.Int, tokenDigest [32]byte, at time.Time) error {
	if r == nil {
		return nil
	}
	if serial == nil {
		return errorf(ErrDuplicateSerial, "timestamp serial number is absent")
	}
	key := serialKey{Issuer: issuer.String(), Serial: serial.String()}
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.records[key]; ok {
		if previous.TokenDigest != tokenDigest {
			return errorf(ErrDuplicateSerial, "issuer %s reused serial %s", issuer.description(), serial.String())
		}
		return nil
	}
	r.records[key] = serialRecord{TokenDigest: tokenDigest, FirstSeen: at}
	return nil
}

// Contains reports whether a serial has been observed for the issuer.
func (r *SerialRegistry) Contains(issuer IssuerIdentity, serial *big.Int) bool {
	if r == nil || serial == nil {
		return false
	}
	key := serialKey{Issuer: issuer.String(), Serial: serial.String()}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.records[key]
	return ok
}

// RevocationList is a concurrency-safe in-memory revocation checker.
type RevocationList struct {
	mu      sync.RWMutex
	revoked map[[32]byte]time.Time
}

func NewRevocationList() *RevocationList {
	return &RevocationList{revoked: make(map[[32]byte]time.Time)}
}

// Revoke marks a certificate revoked at the supplied time. A zero time means
// the certificate is revoked for every verification time.
func (r *RevocationList) Revoke(cert *x509.Certificate, at time.Time) {
	if r == nil || cert == nil {
		return
	}
	certDigest := sha256.Sum256(cert.Raw)
	r.mu.Lock()
	r.revoked[certDigest] = at
	r.mu.Unlock()
}

func (r *RevocationList) Unrevoke(cert *x509.Certificate) {
	if r == nil || cert == nil {
		return
	}
	certDigest := sha256.Sum256(cert.Raw)
	r.mu.Lock()
	delete(r.revoked, certDigest)
	r.mu.Unlock()
}

func (r *RevocationList) IsRevoked(cert *x509.Certificate, at time.Time) (bool, error) {
	if r == nil || cert == nil {
		return false, nil
	}
	certDigest := sha256.Sum256(cert.Raw)
	r.mu.RLock()
	revokedAt, ok := r.revoked[certDigest]
	r.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if revokedAt.IsZero() || !at.Before(revokedAt) {
		return true, nil
	}
	return false, nil
}
