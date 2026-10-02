// Package configlease implements a version-pinned configuration lease
// binder: a config center hands out short-lived leases bound to a config
// version, and worker nodes may only serve traffic while holding a valid
// lease.
package configlease

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Config is the dynamic configuration distributed by the center.
type Config struct {
	Version    uint64
	DBPassword string
}

// Lease is a credential binding a node to a specific config version for a
// bounded time. Past ExpiresAt the holder must consider it void.
type Lease struct {
	ID        string
	Version   uint64
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Center is the config center. It issues, renews and revokes leases.
type Center struct {
	mu      sync.Mutex
	clock   Clock
	ttl     time.Duration
	cfg     Config
	active  map[string]Lease
	revoked map[string]bool
	nextID  uint64
}

// NewCenter creates a center holding initial, granting leases of ttl.
func NewCenter(clock Clock, ttl time.Duration, initial Config) *Center {
	return &Center{
		clock:   clock,
		ttl:     ttl,
		cfg:     initial,
		active:  make(map[string]Lease),
		revoked: make(map[string]bool),
	}
}

// issue creates a fresh lease bound to the current config. Caller holds mu.
func (c *Center) issue() Lease {
	c.nextID++
	now := c.clock.Now()
	l := Lease{
		ID:        fmt.Sprintf("lease-%d", c.nextID),
		Version:   c.cfg.Version,
		IssuedAt:  now,
		ExpiresAt: now.Add(c.ttl),
	}
	c.active[l.ID] = l
	return l
}

// Acquire grants a new lease on the current config and returns both.
func (c *Center) Acquire() (Config, Lease) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg, c.issue()
}

// Renew extends the lease identified by id. If the lease is unknown,
// revoked, expired, or bound to a stale config version, the center instead
// issues a fresh lease bound to the latest config — this is how revoked
// nodes receive the new config and new lease credential.
func (c *Center) Renew(id string) (Config, Lease) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.active[id]; ok && !c.revoked[id] &&
		l.Version == c.cfg.Version && c.clock.Now().Before(l.ExpiresAt) {
		l.ExpiresAt = c.clock.Now().Add(c.ttl)
		c.active[id] = l
		return c.cfg, l
	}
	delete(c.active, id)
	return c.cfg, c.issue()
}

// UpdateConfig installs a new config, bumps its version, and revokes every
// outstanding lease so holders must re-bind to the new version.
func (c *Center) UpdateConfig(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg.Version = c.cfg.Version + 1
	c.cfg = cfg
	for id := range c.active {
		c.revoked[id] = true
		delete(c.active, id)
	}
}

// Validate reports whether id is a live lease on the current config version.
func (c *Center) Validate(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.active[id]
	return ok && !c.revoked[id] && l.Version == c.cfg.Version &&
		c.clock.Now().Before(l.ExpiresAt)
}

// Current returns the center's current config.
func (c *Center) Current() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// ErrLeaseExpired is returned by a node that does not hold a valid lease
// and therefore refuses to serve traffic.
var ErrLeaseExpired = errors.New("configlease: no valid config lease, refusing traffic")
