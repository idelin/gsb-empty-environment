// Package ticketcache implements a concurrency-safe cache for service
// tickets that a proxy obtains from an issuer on behalf of its users.
//
// Tickets are partitioned by tenant, principal, target service, scope and
// the delegation chain, so a request for a narrow scope never reuses a
// ticket issued for a wider one. Concurrent misses for the same partition
// share a single refresh (single-flight); each waiter may cancel its own
// context without disturbing the shared refresh or the other waiters.
// Revoking a partition invalidates the cached ticket and intercepts any
// in-flight refresh started before the revocation: its result is discarded
// and its waiters observe ErrRevoked.
package ticketcache

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrRevoked is returned to the waiters of a refresh whose partition was
// revoked while the refresh was still in flight.
var ErrRevoked = errors.New("ticketcache: refresh superseded by revocation")

// Key identifies a cache partition. Every field contributes to the
// partition, so tickets are never shared across tenants, principals,
// services, scopes or delegation chains.
type Key struct {
	Tenant     string
	Principal  string
	Service    string
	Scope      string
	Delegation []string
}

// id renders the key as an unambiguous string for map indexing.
func (k Key) id() string {
	var b strings.Builder
	b.WriteString(strconv.Quote(k.Tenant))
	b.WriteByte('|')
	b.WriteString(strconv.Quote(k.Principal))
	b.WriteByte('|')
	b.WriteString(strconv.Quote(k.Service))
	b.WriteByte('|')
	b.WriteString(strconv.Quote(k.Scope))
	for _, d := range k.Delegation {
		b.WriteByte('|')
		b.WriteString(strconv.Quote(d))
	}
	return b.String()
}

// Ticket is a credential issued for a Key. A zero ExpiresAt means the
// ticket never expires.
type Ticket struct {
	Token     string
	ExpiresAt time.Time
}

// Issuer obtains a fresh ticket for a key.
type Issuer interface {
	Issue(ctx context.Context, key Key) (Ticket, error)
}

// IssuerFunc adapts a function to the Issuer interface.
type IssuerFunc func(ctx context.Context, key Key) (Ticket, error)

// Issue implements Issuer.
func (f IssuerFunc) Issue(ctx context.Context, key Key) (Ticket, error) {
	return f(ctx, key)
}

// Cache caches issued tickets per partition and coalesces concurrent
// refreshes of the same partition into a single issuer call.
type Cache struct {
	issuer Issuer

	// RenewBefore makes Get treat tickets expiring within this window as
	// already expired, so renewal happens ahead of the deadline.
	RenewBefore time.Duration

	now func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	ticket Ticket
	valid  bool
	gen    uint64  // bumped by Revoke to invalidate in-flight refreshes
	flight *flight // nil unless a refresh is in flight
}

type flight struct {
	gen    uint64
	done   chan struct{}
	ticket Ticket
	err    error
}

// New returns a Cache that obtains missing or expired tickets from issuer.
func New(issuer Issuer) *Cache {
	return &Cache{
		issuer:  issuer,
		now:     time.Now,
		entries: make(map[string]*entry),
	}
}

// Get returns a valid ticket for key, fetching one from the issuer if the
// cached ticket is missing or expired. Concurrent callers for the same
// partition share one issuer call; cancelling ctx only detaches the
// calling waiter and never cancels the shared refresh.
func (c *Cache) Get(ctx context.Context, key Key) (Ticket, error) {
	id := key.id()

	c.mu.Lock()
	e := c.entries[id]
	if e == nil {
		e = &entry{}
		c.entries[id] = e
	}
	if e.valid && !c.expired(e.ticket) {
		t := e.ticket
		c.mu.Unlock()
		return t, nil
	}
	if e.flight == nil {
		e.flight = &flight{gen: e.gen, done: make(chan struct{})}
		go c.refresh(id, key, e.flight)
	}
	f := e.flight
	c.mu.Unlock()

	select {
	case <-f.done:
		return f.ticket, f.err
	case <-ctx.Done():
		return Ticket{}, ctx.Err()
	}
}

// Revoke invalidates the cached ticket for key's partition and intercepts
// any refresh started before the revocation: when it returns, its result
// is discarded instead of being cached, and its waiters get ErrRevoked.
// Subsequent Get calls start a fresh refresh.
func (c *Cache) Revoke(key Key) {
	id := key.id()

	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil {
		e.gen++
		e.valid = false
		e.ticket = Ticket{}
		// Detach the in-flight flight so new Gets start a fresh one.
		// Existing waiters still observe its completion as ErrRevoked.
		e.flight = nil
	}
}

func (c *Cache) expired(t Ticket) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !t.ExpiresAt.After(c.now().Add(c.RenewBefore))
}

// refresh performs the shared issuer call for a flight. The issuer context
// is deliberately detached from any waiter's context so that one waiter
// cancelling does not abort the renewal for the others.
func (c *Cache) refresh(id string, key Key, f *flight) {
	t, err := c.issuer.Issue(context.Background(), key)

	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	if e != nil && e.flight == f {
		e.flight = nil
	}
	switch {
	case err != nil:
		f.err = err
	case e != nil && e.gen == f.gen:
		e.ticket = t
		e.valid = true
		f.ticket = t
	default:
		// The partition was revoked (or removed) while the issuer call
		// was in flight: intercept the stale-generation result.
		f.err = ErrRevoked
	}
	close(f.done)
}
