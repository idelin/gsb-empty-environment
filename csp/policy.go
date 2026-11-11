package csp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// Sentinel errors surfaced by the policy lifecycle.
var (
	ErrPolicyFrozen   = errors.New("csp: policy is frozen after headers were emitted")
	ErrPolicyReleased = errors.New("csp: policy was released back to the builder pool")
	ErrUntrusted      = errors.New("csp: parent node is not authorized to derive scripts")
	ErrUnauthorized   = errors.New("csp: inline script lacks a trusted nonce or approved hash")
)

// policyCtxKey carries the request-scoped Policy through the render context.
type policyCtxKey struct{}

// Builder constructs one isolated Policy per request. It is safe for
// concurrent use by many handlers and recycles released policies through a
// pool so per-request allocations stay small.
type Builder struct {
	mu       sync.RWMutex
	policies []string
	pool     sync.Pool

	created  atomic.Int64
	released atomic.Int64
	alive    atomic.Int64
}

// NewBuilder creates a builder seeded with static CSP directives. Each
// request receives a fresh copy of these directives plus its own nonce, so
// state never leaks between requests.
func NewBuilder(directives ...string) *Builder {
	b := &Builder{policies: append([]string(nil), directives...)}
	b.pool.New = func() any { return new(Policy) }
	return b
}

// AddDirective updates the default directive set used for future requests.
// In-flight policies keep the snapshot they were created with.
func (b *Builder) AddDirective(directive string) {
	b.mu.Lock()
	b.policies = append(b.policies, directive)
	b.mu.Unlock()
}

// NewPolicy builds a request-scoped policy with a freshly generated nonce.
func (b *Builder) NewPolicy() (*Policy, error) {
	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}

	p := b.pool.Get().(*Policy)
	b.mu.RLock()
	directives := append([]string(nil), b.policies...)
	b.mu.RUnlock()

	p.builder = b
	p.nonce = nonce
	p.directives = directives
	p.frozen = false
	p.released = false
	p.derived = make(map[string]bool)
	p.trusted = make(map[string]bool)
	p.nextNode = 0
	p.root = newRoot(p)
	p.trusted[p.root.id] = true

	b.created.Add(1)
	b.alive.Add(1)
	return p, nil
}

// WithPolicy returns a derived context carrying the policy.
func WithPolicy(ctx context.Context, p *Policy) context.Context {
	return context.WithValue(ctx, policyCtxKey{}, p)
}

// FromContext retrieves the policy installed by WithPolicy, or nil.
func FromContext(ctx context.Context) *Policy {
	p, _ := ctx.Value(policyCtxKey{}).(*Policy)
	return p
}

// Stats describes builder-level recycling counters, useful in tests.
type Stats struct {
	Created  int64
	Released int64
	Alive    int64
}

// Stats returns a point-in-time snapshot of the recycling counters.
func (b *Builder) Stats() Stats {
	return Stats{
		Created:  b.created.Load(),
		Released: b.released.Load(),
		Alive:    b.alive.Load(),
	}
}

// Policy is the per-request CSP state. A policy must never be shared between
// requests: once frozen or released it cannot be reused.
type Policy struct {
	builder *Builder

	nonce      string
	directives []string

	mu       sync.Mutex
	frozen   bool
	released bool

	// derived holds nonce/hash tokens approved for emission in this request.
	derived map[string]bool
	// trusted marks node ids allowed to act as a script's parent.
	trusted  map[string]bool
	nextNode int
	root     *rootNode
}

// Nonce returns the per-request nonce used in the CSP header.
func (p *Policy) Nonce() string { return p.nonce }

// Root returns the trusted entry node of the request's node tree.
func (p *Policy) Root() TrustedNode { return p.root }

// AddDirective appends a directive before the header is emitted.
func (p *Policy) AddDirective(directive string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return ErrPolicyReleased
	}
	if p.frozen {
		return ErrPolicyFrozen
	}
	p.directives = append(p.directives, directive)
	return nil
}

// HeaderValue renders the policy as a Content-Security-Policy header value.
// Every request emits its own nonce token.
func (p *Policy) HeaderValue() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	parts := append([]string(nil), p.directives...)
	parts = append(parts,
		fmt.Sprintf("script-src 'nonce-%s' 'strict-dynamic'", p.nonce),
	)
	return strings.Join(parts, "; ")
}

// Freeze locks the policy once response headers have been emitted. Frozen
// policies reject directive changes but still allow trusted script derivation,
// which mirrors strict-dynamic propagation during streaming renders.
func (p *Policy) Freeze() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return ErrPolicyReleased
	}
	p.frozen = true
	return nil
}

// Release returns the policy to the builder pool. It is called when the
// request (including a cancelled render) finishes, guaranteeing every
// request's state is reclaimed.
func (p *Policy) Release() error {
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return ErrPolicyReleased
	}
	p.released = true
	p.mu.Unlock()

	b := p.builder
	p.builder = nil
	p.nonce = ""
	p.directives = nil
	p.frozen = false
	p.derived = nil
	p.trusted = nil
	p.nextNode = 0
	p.root = nil
	if b != nil {
		b.pool.Put(p)
		b.released.Add(1)
		b.alive.Add(-1)
	}
	return nil
}

// registerNode assigns an id and records whether the node inherits trust.
func (p *Policy) registerNode(parent TrustedNode) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return "", ErrPolicyReleased
	}
	if parent == nil || !p.trusted[parent.NodeID()] {
		return "", ErrUntrusted
	}
	id := fmt.Sprintf("n%d", p.nextNode)
	p.nextNode++
	p.trusted[id] = true
	return id, nil
}

// approve records a nonce token or content hash as eligible for this request.
func (p *Policy) approve(token string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return ErrPolicyReleased
	}
	p.derived[token] = true
	return nil
}

// isApproved reports whether a token may appear in the response body.
func (p *Policy) isApproved(token string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.derived[token]
}

// isNodeTrusted reports whether nodeID may parent scripts.
func (p *Policy) isNodeTrusted(nodeID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.trusted[nodeID]
}

// status reports the frozen/released flags, primarily for lifecycle tests.
func (p *Policy) status() (frozen, released bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frozen, p.released
}

// newNonce generates a 128-bit URL-safe random nonce.
func newNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashScript returns the base64 sha256 hash token used in 'sha256-...'.
func hashScript(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}
