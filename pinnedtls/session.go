package pinnedtls

import (
	"crypto/tls"
)

// sessionCache wraps an arbitrary ClientSessionCache so stale sessions can be
// evicted by key. tls.ClientSessionCache.Put(key, nil) is the documented
// removal protocol and is how crypto/tls itself discards unusable entries.
//
// Binding the cache to the verifier is what makes rotation safe for resumed
// sessions: on a resumption crypto/tls skips certificate verification and
// restores the verified chains from the original full handshake, then re-runs
// VerifyConnection on those restored chains. When the restored chains no longer
// match the current pins, the stale entry must be dropped and the connection
// retried with a full handshake; merely failing the handshake would leave the
// same stale ticket in the cache and repeat the failure forever.
type sessionCache struct {
	inner tls.ClientSessionCache
}

func newSessionCache(inner tls.ClientSessionCache) *sessionCache {
	if inner == nil {
		inner = tls.NewLRUClientSessionCache(0)
	}
	return &sessionCache{inner: inner}
}

func (c *sessionCache) Get(sessionKey string) (*tls.ClientSessionState, bool) {
	return c.inner.Get(sessionKey)
}

func (c *sessionCache) Put(sessionKey string, cs *tls.ClientSessionState) {
	c.inner.Put(sessionKey, cs)
}

// Evict removes the cached session for key.
func (c *sessionCache) Evict(sessionKey string) {
	c.inner.Put(sessionKey, nil)
}

// NewBoundSessionCache wraps an existing cache, for example an
// application-provided LRU cache, so it can participate in pin-aware
// eviction. Pass nil to obtain a cache backed by an unbounded LRU.
func NewBoundSessionCache(inner tls.ClientSessionCache) tls.ClientSessionCache {
	return newSessionCache(inner)
}
