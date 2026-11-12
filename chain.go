package netsec

import (
	"errors"
	"net/http"
	"sync"
)

// Stage identifies a fixed position in the proxy middleware chain.
type Stage int

const (
	StageSanitize Stage = iota
	StageAuth
	StageInject
	StageForward
)

// ErrStageLocked is returned when a caller attempts to insert middleware
// into one of the fixed stages. The pipeline order
// sanitize -> auth -> inject -> forward is immutable once a Chain exists;
// custom middleware may only run after injection, via Use or
// WithMiddleware.
var ErrStageLocked = errors.New("netsec: chain order is fixed; custom middleware may only be appended after the inject stage (use Use)")

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain is a reverse-proxy middleware chain with a fixed stage order:
// sanitize, authenticate, inject identity, forward. Caller-supplied
// middleware always runs between inject and forward.
type Chain struct {
	auth Authenticator

	mu   sync.RWMutex
	post []Middleware
}

// Option configures a Chain at creation time.
type Option func(*Chain)

// WithMiddleware appends custom middleware that runs after the inject
// stage and before forwarding. It cannot be placed earlier.
func WithMiddleware(mw ...Middleware) Option {
	return func(c *Chain) {
		c.post = append(c.post, mw...)
	}
}

// NewChain creates a chain with the given authenticator. The stage order
// is fixed and cannot be changed afterwards.
func NewChain(auth Authenticator, opts ...Option) *Chain {
	c := &Chain{auth: auth}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Use appends custom middleware after the inject stage. It is the only
// supported way to extend a chain; the fixed stages are not affected.
func (c *Chain) Use(mw ...Middleware) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.post = append(c.post, mw...)
}

// Insert always fails: the fixed stages (sanitize, auth, inject, forward)
// are locked and their order cannot be adjusted after chain creation.
func (c *Chain) Insert(stage Stage, mw ...Middleware) error {
	return ErrStageLocked
}

// Handler builds the full pipeline around upstream (the forward stage).
// The returned handler executes, in order: sanitize, auth, inject,
// custom middleware (in registration order), upstream.
func (c *Chain) Handler(upstream http.Handler) http.Handler {
	c.mu.RLock()
	post := make([]Middleware, len(c.post))
	copy(post, c.post)
	c.mu.RUnlock()

	h := upstream
	for i := len(post) - 1; i >= 0; i-- {
		h = post[i](h)
	}
	h = injectMiddleware(h)
	h = authMiddleware(c.auth)(h)
	h = sanitizeMiddleware(h)
	return h
}
