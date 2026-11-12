package netsec

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Kind distinguishes the subquery classes that share a root budget.
type Kind int

const (
	// KindNS is a nameserver address lookup (glue / glue-less referral).
	KindNS Kind = iota
	// KindAlias is an alias chase (CNAME / DNAME target).
	KindAlias
	// KindName is a plain recursive name resolution.
	KindName
)

func (k Kind) String() string {
	switch k {
	case KindNS:
		return "ns"
	case KindAlias:
		return "alias"
	case KindName:
		return "name"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// Key identifies a subquery. Equal Keys are merged onto one in-flight query.
type Key struct {
	Kind Kind
	Name string
}

func (k Key) String() string { return k.Kind.String() + ":" + k.Name }

// CycleError is returned when a subquery would wait on a result that its
// own ancestor chain is still producing. Witness holds the chain of keys
// forming the loop, starting and ending at the repeated key.
type CycleError struct {
	Witness []Key
}

func (e *CycleError) Error() string {
	parts := make([]string, len(e.Witness))
	for i, k := range e.Witness {
		parts[i] = k.String()
	}
	return "netsec: resolution cycle: " + strings.Join(parts, " -> ")
}

// Resolver is implemented by the recursive host (or a test graph). Resolve
// may issue further subqueries through the supplied SubResolver.
type Resolver interface {
	Resolve(ctx context.Context, sub SubResolver, key Key) (string, error)
}

// SubResolver lets a Resolver issue nested subqueries that participate in
// the same coalescing, cancellation, cycle detection and budget controls.
type SubResolver interface {
	Resolve(ctx context.Context, key Key) (string, error)
}

type ctxKey int

const (
	ctxChainKey ctxKey = iota + 1
	ctxBudgetKey
)

// WithBudget attaches a request budget to ctx. A root Engine.Resolve called
// with such a context charges the whole recursion against the given budget
// tree instead of the engine's default budget.
func WithBudget(ctx context.Context, b *Budget) context.Context {
	return context.WithValue(ctx, ctxBudgetKey, b)
}

func chainFrom(ctx context.Context) []Key {
	chain, _ := ctx.Value(ctxChainKey).([]Key)
	return chain
}

func budgetFrom(ctx context.Context, fallback *Budget) *Budget {
	if b, ok := ctx.Value(ctxBudgetKey).(*Budget); ok && b != nil {
		return b
	}
	return fallback
}

type result struct {
	value string
	err   error
}

type waiter struct {
	ctx context.Context
	ch  chan result
}

type flight struct {
	key     Key
	done    chan struct{}
	cancel  context.CancelFunc
	waiters map[*waiter]struct{}
	res     result
}

// Engine coalesces equal subqueries. Repeated subqueries for an in-flight
// Key share a single underlying resolution, while every waiter keeps an
// independent context: cancelling one waiter never cancels the others, and
// the shared query is only torn down once every waiter has gone away.
type Engine struct {
	resolver Resolver
	budget   *Budget

	mu     sync.Mutex
	active map[Key]*flight
}

// NewEngine builds an Engine around r. defaultLimit bounds the shared root
// budget for contexts that do not carry one via WithBudget; a negative
// value means unlimited while zero rejects every subquery.
func NewEngine(r Resolver, defaultLimit int) *Engine {
	return &Engine{
		resolver: r,
		budget:   NewRootBudget(defaultLimit),
		active:   make(map[Key]*flight),
	}
}

// Resolve runs key under ctx, merging onto an identical in-flight query when
// one exists.
func (e *Engine) Resolve(ctx context.Context, key Key) (string, error) {
	chain := chainFrom(ctx)
	for _, seen := range chain {
		if seen == key {
			return "", &CycleError{Witness: append(append([]Key{}, chain[indexOf(chain, key):]...), key)}
		}
	}

	w := &waiter{ctx: ctx, ch: make(chan result, 1)}

	e.mu.Lock()
	if f, ok := e.active[key]; ok {
		f.waiters[w] = struct{}{}
		e.mu.Unlock()
		return e.await(f, w)
	}

	driverCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	if b := budgetFrom(ctx, e.budget); b != nil {
		driverCtx = WithBudget(driverCtx, b.Child())
	}
	childChain := append(append([]Key{}, chain...), key)
	driverCtx = context.WithValue(driverCtx, ctxChainKey, childChain)

	f := &flight{
		key:     key,
		done:    make(chan struct{}),
		cancel:  cancel,
		waiters: map[*waiter]struct{}{w: {}},
	}
	e.active[key] = f
	e.mu.Unlock()

	go e.drive(f, driverCtx)

	return e.await(f, w)
}

func indexOf(chain []Key, key Key) int {
	for i, k := range chain {
		if k == key {
			return i
		}
	}
	return 0
}

func (e *Engine) await(f *flight, w *waiter) (string, error) {
	select {
	case <-w.ctx.Done():
		e.removeWaiter(f, w)
		return "", w.ctx.Err()
	case <-f.done:
		return f.res.value, f.res.err
	case res := <-w.ch:
		return res.value, res.err
	}
}

// removeWaiter detaches a cancelled waiter. When the last waiter leaves
// before the shared query finishes, the driver is cancelled so the host
// does not keep doing work nobody is waiting for.
func (e *Engine) removeWaiter(f *flight, w *waiter) {
	e.mu.Lock()
	delete(f.waiters, w)
	last := len(f.waiters) == 0
	e.mu.Unlock()
	if last {
		f.cancel()
	}
}

func (e *Engine) drive(f *flight, ctx context.Context) {
	defer close(f.done)

	var res result
	if b, _ := ctx.Value(ctxBudgetKey).(*Budget); b != nil {
		if err := b.Acquire(); err != nil {
			res = result{err: err}
		}
	}

	if res.err == nil {
		res.value, res.err = e.resolver.Resolve(ctx, subResolver{e: e, ctx: ctx}, f.key)
	}

	e.mu.Lock()
	delete(e.active, f.key)
	f.res = res
	waiters := make([]*waiter, 0, len(f.waiters))
	for w := range f.waiters {
		waiters = append(waiters, w)
	}
	f.waiters = map[*waiter]struct{}{}
	f.cancel()
	e.mu.Unlock()

	for _, w := range waiters {
		select {
		case w.ch <- res:
		default:
		}
	}
}

type subResolver struct {
	e   *Engine
	ctx context.Context
}

func (s subResolver) Resolve(_ context.Context, key Key) (string, error) {
	return s.e.Resolve(s.ctx, key)
}

// Flights reports how many shared queries are currently in flight; it is
// mainly useful in tests.
func (e *Engine) Flights() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.active)
}

var _ SubResolver = subResolver{}
