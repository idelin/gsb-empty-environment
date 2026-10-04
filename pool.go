// Package pool implements a connection pool with generation-based
// config rotation. Idle connections are reused; after a rotation the
// old generation is drained: its idle connections are closed at once,
// while borrowed ones finish their in-flight requests and are closed
// on return instead of re-entering the new pool.
package pool

import (
	"context"
	"errors"
	"sync"
)

// ErrPoolClosed is returned by Get after the pool has been closed.
var ErrPoolClosed = errors.New("pool: closed")

// RawConn is the underlying connection managed by the pool.
type RawConn interface {
	// Healthy reports whether the connection is still usable.
	Healthy() bool
	// Close releases the connection. It is called exactly once.
	Close() error
}

// Factory creates a new raw connection.
type Factory func(ctx context.Context) (RawConn, error)

// Config configures a Pool.
type Config struct {
	// MaxOpen caps the number of open connections per generation.
	// Values <= 0 default to 10.
	MaxOpen int
}

// generation tracks the connections created under one config epoch.
type generation struct {
	id   uint64
	idle []*Conn
	open int
}

// Pool is a concurrency-safe connection pool.
type Pool struct {
	mu      sync.Mutex
	factory Factory
	maxOpen int
	nextID  uint64
	gen     *generation
	wake    chan struct{}
	closed  bool
}

// New creates a pool using factory and cfg.
func New(factory Factory, cfg Config) *Pool {
	maxOpen := cfg.MaxOpen
	if maxOpen <= 0 {
		maxOpen = 10
	}
	p := &Pool{factory: factory, maxOpen: maxOpen, wake: make(chan struct{})}
	p.gen = &generation{id: p.nextID}
	p.nextID++
	return p
}

// Get borrows a connection, reusing an idle one when possible. It
// blocks while the current generation is at MaxOpen until a
// connection is returned or ctx expires.
func (p *Pool) Get(ctx context.Context) (*Conn, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, ErrPoolClosed
		}
		if n := len(p.gen.idle); n > 0 {
			c := p.gen.idle[n-1]
			p.gen.idle = p.gen.idle[:n-1]
			c.returned = false
			p.mu.Unlock()
			if c.raw.Healthy() {
				return c, nil
			}
			// Unhealthy idle connection: invalidate and retry.
			p.discard(c)
			continue
		}
		if p.gen.open < p.maxOpen {
			gen := p.gen
			gen.open++
			factory := p.factory
			p.mu.Unlock()
			raw, err := factory(ctx)
			if err != nil {
				p.mu.Lock()
				gen.open--
				p.broadcastLocked()
				p.mu.Unlock()
				return nil, err
			}
			return &Conn{raw: raw, pool: p, gen: gen}, nil
		}
		wake := p.wake
		p.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Rotate swaps in a new config generation. Idle connections of the
// old generation are closed immediately; borrowed ones keep serving
// their requests and are closed when returned, never re-entering the
// new pool. If factory is non-nil it replaces the pool's factory.
func (p *Pool) Rotate(factory Factory) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	old := p.gen
	p.gen = &generation{id: p.nextID}
	p.nextID++
	if factory != nil {
		p.factory = factory
	}
	idle := old.idle
	old.idle = nil
	old.open -= len(idle)
	p.mu.Unlock()
	for _, c := range idle {
		c.raw.Close()
	}
}

// Close shuts the pool down: idle connections are closed now,
// borrowed ones are closed when returned. Safe to call more than once.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	idle := p.gen.idle
	p.gen.idle = nil
	p.gen.open -= len(idle)
	p.broadcastLocked()
	p.mu.Unlock()
	var firstErr error
	for _, c := range idle {
		if err := c.raw.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// discard closes a connection popped from the idle list.
func (p *Pool) discard(c *Conn) {
	p.mu.Lock()
	c.gen.open--
	p.broadcastLocked()
	p.mu.Unlock()
	c.raw.Close()
}

func (p *Pool) broadcastLocked() {
	close(p.wake)
	p.wake = make(chan struct{})
}

// Conn is a borrowed connection. Close returns it to the pool and is
// idempotent: the underlying raw connection is closed at most once.
type Conn struct {
	raw      RawConn
	pool     *Pool
	gen      *generation
	returned bool
}

// Raw exposes the underlying connection for issuing requests.
func (c *Conn) Raw() RawConn { return c.raw }

// Close returns the connection to the pool. Unhealthy connections,
// connections from a superseded generation, and connections returned
// after pool shutdown are closed instead of being pooled.
func (c *Conn) Close() error {
	p := c.pool
	healthy := c.raw.Healthy()
	p.mu.Lock()
	if c.returned {
		p.mu.Unlock()
		return nil
	}
	c.returned = true
	if !p.closed && healthy && c.gen == p.gen {
		c.gen.idle = append(c.gen.idle, c)
		p.broadcastLocked()
		p.mu.Unlock()
		return nil
	}
	c.gen.open--
	p.broadcastLocked()
	p.mu.Unlock()
	return c.raw.Close()
}
