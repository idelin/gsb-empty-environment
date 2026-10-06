package dfacmp

import (
	"context"
	"fmt"
	"sync"
)

// Expander resolves external fragments. Implementations must be
// deterministic: the same fragment id always yields the same FragmentData.
type Expander interface {
	Expand(ctx context.Context, id FragmentID) (FragmentData, error)
}

// ExpanderFunc adapts a function to the Expander interface.
type ExpanderFunc func(ctx context.Context, id FragmentID) (FragmentData, error)

// Expand implements Expander.
func (f ExpanderFunc) Expand(ctx context.Context, id FragmentID) (FragmentData, error) {
	return f(ctx, id)
}

// Engine compares two automata, expanding external fragments on demand.
// Successful expansions are also applied to the engine's live automata, so
// later queries start from a more expanded version. Results of decided
// queries are cached by version pair and re-validated against the full
// product graph before being served.
//
// The engine serializes its own mutations, but callers must not mutate the
// automata directly while queries are in flight.
type Engine struct {
	mu    sync.Mutex
	a     *Automaton
	b     *Automaton
	exp   Expander
	cache map[[2]uint64]Result
}

// NewEngine creates an engine comparing a and b, using exp to expand
// fragments. exp may be nil, in which case blocked queries report Unknown.
func NewEngine(a, b *Automaton, exp Expander) *Engine {
	return &Engine{a: a, b: b, exp: exp, cache: map[[2]uint64]Result{}}
}

// Query binds the current versions of both automata and starts an
// asynchronous comparison. The query works on its own snapshot, so later
// expansions never change what it reports: old queries stay bound to old
// versions.
func (e *Engine) Query() *Query { return e.QueryContext(context.Background()) }

// QueryContext is Query with a context governing fragment expansion.
func (e *Engine) QueryContext(ctx context.Context) *Query {
	e.mu.Lock()
	snapA, snapB := e.a.Clone(), e.b.Clone()
	e.mu.Unlock()
	q := &Query{
		engine:   e,
		a:        snapA,
		b:        snapB,
		versionA: snapA.Version(),
		versionB: snapB.Version(),
		done:     make(chan struct{}),
	}
	go q.run(ctx)
	return q
}

// Compare runs a query to completion.
func (e *Engine) Compare(ctx context.Context) (Result, error) {
	return e.QueryContext(ctx).Wait(ctx)
}

// Query is an in-flight comparison bound to the automaton versions captured
// at creation time.
type Query struct {
	engine             *Engine
	a, b               *Automaton // private snapshot
	versionA, versionB uint64

	done chan struct{}
	res  Result
	err  error
}

// Done closes when the query has finished.
func (q *Query) Done() <-chan struct{} { return q.done }

// Wait blocks until the query finishes or ctx is cancelled. A non-nil error
// together with an Unknown result means fragment expansion failed; the
// result's Blocking lists the fragments still missing.
func (q *Query) Wait(ctx context.Context) (Result, error) {
	select {
	case <-q.done:
		return q.res, q.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (q *Query) run(ctx context.Context) {
	defer close(q.done)
	key := [2]uint64{q.versionA, q.versionB}
	q.engine.mu.Lock()
	cached, ok := q.engine.cache[key]
	q.engine.mu.Unlock()
	if ok {
		// Serve the cache only after a full product-graph recheck.
		if err := FullProductCheck(q.a, q.b, cached); err == nil {
			q.res = cached
			return
		}
	}
	search := NewSearch(q.a, q.b)
	res := search.Run()
	for res.Kind == Unknown && q.engine.exp != nil {
		applied, err := q.expandAll(ctx, res.Blocking)
		if applied == 0 {
			if err != nil {
				q.err = err
			}
			res.Blocking = unexpandedFragments(q.a, q.b)
			break
		}
		res = search.Resume()
	}
	res.VersionA, res.VersionB = q.versionA, q.versionB
	q.res = res
	if res.Kind != Unknown {
		q.engine.mu.Lock()
		q.engine.cache[key] = res
		q.engine.mu.Unlock()
	}
}

// expandAll expands the given fragments concurrently, applying the results
// to the query snapshot and to the engine's live automata. It returns the
// number of fragments actually expanded in the snapshot.
func (q *Query) expandAll(ctx context.Context, ids []FragmentID) (int, error) {
	type outcome struct {
		id   FragmentID
		data FragmentData
		err  error
	}
	ch := make(chan outcome, len(ids))
	for _, id := range ids {
		go func(id FragmentID) {
			data, err := q.engine.exp.Expand(ctx, id)
			ch <- outcome{id, data, err}
		}(id)
	}
	applied := 0
	var firstErr error
	for range ids {
		o := <-ch
		if o.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("dfacmp: expand fragment %q: %w", o.id, o.err)
			}
			continue
		}
		errA := q.a.ExpandFragment(o.id, o.data)
		errB := q.b.ExpandFragment(o.id, o.data)
		if errA == nil || errB == nil {
			applied++
		}
		q.engine.mu.Lock()
		q.engine.a.ExpandFragment(o.id, o.data) //nolint:errcheck // already expanded is fine
		q.engine.b.ExpandFragment(o.id, o.data) //nolint:errcheck
		q.engine.mu.Unlock()
	}
	return applied, firstErr
}
