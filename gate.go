package netsec

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	// ErrInvalidRequest means the request did not identify an operation and an
	// idempotency key.
	ErrInvalidRequest = errors.New("netsec: invalid compare request")

	// ErrBudgetExhausted means no comparison budget is left for the request's
	// scope. The comparator is not called when this error is returned.
	ErrBudgetExhausted = errors.New("netsec: comparison budget exhausted")

	// ErrRequestConflict means a request ID was already used for a different
	// query, field, purpose, or input.
	ErrRequestConflict = errors.New("netsec: request id reused for a different operation")

	// ErrComparatorPanic means the trusted comparator panicked. The panic is
	// converted into an error so waiters receive the same result and so the
	// reservation cannot be retried through the same request ID.
	ErrComparatorPanic = errors.New("netsec: comparator panicked")

	// ErrReentrantRequest means a comparator recursively called the gate with
	// the request ID that is currently executing. Returning an error avoids
	// deadlocking on the gate's own in-flight result.
	ErrReentrantRequest = errors.New("netsec: reentrant comparison with the same request id")
)

// Compatibility aliases for callers that use the more common names.
var (
	ErrBudgetExceeded = ErrBudgetExhausted
	ErrIDConflict     = ErrRequestConflict
)

// Scope identifies the authorization scope of one comparison. All three
// fields are part of both the idempotency check and the budget key.
type Scope struct {
	QueryID string
	Field   string
	Purpose string
}

// String formats the scope for diagnostics.
func (s Scope) String() string {
	return fmt.Sprintf("query=%q field=%q purpose=%q", s.QueryID, s.Field, s.Purpose)
}

// Request is one comparison request. RequestID is the idempotency key; the
// remaining fields identify the operation and are passed to the trusted
// comparator. Input is opaque to the gate and is included in the operation
// identity, so replaying a request ID with different input is rejected.
//
// Input must not be mutated while the request is in use. Values that cannot be
// compared with reflect.DeepEqual (for example, functions) should not be used
// as Input.
type Request struct {
	RequestID string
	QueryID   string
	Field     string
	Purpose   string
	Input     any
}

// Scope returns the authorization scope of the request.
func (r Request) Scope() Scope {
	return Scope{
		QueryID: r.QueryID,
		Field:   r.Field,
		Purpose: r.Purpose,
	}
}

func (r Request) validate() error {
	switch {
	case r.RequestID == "":
		return fmt.Errorf("%w: request id is required", ErrInvalidRequest)
	case r.QueryID == "":
		return fmt.Errorf("%w: query id is required", ErrInvalidRequest)
	case r.Field == "":
		return fmt.Errorf("%w: field is required", ErrInvalidRequest)
	case r.Purpose == "":
		return fmt.Errorf("%w: purpose is required", ErrInvalidRequest)
	default:
		return nil
	}
}

// Comparator is the trusted comparison implementation supplied by the query
// host. It is never supplied by a plugin. The context carries an internal
// marker used to detect reentrant calls with the same request ID, so a
// reentrant comparator should forward the context it receives. With that
// context, reentering with the same request ID returns ErrReentrantRequest
// instead of deadlocking. Reentering with a different request ID is fully
// supported.
type Comparator[T any] func(ctx context.Context, req Request) (T, error)

// CompareFunc is an alias for Comparator.
type CompareFunc[T any] = Comparator[T]

// Comparer is the narrow gate surface a host can expose to a plugin without
// exposing budget counters, cached entries, or the trusted comparator.
type Comparer[T any] interface {
	Compare(ctx context.Context, req Request) (T, error)
}

// CompareRequest is an alias for Request.
type CompareRequest = Request

type operation struct {
	scope Scope
	input any
}

func (o operation) equal(other operation) bool {
	return o.scope == other.scope && reflect.DeepEqual(o.input, other.input)
}

type entry[T any] struct {
	op    operation
	done  chan struct{}
	value T
	err   error
}

type reentrantKey struct{}

// Gate is a concurrency-safe comparison gate. It atomically reserves budget
// before invoking the comparator and binds the comparator's value or error to
// the request ID.
//
// A Gate must be created with NewGate or NewScopedGate.
type Gate[T any] struct {
	mu         sync.Mutex
	comparator Comparator[T]

	// global is true for a single budget shared by every scope. Otherwise
	// budgets and used are indexed by Scope.
	global  bool
	limit   int
	budgets map[Scope]int

	used      map[Scope]int
	totalUsed int
	entries   map[string]*entry[T]
}

// ComparisonGate is an alias for Gate.
type ComparisonGate[T any] = Gate[T]

// NewGate creates a gate with one budget shared by all scopes. A zero budget
// rejects every comparison. The function panics if comparator is nil or budget
// is negative.
func NewGate[T any](comparator Comparator[T], budget int) *Gate[T] {
	if comparator == nil {
		panic("netsec: nil comparator")
	}
	if budget < 0 {
		panic("netsec: negative budget")
	}
	return &Gate[T]{
		comparator: comparator,
		global:     true,
		limit:      budget,
		used:       make(map[Scope]int),
		entries:    make(map[string]*entry[T]),
	}
}

// New creates a gate with one budget shared by all scopes.
func New[T any](comparator Comparator[T], budget int) *Gate[T] {
	return NewGate(comparator, budget)
}

// NewComparisonGate creates a gate with one budget shared by all scopes.
func NewComparisonGate[T any](comparator Comparator[T], budget int) *Gate[T] {
	return NewGate(comparator, budget)
}

// NewScopedGate creates a gate with an independent budget for each scope.
// Scopes absent from budgets have a budget of zero. The function panics if
// comparator is nil, any budget is negative, or the whole map is nil.
func NewScopedGate[T any](comparator Comparator[T], budgets map[Scope]int) *Gate[T] {
	if comparator == nil {
		panic("netsec: nil comparator")
	}
	if budgets == nil {
		panic("netsec: nil scope budget map")
	}

	copied := make(map[Scope]int, len(budgets))
	for scope, budget := range budgets {
		if budget < 0 {
			panic("netsec: negative scope budget")
		}
		copied[scope] = budget
	}

	return &Gate[T]{
		comparator: comparator,
		budgets:    copied,
		used:       make(map[Scope]int),
		entries:    make(map[string]*entry[T]),
	}
}

// Compare reserves one unit of budget and, if this request ID is new,
// invokes the trusted comparator. Concurrent calls with the same request ID
// wait for the first call and receive the cached value or error. Reusing the
// ID with a different operation returns ErrRequestConflict. A failed
// comparison still consumes its reservation; no refund is performed.
func (g *Gate[T]) Compare(ctx context.Context, req Request) (T, error) {
	var zero T

	if ctx == nil {
		ctx = context.Background()
	}
	if err := req.validate(); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	reentrantID, _ := ctx.Value(reentrantKey{}).(string)
	op := operation{scope: req.Scope(), input: req.Input}

	g.mu.Lock()
	if existing, ok := g.entries[req.RequestID]; ok {
		if !existing.op.equal(op) {
			g.mu.Unlock()
			return zero, fmt.Errorf("%w: %q", ErrRequestConflict, req.RequestID)
		}
		if reentrantID == req.RequestID {
			g.mu.Unlock()
			return zero, fmt.Errorf("%w: %q", ErrReentrantRequest, req.RequestID)
		}
		done := existing.done
		g.mu.Unlock()

		select {
		case <-done:
			return existing.value, existing.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}

	if err := g.reserveLocked(op.scope); err != nil {
		g.mu.Unlock()
		return zero, err
	}

	existing := &entry[T]{
		op:   op,
		done: make(chan struct{}),
	}
	g.entries[req.RequestID] = existing
	g.mu.Unlock()

	g.execute(ctx, req, existing)
	return existing.value, existing.err
}

func (g *Gate[T]) reserveLocked(scope Scope) error {
	if g.global {
		if g.totalUsed >= g.limit {
			return fmt.Errorf("%w: scope %s", ErrBudgetExhausted, scope)
		}
	} else if g.used[scope] >= g.budgets[scope] {
		return fmt.Errorf("%w: scope %s", ErrBudgetExhausted, scope)
	}

	g.used[scope]++
	g.totalUsed++
	return nil
}

func (g *Gate[T]) execute(parent context.Context, req Request, e *entry[T]) {
	ctx := context.WithValue(parent, reentrantKey{}, req.RequestID)
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				e.err = fmt.Errorf("%w: %w", ErrComparatorPanic, recoveredErr)
			} else {
				e.err = fmt.Errorf("%w: %v", ErrComparatorPanic, recovered)
			}
		}
		close(e.done)
	}()

	e.value, e.err = g.comparator(ctx, req)
}

// Remaining reports the unused budget. With no argument it reports the total
// remaining budget. With a scope argument it reports the remaining budget for
// that scope; for a gate created by NewGate this is the shared total because
// all scopes draw from the same budget.
func (g *Gate[T]) Remaining(scopes ...Scope) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.global {
		remaining := g.limit - g.totalUsed
		if remaining < 0 {
			return 0
		}
		return remaining
	}

	if len(scopes) == 0 {
		total := 0
		for scope, budget := range g.budgets {
			remaining := budget - g.used[scope]
			if remaining > 0 {
				total += remaining
			}
		}
		return total
	}

	scope := scopes[0]
	remaining := g.budgets[scope] - g.used[scope]
	if remaining < 0 {
		return 0
	}
	return remaining
}

// RemainingTotal reports the total unused budget across the gate.
func (g *Gate[T]) RemainingTotal() int {
	return g.Remaining()
}

// Used reports the number of reservations. With no argument it reports the
// total; with a scope argument it reports the reservations for that scope.
func (g *Gate[T]) Used(scopes ...Scope) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(scopes) == 0 {
		return g.totalUsed
	}
	return g.used[scopes[0]]
}

// UsedTotal reports the total number of reservations.
func (g *Gate[T]) UsedTotal() int {
	return g.Used()
}

// Limit reports the configured budget. With no argument it reports the shared
// limit for a global gate and the sum of scope limits for a scoped gate. With
// a scope argument it reports that scope's limit; for a global gate every
// scope reports the shared limit.
func (g *Gate[T]) Limit(scopes ...Scope) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(scopes) == 0 {
		if g.global {
			return g.limit
		}
		total := 0
		for _, budget := range g.budgets {
			total += budget
		}
		return total
	}
	if g.global {
		return g.limit
	}
	return g.budgets[scopes[0]]
}
