package plancache

import (
	"errors"
	"fmt"
	"sync"
)

// InputResolver produces a fresh CompileInput for a logical request after a
// cache miss. Hosts typically re-run canonicalization and view expansion
// against current live catalog state.
type InputResolver func() (CompileInput, error)

// Handle is a session-local prepared-statement handle. Handles are
// invalidated, never reused, when the pooled connection switches principal.
type Handle struct {
	id        string
	principal PrincipalID
	expired   bool
}

// Pending is a plan waiting to execute. The fence captured at enqueue time
// bounds which later immediate events can invalidate it.
type Pending struct {
	Plan       *Plan
	Tx         *Transaction
	EnqueuedAt int64
}

// Transaction is a begun unit of work with a pinned permission snapshot.
type Transaction struct {
	Principal  PrincipalID
	Snapshot   *Snapshot
	beganFence int64
}

// Executor is the host-facing plan cache manager.
type Executor struct {
	mu       sync.Mutex
	cat      Catalog
	session  *PlanCache
	global   *PlanCache
	observer Observer
	handles  map[string]*Handle
	pending  map[string]*Pending
}

// NewExecutor wires the catalog, caches and observer.
func NewExecutor(cat Catalog, session, global *PlanCache, observer Observer) *Executor {
	return &Executor{
		cat:      cat,
		session:  session,
		global:   global,
		observer: observer,
		handles:  map[string]*Handle{},
		pending:  map[string]*Pending{},
	}
}

// Begin pins a permission snapshot for the principal.
func (e *Executor) Begin(p PrincipalID) (*Transaction, error) {
	snap, err := e.cat.Begin(p)
	if err != nil {
		return nil, err
	}
	return &Transaction{
		Principal:  p,
		Snapshot:   snap,
		beganFence: e.cat.Bus().Fence(),
	}, nil
}

// Prepare looks the request up by full key across session then global cache.
// Every hit is revalidated against the transaction snapshot before it is
// returned; a stale hit is discarded and recompiled exactly once via
// resolve. Immediate invalidation fails hard and never recompiles, because
// the security decision was withdrawn for this transaction.
func (e *Executor) Prepare(tx *Transaction, resolve InputResolver, factory OperatorFactory) (*Plan, *Handle, error) {
	// Immediate invalidation pierces the whole transaction, including cache
	// misses that would otherwise recompile. Check the fence before doing
	// any resolution or compilation work.
	if events := e.cat.Bus().Since(tx.beganFence, tx.Principal, Manifest{}); len(events) > 0 {
		err := fmt.Errorf("%w: event=%s fence=%d",
			ErrImmediateInvalidation, events[0].Type, events[0].Fence)
		e.observer.ImmediateFailure("", events[0].Type)
		return nil, nil, err
	}

	in, err := resolve()
	if err != nil {
		return nil, nil, err
	}
	key := KeyOf(in)
	hash := key.Hash()

	e.mu.Lock()
	defer e.mu.Unlock()

	if cached := e.lookupLocked(key); cached != nil {
		if fail := e.gateLocked(tx, cached); fail != nil {
			e.observer.RevalidationFailed(cached.ID, reasonOf(fail))
			if isImmediate(fail) {
				e.observer.ImmediateFailure(cached.ID, eventOf(fail))
				return nil, nil, fail
			}
			// Ordinary drift: treat as miss and recompile with fresh input.
			e.observer.CacheMiss(hash)
		} else {
			e.observer.CacheHit(cached.ID, hash)
			return cached, e.issueHandleLocked(tx.Principal), nil
		}
	} else {
		e.observer.CacheMiss(hash)
	}

	fresh, err := resolve()
	if err != nil {
		return nil, nil, err
	}
	plan, err := Compile(e.cat, fresh, factory)
	if err != nil {
		return nil, nil, errors.Join(ErrCompileRejected, err)
	}
	e.observer.PlanCompiled(plan.ID, hash)
	e.session.Put(plan)
	return plan, e.issueHandleLocked(tx.Principal), nil
}

func (e *Executor) lookupLocked(key CacheKey) *Plan {
	if p := e.session.Get(key); p != nil {
		return p
	}
	return e.global.Get(key)
}

// gateLocked applies immediate-invalidation checks first, then full
// snapshot revalidation.
func (e *Executor) gateLocked(tx *Transaction, plan *Plan) error {
	if err := checkImmediate(e.cat.Bus(), tx.beganFence, plan); err != nil {
		return err
	}
	return revalidate(e.cat, tx.Snapshot, plan)
}

func (e *Executor) issueHandleLocked(p PrincipalID) *Handle {
	h := &Handle{id: newPlanID(), principal: p}
	e.handles[h.id] = h
	return h
}

// checkHandle fails if a principal switch invalidated the handle.
func (e *Executor) checkHandleLocked(h *Handle) error {
	if h == nil || h.expired {
		return ErrHandleExpired
	}
	cur, ok := e.handles[h.id]
	if !ok || cur.expired {
		return ErrHandleExpired
	}
	return nil
}

// BindAndExecute runs a prepared plan immediately with structured
// parameter bindings. Bindings are matched positionally against the compiled
// parameter types; values never participate in plan identity. The full gate
// runs again, and zero operators execute on any failure.
func (e *Executor) BindAndExecute(tx *Transaction, h *Handle, plan *Plan, bindings []any) error {
	if err := checkBindings(plan, bindings); err != nil {
		return err
	}

	e.mu.Lock()
	if err := e.checkHandleLocked(h); err != nil {
		e.mu.Unlock()
		return err
	}
	if err := e.gateLocked(tx, plan); err != nil {
		e.observer.RevalidationFailed(plan.ID, reasonOf(err))
		e.mu.Unlock()
		if isImmediate(err) {
			e.observer.ImmediateFailure(plan.ID, eventOf(err))
		}
		return err
	}
	ops := append([]Operator(nil), plan.Operators...)
	e.mu.Unlock()

	for i, op := range ops {
		if err := op.Func(bindings); err != nil {
			return err
		}
		e.observer.OperatorExecuted(plan.ID, op.Name, i)
	}
	return nil
}

// Enqueue places a prepared plan in the waiting queue and captures the
// current invalidation fence.
func (e *Executor) Enqueue(tx *Transaction, h *Handle, plan *Plan) (*Pending, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkHandleLocked(h); err != nil {
		return nil, err
	}
	p := &Pending{
		Plan:       plan,
		Tx:         tx,
		EnqueuedAt: e.cat.Bus().Fence(),
	}
	e.pending[plan.ID] = p
	return p, nil
}

// Dispatch attempts to run a waiting plan. Immediate events published after
// enqueue fail it before the first operator; snapshot drift is a miss that
// returns ErrStalePlan (also with zero operators) for the caller to
// recompile.
func (e *Executor) Dispatch(p *Pending, bindings []any) error {
	if err := checkBindings(p.Plan, bindings); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := checkImmediate(e.cat.Bus(), p.EnqueuedAt, p.Plan); err != nil {
		e.observer.RevalidationFailed(p.Plan.ID, reasonOf(err))
		e.observer.ImmediateFailure(p.Plan.ID, eventOf(err))
		delete(e.pending, p.Plan.ID)
		return err
	}
	if err := revalidate(e.cat, p.Tx.Snapshot, p.Plan); err != nil {
		e.observer.RevalidationFailed(p.Plan.ID, reasonOf(err))
		delete(e.pending, p.Plan.ID)
		return err
	}

	for i, op := range p.Plan.Operators {
		if err := op.Func(bindings); err != nil {
			delete(e.pending, p.Plan.ID)
			return err
		}
		e.observer.OperatorExecuted(p.Plan.ID, op.Name, i)
	}
	delete(e.pending, p.Plan.ID)
	return nil
}

// InvalidateSession is called when a pooled connection switches principal:
// all session-local handles die and the SESSION cache is cleared. GLOBAL
// entries remain untouched and remain reachable via full-key match.
func (e *Executor) InvalidateSession() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, h := range e.handles {
		h.expired = true
		e.observer.HandleInvalidated(id)
	}
	e.handles = map[string]*Handle{}
	e.pending = map[string]*Pending{}
	e.session.Clear()
}

// InstallGlobal publishes a plan into the shared GLOBAL cache.
func (e *Executor) InstallGlobal(plan *Plan) {
	e.global.Put(plan)
}

// SessionCache exposes the session cache (mainly for tests/metrics).
func (e *Executor) SessionCache() *PlanCache { return e.session }

// GlobalCache exposes the global cache (mainly for tests/metrics).
func (e *Executor) GlobalCache() *PlanCache { return e.global }
