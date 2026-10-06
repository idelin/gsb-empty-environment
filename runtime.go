package compute

import (
	"context"
	"errors"
	"sync"
)

// Runtime executes graph nodes with sharing and reference counting.
//
// Invariants (all guarded by rt.mu):
//   - a generation is present in rt.gens only while it is running and
//     has refs > 0, so it is always joinable;
//   - a generation that finishes or loses its last reference is removed
//     from rt.gens, so a later Subscribe starts a new generation.
type Runtime struct {
	mu   sync.Mutex
	gens map[*Node]*generation
}

// NewRuntime returns a runtime with no in-flight computations.
func NewRuntime() *Runtime {
	return &Runtime{gens: make(map[*Node]*generation)}
}

// generation is one shared execution of a node.
type generation struct {
	node   *Node
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the generation finishes

	refs     int
	finished bool
	val      interface{}
	err      error
}

// Subscription is one subscriber's reference to a generation.
type Subscription struct {
	rt   *Runtime
	gen  *generation
	once sync.Once
}

// Subscribe joins the node's current generation, creating a new one if
// none is running. The caller must eventually call Unsubscribe.
func (rt *Runtime) Subscribe(n *Node) *Subscription {
	rt.mu.Lock()
	gen, ok := rt.gens[n]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		gen = &generation{node: n, ctx: ctx, cancel: cancel, done: make(chan struct{})}
		rt.gens[n] = gen
		go gen.run(rt)
	}
	gen.refs++
	rt.mu.Unlock()
	return &Subscription{rt: rt, gen: gen}
}

// Wait blocks until the generation finishes and returns its result. It
// may be called any number of times, before or after Unsubscribe.
func (s *Subscription) Wait() (interface{}, error) {
	<-s.gen.done
	return s.gen.val, s.gen.err
}

// Unsubscribe releases this subscriber's reference. When the last
// reference of a still-running generation is released, the generation
// is cancelled and removed from the sharing table. It is idempotent.
func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		rt := s.rt
		rt.mu.Lock()
		gen := s.gen
		gen.refs--
		if gen.refs == 0 && !gen.finished {
			if rt.gens[gen.node] == gen {
				delete(rt.gens, gen.node)
			}
			gen.cancel()
		}
		rt.mu.Unlock()
	})
}

// Refs reports the reference count of the node's current generation, or
// zero if no generation is running. Intended for tests and diagnostics.
func (rt *Runtime) Refs(n *Node) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if gen, ok := rt.gens[n]; ok {
		return gen.refs
	}
	return 0
}

// Running reports the number of generations currently in flight.
func (rt *Runtime) Running() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.gens)
}

// run executes one generation: subscribe to dependencies, wait for
// them, invoke the node function, then publish the outcome.
func (g *generation) run(rt *Runtime) {
	node := g.node
	deps := node.Deps()

	subs := make([]*Subscription, len(deps))
	for i, d := range deps {
		subs[i] = rt.Subscribe(d)
	}

	results := make([]Result, len(deps))
	var depErr error
	for i, s := range subs {
		val, err := s.Wait()
		results[i] = Result{Node: deps[i], Val: val, Err: err}
		if err != nil && depErr == nil {
			depErr = err
		}
	}
	// The generation no longer needs its dependencies once their
	// results are in; release them before publishing.
	for _, s := range subs {
		s.Unsubscribe()
	}

	var val interface{}
	var err error
	switch {
	case depErr != nil:
		// A shared dependency failed: propagate the same failure to
		// every subscriber of this generation.
		err = depErr
	case g.ctx.Err() != nil:
		err = g.ctx.Err()
	default:
		fn := node.Func()
		if fn == nil {
			err = errors.New("compute: node " + node.Name() + " has no function")
		} else {
			val, err = fn(g.ctx, results)
		}
	}
	rt.finish(g, val, err)
}

// finish publishes the outcome and unshares the generation. A stale
// (already cancelled and replaced) generation must not evict the newer
// generation registered for the same node.
func (rt *Runtime) finish(g *generation, val interface{}, err error) {
	rt.mu.Lock()
	g.val, g.err = val, err
	g.finished = true
	close(g.done)
	if rt.gens[g.node] == g {
		delete(rt.gens, g.node)
	}
	rt.mu.Unlock()
}
