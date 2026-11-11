package jwsbridge

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrUnknownKey        = errors.New("jwsbridge: unknown key for tenant/kid")
	ErrDowngradeRotation = errors.New("jwsbridge: key rotation must advance the generation")
)

// KeyRecord is the public-key material registered for one key generation.
type KeyRecord struct {
	KeyID      string
	Use        string
	Generation int
	Public     ed25519.PublicKey
}

// cacheKey scopes every cached record by tenant AND rotation generation so
// that a reused kid across tenants or across rotations can never collide.
type cacheKey struct {
	tenant string
	kid    string
	gen    int
}

// KeyStore is a cache-aside key directory. The directory holds the current
// record per (tenant, kid); the cache fronts it keyed by
// (tenant, kid, generation).
type KeyStore struct {
	mu        sync.RWMutex
	directory map[string]map[string]KeyRecord // tenant -> kid -> current record
	cache     map[cacheKey]KeyRecord
}

// NewKeyStore returns an empty key store.
func NewKeyStore() *KeyStore {
	return &KeyStore{
		directory: make(map[string]map[string]KeyRecord),
		cache:     make(map[cacheKey]KeyRecord),
	}
}

// Register installs rec as the current key for (tenant, rec.KeyID). The
// generation must strictly increase on rotation, even when the kid is
// reused, so cached material from older generations is never served again.
func (ks *KeyStore) Register(tenant string, rec KeyRecord) error {
	if rec.KeyID == "" {
		return errors.New("jwsbridge: key record missing kid")
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	byKid, ok := ks.directory[tenant]
	if !ok {
		byKid = make(map[string]KeyRecord)
		ks.directory[tenant] = byKid
	}
	if cur, ok := byKid[rec.KeyID]; ok && rec.Generation <= cur.Generation {
		return fmt.Errorf("%w: tenant %q kid %q gen %d <= %d",
			ErrDowngradeRotation, tenant, rec.KeyID, rec.Generation, cur.Generation)
	}
	byKid[rec.KeyID] = rec
	// Purge stale generations of this (tenant, kid) from the cache.
	for ck := range ks.cache {
		if ck.tenant == tenant && ck.kid == rec.KeyID && ck.gen < rec.Generation {
			delete(ks.cache, ck)
		}
	}
	return nil
}

// Resolve returns the current key record for (tenant, kid), serving it from
// the generation-scoped cache when possible.
func (ks *KeyStore) Resolve(tenant, kid string) (KeyRecord, error) {
	ks.mu.RLock()
	rec, ok := ks.directory[tenant][kid]
	ks.mu.RUnlock()
	if !ok {
		return KeyRecord{}, fmt.Errorf("%w: tenant %q kid %q", ErrUnknownKey, tenant, kid)
	}
	ck := cacheKey{tenant: tenant, kid: kid, gen: rec.Generation}
	ks.mu.RLock()
	cached, hit := ks.cache[ck]
	ks.mu.RUnlock()
	if hit {
		return cached, nil
	}
	ks.mu.Lock()
	ks.cache[ck] = rec
	ks.mu.Unlock()
	return rec, nil
}

// CacheKeys lists the cache entries as "tenant/kid#gen" for inspection and
// tests, proving tenant and generation isolation.
func (ks *KeyStore) CacheKeys() []string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	keys := make([]string, 0, len(ks.cache))
	for ck := range ks.cache {
		keys = append(keys, fmt.Sprintf("%s/%s#%d", ck.tenant, ck.kid, ck.gen))
	}
	return keys
}
