// Package compute provides a dependency graph of shared computations with
// reference-counted lifetimes.
//
// A query (Subscription) acquires a whole subgraph: every node it reaches
// holds one reference per incoming edge, so shared sub-computations (e.g.
// the bottom of a diamond) are computed once and shared. Closing a
// subscription only releases its own references; a sub-computation is
// canceled and its cached result dropped only when its reference count
// reaches zero. A failed generation propagates its error to every
// still-subscribed waiter and is not cached, so a new subscription starts
// a fresh generation.
package compute

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrDuplicateNode = errors.New("compute: node already defined")
	ErrUnknownNode   = errors.New("compute: unknown node")
	ErrNilFunc       = errors.New("compute: nil compute function")
	ErrCycle         = errors.New("compute: dependency cycle detected")
	ErrClosed        = errors.New("compute: subscription closed")
)

// Func computes a node's value. deps holds the results of the node's
// declared dependencies, in declaration order. The ctx is canceled when
// the node's reference count drops to zero before the value is needed.
type Func func(ctx context.Context, deps ...any) (any, error)

// Graph is a registry of named computations and their dependency edges.
// It is safe for concurrent use.
type Graph struct {
	mu    sync.Mutex
	nodes map[string]*node
}

type node struct {
	key     string
	fn      Func
	depKeys []string

	refs int
	gen  *generation
	// lastErr remembers the most recent generation failure so that a
	// subscriber that calls Results after the failure was cleared still
	// observes it. Cleared when a new generation starts.
	lastErr error
}

// generation is one run of a node's computation. A failed generation is
// never cached; a successful one is cached while refs > 0.
type generation struct {
	done   chan struct{}
	cancel context.CancelFunc
	val    any
	err    error
}

// depRef binds a dependency node to the exact generation a run observes,
// so a retried dependency never swaps results mid-run.
type depRef struct {
	n   *node
	gen *generation
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{nodes: make(map[string]*node)}
}

// Add defines a node. Dependencies may reference nodes defined later
// (forward references), but the resulting graph must stay acyclic:
// Add returns ErrCycle and rolls back if the new edges close a loop.
func (g *Graph) Add(key string, deps []string, fn Func) error {
	if fn == nil {
		return ErrNilFunc
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[key]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateNode, key)
	}
	n := &node{key: key, fn: fn, depKeys: append([]string(nil), deps...)}
	g.nodes[key] = n
	for _, dep := range deps {
		if g.reachesLocked(dep, key, make(map[string]bool)) {
			delete(g.nodes, key)
			return fmt.Errorf("%w: %s -> %s", ErrCycle, key, dep)
		}
	}
	return nil
}

// reachesLocked reports whether target is reachable from key by following
// dependency edges. Unknown keys are treated as leaf nodes.
func (g *Graph) reachesLocked(key, target string, seen map[string]bool) bool {
	if key == target {
		return true
	}
	if seen[key] {
		return false
	}
	seen[key] = true
	n, ok := g.nodes[key]
	if !ok {
		return false
	}
	for _, dep := range n.depKeys {
		if g.reachesLocked(dep, target, seen) {
			return true
		}
	}
	return false
}

// Refs returns the current reference count of a node. It is intended for
// introspection and tests.
func (g *Graph) Refs(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n, ok := g.nodes[key]; ok {
		return n.refs
	}
	return 0
}

// Subscribe acquires one reference on each key's whole subgraph and
// starts any computations that are not already running or cached.
// Shared sub-computations keep a single running generation.
func (g *Graph) Subscribe(keys ...string) (*Subscription, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, k := range keys {
		if err := g.validateLocked(k, make(map[string]bool)); err != nil {
			return nil, err
		}
	}
	roots := make([]*node, len(keys))
	for i, k := range keys {
		n := g.nodes[k]
		g.acquireLocked(n)
		roots[i] = n
	}
	return &Subscription{g: g, roots: roots, closed: make(chan struct{})}, nil
}

func (g *Graph) validateLocked(key string, seen map[string]bool) error {
	if seen[key] {
		return nil
	}
	seen[key] = true
	n, ok := g.nodes[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, key)
	}
	for _, dep := range n.depKeys {
		if err := g.validateLocked(dep, seen); err != nil {
			return err
		}
	}
	return nil
}

// acquireLocked adds one reference to n and, transitively, to every node
// below it. Diamond bottoms therefore hold one reference per parent edge.
func (g *Graph) acquireLocked(n *node) {
	n.refs++
	for _, dep := range n.depKeys {
		g.acquireLocked(g.nodes[dep])
	}
	g.ensureGenLocked(n)
}

// releaseLocked drops one reference; at zero the running generation is
// canceled and any cached result discarded.
func (g *Graph) releaseLocked(n *node) {
	n.refs--
	if n.refs == 0 && n.gen != nil {
		n.gen.cancel()
		n.gen = nil
	}
	for _, dep := range n.depKeys {
		g.releaseLocked(g.nodes[dep])
	}
}

// ensureGenLocked starts a new generation if none is active. It first
// ensures every dependency has an active generation (retrying failed
// ones), then snapshots them so the run observes a consistent set.
func (g *Graph) ensureGenLocked(n *node) {
	if n.gen != nil {
		return
	}
	deps := make([]depRef, len(n.depKeys))
	for i, dep := range n.depKeys {
		d := g.nodes[dep]
		g.ensureGenLocked(d)
		deps[i] = depRef{n: d, gen: d.gen}
	}
	ctx, cancel := context.WithCancel(context.Background())
	gen := &generation{done: make(chan struct{}), cancel: cancel}
	n.gen = gen
	n.lastErr = nil
	go n.run(g, ctx, gen, deps)
}

func (n *node) run(g *Graph, ctx context.Context, gen *generation, deps []depRef) {
	vals := make([]any, len(deps))
	var err error
	for i, d := range deps {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-d.gen.done:
			if d.gen.err != nil {
				err = fmt.Errorf("compute: dependency %q failed: %w", d.n.key, d.gen.err)
			} else {
				vals[i] = d.gen.val
			}
		}
		if err != nil {
			break
		}
	}
	var val any
	if err == nil {
		val, err = n.fn(ctx, vals...)
	}
	g.mu.Lock()
	gen.val, gen.err = val, err
	close(gen.done)
	if err != nil && n.gen == gen {
		// Failed generations are not cached; the next subscriber or
		// retry starts a new generation. Waiters on this generation
		// still observe the error through gen.done.
		n.gen = nil
		n.lastErr = err
	}
	g.mu.Unlock()
}

// Subscription holds acquired references on one or more query roots.
type Subscription struct {
	g      *Graph
	roots  []*node
	once   sync.Once
	closed chan struct{}
}

// Results blocks until every root's current generation completes and
// returns their values aligned with the Subscribe keys. A failure in any
// shared sub-computation propagates here as an error, including failures
// that completed before the call. Results never starts a new generation;
// only a new Subscribe does.
func (s *Subscription) Results(ctx context.Context) ([]any, error) {
	s.g.mu.Lock()
	select {
	case <-s.closed:
		s.g.mu.Unlock()
		return nil, ErrClosed
	default:
	}
	gens := make([]*generation, len(s.roots))
	errs := make([]error, len(s.roots))
	for i, r := range s.roots {
		gens[i] = r.gen
		if gens[i] == nil {
			// The generation failed and was cleared before this call;
			// deliver the recorded failure to this still-subscribed
			// waiter instead of silently retrying.
			errs[i] = r.lastErr
			if errs[i] == nil {
				errs[i] = errors.New("compute: no active generation")
			}
		}
	}
	s.g.mu.Unlock()

	vals := make([]any, len(gens))
	var firstErr error
	for i := range gens {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		gen := gens[i]
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-gen.done:
		}
		if gen.err != nil && firstErr == nil {
			firstErr = gen.err
		}
		vals[i] = gen.val
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return vals, nil
}

// Close releases exactly the references this subscription acquired.
// Shared sub-computations keep running until their last reference goes
// away. Close is idempotent.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.g.mu.Lock()
		close(s.closed)
		for _, r := range s.roots {
			s.g.releaseLocked(r)
		}
		s.g.mu.Unlock()
	})
}
