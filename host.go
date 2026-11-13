package netsec

import (
	"fmt"
	"sync"
)

// Host owns the authoritative policy snapshot, the process-global plan cache
// and the immediate-revocation fence. It is safe for concurrent use.
type Host struct {
	mu          sync.Mutex
	snapshot    *SecuritySnapshot
	cache       *GlobalCache
	observers   observerSet
	executorMu  sync.RWMutex
	executor    Executor
	sequence    uint64
	fence       []immediateRevocation
	nextVersion map[ObjectID]uint64
}

type immediateRevocation struct {
	Sequence   uint64
	Principal  PrincipalID
	ObjectID   ObjectID
	Operations OperationSet
}

// NewHost creates a host from a snapshot. The snapshot is copied, so later
// mutation of the caller's maps cannot silently change policy.
func NewHost(snapshot *SecuritySnapshot) *Host {
	if snapshot == nil {
		snapshot = NewSnapshot(0)
	}
	snap := snapshot.Clone()
	next := make(map[ObjectID]uint64, len(snap.Objects))
	for id, obj := range snap.Objects {
		next[id] = obj.SchemaVersion
	}
	return &Host{
		snapshot:    snap,
		cache:       NewGlobalCache(),
		nextVersion: next,
	}
}

// Snapshot returns a deep copy of the current policy snapshot.
func (h *Host) Snapshot() *SecuritySnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshot.Clone()
}

// GlobalCache exposes the process-global cache for diagnostics and tests.
// Hosts still review each hit against a pinned snapshot before execution.
func (h *Host) GlobalCache() *GlobalCache { return h.cache }

// AddObserver registers a synchronous observable callback.
func (h *Host) AddObserver(observer Observer) { h.observers.add(observer) }

// Observe is a convenience wrapper for ObserverFunc.
func (h *Host) Observe(fn func(Event)) { h.observers.add(ObserverFunc(fn)) }

// SetExecutor installs the primitive execution callback. It is invoked only
// after review passes; a rejected plan invokes it zero times.
func (h *Host) SetExecutor(executor Executor) {
	h.executorMu.Lock()
	h.executor = executor
	h.executorMu.Unlock()
}

func (h *Host) executeOperator(operator Operator, params BoundParameters) error {
	h.executorMu.RLock()
	executor := h.executor
	h.executorMu.RUnlock()
	if executor == nil {
		return nil
	}
	return executor.Execute(operator, params)
}

// PolicyEpoch returns the current global policy epoch.
func (h *Host) PolicyEpoch() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshot.PolicyEpoch
}

// Compile validates a complete compile input against the current snapshot and
// returns a reviewed immutable plan. It is the low-level counterpart to
// Session.Prepare; callers normally use Session.Execute.
func (h *Host) Compile(input CompileInput, compilers ...Compiler) (*Plan, error) {
	norm, err := input.normalize()
	if err != nil {
		return nil, err
	}
	key, err := norm.Key()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	snap := h.snapshot.Clone()
	h.mu.Unlock()
	if err := snap.Revalidate(norm); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPlanRejected, err)
	}
	if plan, ok := h.cache.Get(key); ok {
		if err := snap.Revalidate(plan.Input()); err == nil {
			h.observers.emit(Event{
				Type:        EventCacheHit,
				PlanKey:     key,
				PrincipalID: norm.PrincipalID,
				PolicyEpoch: snap.PolicyEpoch,
			})
			return plan, nil
		}
		h.cache.Delete(key)
	}
	h.observers.emit(Event{
		Type:        EventCacheMiss,
		PlanKey:     key,
		PrincipalID: norm.PrincipalID,
		PolicyEpoch: snap.PolicyEpoch,
	})
	ops, err := compileWith(compilerOrNil(compilers), norm)
	if err != nil {
		return nil, err
	}
	plan := newPlan(norm, key, ops)
	h.cache.Put(plan)
	h.observers.emit(Event{
		Type:        EventPlanCompiled,
		PlanKey:     key,
		PrincipalID: norm.PrincipalID,
		PolicyEpoch: snap.PolicyEpoch,
	})
	return plan, nil
}

// Session is a pooled connection. Local plan handles belong to the current
// principal and are discarded whenever the connection switches principal.
type Session struct {
	mu         sync.Mutex
	host       *Host
	principal  PrincipalID
	local      map[PlanKey]*Plan
	tx         *Transaction
	generation uint64
	closed     bool
}

// Connection is an alias for Session.
type Connection = Session

// NewSession acquires a connection for a principal.
func (h *Host) NewSession(principal PrincipalID) (*Session, error) {
	if principal == "" {
		return nil, fmt.Errorf("netsec: empty principal")
	}
	return &Session{
		host:       h,
		principal:  principal,
		local:      make(map[PlanKey]*Plan),
		generation: 1,
	}, nil
}

// Acquire is an alias for NewSession.
func (h *Host) Acquire(principal PrincipalID) (*Session, error) { return h.NewSession(principal) }

// PrincipalID reports the connection's current principal.
func (s *Session) PrincipalID() PrincipalID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.principal
}

// SwitchPrincipal changes the connection principal and clears every
// session-local plan handle. Global cache entries are not cleared; the new
// principal can only reach them through its own complete key.
func (s *Session) SwitchPrincipal(principal PrincipalID) error {
	if principal == "" {
		return fmt.Errorf("netsec: empty principal")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.tx != nil {
		s.tx.abort()
		s.tx = nil
	}
	s.principal = principal
	s.local = make(map[PlanKey]*Plan)
	s.generation++
	return nil
}

// SetPrincipal is an alias for SwitchPrincipal.
func (s *Session) SetPrincipal(principal PrincipalID) error {
	return s.SwitchPrincipal(principal)
}

// SwitchUser is an alias for SwitchPrincipal.
func (s *Session) SwitchUser(principal PrincipalID) error {
	return s.SwitchPrincipal(principal)
}

// LocalPlanCount returns the number of session-local handles.
func (s *Session) LocalPlanCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.local)
}

// PlanCount is an alias for LocalPlanCount.
func (s *Session) PlanCount() int { return s.LocalPlanCount() }

// Close releases the session.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.tx != nil {
		s.tx.abort()
		s.tx = nil
	}
	s.local = nil
	s.closed = true
	s.generation++
	return nil
}

// Begin starts a transaction pinned to the current policy snapshot.
func (s *Session) Begin() (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	if s.tx != nil {
		return nil, fmt.Errorf("netsec: transaction already active")
	}
	s.host.mu.Lock()
	snap := s.host.snapshot.Clone()
	seq := s.host.sequence
	s.host.mu.Unlock()
	tx := &Transaction{
		session:   s,
		snapshot:  snap,
		startSeq:  seq,
		principal: s.principal,
	}
	s.tx = tx
	return tx, nil
}

// Transaction returns the active transaction, or nil.
func (s *Session) Transaction() *Transaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tx
}

// Transaction fixes one authorization snapshot for all of its statements.
type Transaction struct {
	mu        sync.Mutex
	session   *Session
	snapshot  *SecuritySnapshot
	startSeq  uint64
	principal PrincipalID
	done      bool
}

// Snapshot returns a copy of the transaction's pinned snapshot.
func (tx *Transaction) Snapshot() *SecuritySnapshot {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.snapshot.Clone()
}

// Prepare compiles or retrieves a plan inside this transaction's fixed
// authorization snapshot.
func (tx *Transaction) Prepare(req ExecutionRequest, compilers ...Compiler) (*PendingPlan, error) {
	tx.mu.Lock()
	done := tx.done
	tx.mu.Unlock()
	if done {
		return nil, ErrTransactionDone
	}
	if err := tx.session.ensureTransaction(tx); err != nil {
		return nil, err
	}
	return tx.session.Prepare(req, compilers...)
}

// Execute runs one statement inside this transaction.
func (tx *Transaction) Execute(req ExecutionRequest, compilers ...Compiler) (ExecutionResult, error) {
	tx.mu.Lock()
	done := tx.done
	tx.mu.Unlock()
	if done {
		return ExecutionResult{}, ErrTransactionDone
	}
	if err := tx.session.ensureTransaction(tx); err != nil {
		return ExecutionResult{}, err
	}
	return tx.session.Execute(req, compilers...)
}

// Run is an alias for Transaction.Execute.
func (tx *Transaction) Run(req ExecutionRequest, compilers ...Compiler) (ExecutionResult, error) {
	return tx.Execute(req, compilers...)
}

func (s *Session) ensureTransaction(tx *Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.tx != tx {
		return fmt.Errorf("%w: transaction is not active on this session", ErrTransactionDone)
	}
	return nil
}

// Commit ends the transaction. A non-immediate REVOKE becomes visible to the
// next transaction, which starts from the host's current snapshot.
func (tx *Transaction) Commit() error { return tx.finish() }

// Rollback ends the transaction.
func (tx *Transaction) Rollback() error { return tx.finish() }

func (tx *Transaction) finish() error {
	tx.session.mu.Lock()
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.done {
		tx.session.mu.Unlock()
		return ErrTransactionDone
	}
	tx.done = true
	if tx.session.tx == tx {
		tx.session.tx = nil
	}
	tx.session.mu.Unlock()
	return nil
}

func (tx *Transaction) abort() {
	tx.mu.Lock()
	tx.done = true
	tx.mu.Unlock()
}

// ExecutionRequest is one statement. Param values are bound structurally at
// execution time and are not part of plan identity.
type ExecutionRequest struct {
	Query  Query
	Params BoundParameters
}

// ExecutionResult reports the reviewed plan and how many operators ran.
type ExecutionResult struct {
	PlanKey           PlanKey
	PrincipalID       PrincipalID
	PolicyEpoch       uint64
	CacheHit          bool
	OperatorsExecuted int
}

type executionContext struct {
	principal  PrincipalID
	generation uint64
	tx         *Transaction
	snapshot   *SecuritySnapshot
	startSeq   uint64
}

func (s *Session) context() (executionContext, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return executionContext{}, ErrSessionClosed
	}
	ctx := executionContext{
		principal:  s.principal,
		generation: s.generation,
		tx:         s.tx,
	}
	s.mu.Unlock()

	if ctx.tx != nil {
		ctx.tx.mu.Lock()
		if ctx.tx.done {
			ctx.tx.mu.Unlock()
			return executionContext{}, ErrTransactionDone
		}
		ctx.snapshot = ctx.tx.snapshot
		ctx.startSeq = ctx.tx.startSeq
		ctx.principal = ctx.tx.principal
		ctx.tx.mu.Unlock()
		return ctx, nil
	}

	s.host.mu.Lock()
	ctx.snapshot = s.host.snapshot.Clone()
	ctx.startSeq = s.host.sequence
	s.host.mu.Unlock()
	return ctx, nil
}

// Prepare compiles or retrieves a plan and performs the first review pass. It
// does not run any operators. The returned PendingPlan deliberately models a
// plan waiting to execute.
func (s *Session) Prepare(req ExecutionRequest, compilers ...Compiler) (*PendingPlan, error) {
	ctx, err := s.context()
	if err != nil {
		return nil, err
	}
	if err := s.host.fenceError(ctx.startSeq, ctx.principal, queryObjectIDs(req.Query), req.Query.Objects); err != nil {
		s.host.observers.emit(Event{Type: EventPlanRejected, PrincipalID: ctx.principal, Err: err})
		return nil, err
	}
	compiler := compilerOrNil(compilers)

	input, err := ctx.snapshot.CompileInput(req.Query, ctx.principal)
	if err != nil {
		s.host.observers.emit(Event{Type: EventPlanRejected, PrincipalID: ctx.principal, Err: err})
		return nil, err
	}
	if err := req.Params.Validate(input.ParamTypes); err != nil {
		s.host.observers.emit(Event{Type: EventPlanRejected, PrincipalID: ctx.principal, Err: err})
		return nil, err
	}

	key := input.CacheKey()
	plan, cacheHit, err := s.reviewOrCompile(ctx, key, input, compiler)
	if err != nil {
		s.host.observers.emit(Event{Type: EventPlanRejected, PlanKey: key, PrincipalID: ctx.principal, Err: err})
		return nil, err
	}
	if err := ctx.snapshot.Revalidate(plan.Input()); err != nil {
		// A plan that cannot pass review is never returned as executable.
		s.host.cache.Delete(key)
		s.dropLocal(key)
		s.host.observers.emit(Event{Type: EventPlanRejected, PlanKey: key, PrincipalID: ctx.principal, Err: err})
		return nil, fmt.Errorf("%w: %w", ErrPlanRejected, err)
	}

	// Do not install a session-local handle if the connection switched
	// principal while compilation was running.
	s.mu.Lock()
	if !s.closed && s.generation == ctx.generation && s.principal == ctx.principal {
		s.local[key] = plan
	}
	s.mu.Unlock()

	s.host.observers.emit(Event{
		Type:        EventPlanValidated,
		PlanKey:     plan.Key(),
		PrincipalID: ctx.principal,
		PolicyEpoch: ctx.snapshot.PolicyEpoch,
	})
	return &PendingPlan{
		session:    s,
		host:       s.host,
		snapshot:   ctx.snapshot,
		tx:         ctx.tx,
		principal:  ctx.principal,
		generation: ctx.generation,
		startSeq:   ctx.startSeq,
		request:    req,
		compiler:   compiler,
		input:      input,
		plan:       plan,
		params:     req.Params,
		cacheHit:   cacheHit,
	}, nil
}

func (s *Session) reviewOrCompile(ctx executionContext, key PlanKey, input CompileInput, compiler Compiler) (*Plan, bool, error) {
	if plan := s.getLocal(key); plan != nil {
		if err := ctx.snapshot.Revalidate(plan.Input()); err == nil {
			s.host.observers.emit(Event{
				Type:        EventCacheHit,
				PlanKey:     key,
				PrincipalID: ctx.principal,
				PolicyEpoch: ctx.snapshot.PolicyEpoch,
			})
			return plan, true, nil
		}
		s.dropLocal(key)
		s.host.cache.Delete(key)
	}

	if plan, ok := s.host.cache.Get(key); ok {
		if err := ctx.snapshot.Revalidate(plan.Input()); err == nil {
			s.host.observers.emit(Event{
				Type:        EventCacheHit,
				PlanKey:     key,
				PrincipalID: ctx.principal,
				PolicyEpoch: ctx.snapshot.PolicyEpoch,
			})
			return plan, true, nil
		}
		// The weak or stale entry is removed. It is never executed and its
		// prior allow decision is not inherited.
		s.host.cache.Delete(key)
	}

	s.host.observers.emit(Event{
		Type:        EventCacheMiss,
		PlanKey:     key,
		PrincipalID: ctx.principal,
		PolicyEpoch: ctx.snapshot.PolicyEpoch,
	})
	ops, err := compileWith(compiler, input)
	if err != nil {
		return nil, false, err
	}
	plan := newPlan(input, key, ops)
	s.host.cache.Put(plan)
	s.host.observers.emit(Event{
		Type:        EventPlanCompiled,
		PlanKey:     key,
		PrincipalID: ctx.principal,
		PolicyEpoch: ctx.snapshot.PolicyEpoch,
	})
	return plan, false, nil
}

func (s *Session) getLocal(key PlanKey) *Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.local[key]
}

func (s *Session) dropLocal(key PlanKey) {
	s.mu.Lock()
	delete(s.local, key)
	s.mu.Unlock()
}

// Execute prepares a plan and immediately runs it. For a non-transactional
// statement it refreshes the snapshot at the final boundary, so a policy
// change after preparation is a miss and forces recompilation.
func (s *Session) Execute(req ExecutionRequest, compilers ...Compiler) (ExecutionResult, error) {
	pending, err := s.Prepare(req, compilers...)
	if err != nil {
		return ExecutionResult{}, err
	}
	return pending.run(true)
}

// Run is an alias for Execute.
func (s *Session) Run(req ExecutionRequest, compilers ...Compiler) (ExecutionResult, error) {
	return s.Execute(req, compilers...)
}

// ExecuteQuery is a typed convenience wrapper.
func (s *Session) ExecuteQuery(q Query, params BoundParameters, compilers ...Compiler) (ExecutionResult, error) {
	return s.Execute(ExecutionRequest{Query: q, Params: params}, compilers...)
}

// PendingPlan represents a reviewed plan that has not yet run. Calling
// Execute rechecks the principal, transaction snapshot, role closure,
// policyEpoch, object generations, RLS digests and privileges. Any failure
// rejects the plan with zero operator execution.
type PendingPlan struct {
	session    *Session
	host       *Host
	snapshot   *SecuritySnapshot
	tx         *Transaction
	principal  PrincipalID
	generation uint64
	startSeq   uint64
	request    ExecutionRequest
	compiler   Compiler
	input      CompileInput
	plan       *Plan
	params     BoundParameters
	cacheHit   bool
}

// Plan returns the reviewed plan.
func (p *PendingPlan) Plan() *Plan { return p.plan }

// CacheHit reports whether the plan came from a cache after review.
func (p *PendingPlan) CacheHit() bool { return p.cacheHit }

// Execute performs final review and then runs operators. A waiting plan keeps
// its transaction-pinned snapshot, but it still rechecks the immediate
// revocation fence, principal, role closure, epoch, object generations and
// privileges. Non-transactional waiting plans refresh the current snapshot.
func (p *PendingPlan) Execute() (ExecutionResult, error) {
	return p.run(true)
}

func (p *PendingPlan) run(refresh bool) (ExecutionResult, error) {
	result := ExecutionResult{
		PlanKey:     p.plan.Key(),
		PrincipalID: p.principal,
		PolicyEpoch: p.snapshot.PolicyEpoch,
		CacheHit:    p.cacheHit,
	}

	if p.tx == nil && refresh {
		return p.refreshAndRun()
	}

	if err := p.sameConnection(); err != nil {
		p.reject(err)
		return result, err
	}
	if p.tx != nil {
		p.tx.mu.Lock()
		done := p.tx.done
		p.tx.mu.Unlock()
		if done {
			err := ErrTransactionDone
			p.reject(err)
			return result, err
		}
	}

	// A waiting plan is fenced by an explicit immediate REVOKE even though
	// its transaction otherwise keeps the old snapshot. A non-immediate
	// REVOKE does not appear in the fence and therefore only affects the
	// next transaction.
	if err := p.host.fenceError(p.startSeq, p.principal, queryObjectIDs(p.request.Query), p.request.Query.Objects); err != nil {
		p.reject(err)
		return result, err
	}
	if err := p.snapshot.Revalidate(p.plan.Input()); err != nil {
		p.host.cache.Delete(p.plan.Key())
		p.session.dropLocal(p.plan.Key())
		p.reject(err)
		return result, err
	}
	if err := p.params.Validate(p.input.ParamTypes); err != nil {
		p.reject(err)
		return result, err
	}

	result.OperatorsExecuted, result.PolicyEpoch = 0, p.snapshot.PolicyEpoch
	ops := p.plan.Operators()
	for i, op := range ops {
		// Recheck immediately before each primitive. A revocation injected
		// through an observable callback cannot let the next operator run.
		if err := p.preOperatorCheck(); err != nil {
			p.reject(err)
			return result, err
		}
		p.host.observers.emit(Event{
			Type:          EventOperatorStart,
			PlanKey:       p.plan.Key(),
			PrincipalID:   p.principal,
			PolicyEpoch:   p.snapshot.PolicyEpoch,
			OperatorIndex: i,
			Operator:      op,
		})
		// An observer may have injected a policy change. Do not call the
		// executor unless the plan still passes review.
		if err := p.preOperatorCheck(); err != nil {
			p.reject(err)
			return result, err
		}
		if err := p.host.executeOperator(op, p.params); err != nil {
			p.reject(err)
			return result, err
		}
		p.host.observers.emit(Event{
			Type:          EventOperatorEnd,
			PlanKey:       p.plan.Key(),
			PrincipalID:   p.principal,
			PolicyEpoch:   p.snapshot.PolicyEpoch,
			OperatorIndex: i,
			Operator:      op,
		})
		result.OperatorsExecuted++
	}
	return result, nil
}

func (p *PendingPlan) refreshAndRun() (ExecutionResult, error) {
	ctx, err := p.session.context()
	if err != nil {
		p.reject(err)
		return ExecutionResult{}, err
	}
	if ctx.principal != p.principal || ctx.generation != p.generation {
		err = fmt.Errorf("%w: connection principal changed", ErrPlanRejected)
		p.reject(err)
		return ExecutionResult{}, err
	}
	// A waiting plan is fenced by an immediate revocation that happened
	// after it was prepared, even outside an explicit transaction.
	if err := p.host.fenceError(p.startSeq, ctx.principal, queryObjectIDs(p.request.Query), p.request.Query.Objects); err != nil {
		p.reject(err)
		return ExecutionResult{}, err
	}
	if err := p.host.fenceError(ctx.startSeq, ctx.principal, queryObjectIDs(p.request.Query), p.request.Query.Objects); err != nil {
		p.reject(err)
		return ExecutionResult{}, err
	}
	input, err := ctx.snapshot.CompileInput(p.request.Query, ctx.principal)
	if err != nil {
		p.reject(err)
		return ExecutionResult{}, err
	}
	if err := p.params.Validate(input.ParamTypes); err != nil {
		p.reject(err)
		return ExecutionResult{}, err
	}

	key := input.CacheKey()
	if key != p.input.CacheKey() || ctx.snapshot.Revalidate(p.plan.Input()) != nil {
		// The prepared plan is a miss at the final boundary. Recompile from
		// the current snapshot and only execute the replacement.
		plan, hit, err := p.session.reviewOrCompile(ctx, key, input, p.compiler)
		if err != nil {
			p.reject(err)
			return ExecutionResult{}, err
		}
		if err := ctx.snapshot.Revalidate(plan.Input()); err != nil {
			p.host.cache.Delete(key)
			p.reject(err)
			return ExecutionResult{}, err
		}
		p.snapshot = ctx.snapshot
		p.input = input
		p.plan = plan
		p.cacheHit = hit
	}
	p.snapshot = ctx.snapshot
	p.startSeq = ctx.startSeq
	return p.run(false)
}

func (p *PendingPlan) sameConnection() error {
	s := p.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.generation != p.generation || s.principal != p.principal {
		return fmt.Errorf("%w: connection switched principal", ErrPlanRejected)
	}
	if p.tx != nil && s.tx != p.tx {
		return fmt.Errorf("%w: transaction changed", ErrPlanRejected)
	}
	return nil
}

func (p *PendingPlan) preOperatorCheck() error {
	if err := p.sameConnection(); err != nil {
		return err
	}
	if p.tx != nil {
		p.tx.mu.Lock()
		done := p.tx.done
		p.tx.mu.Unlock()
		if done {
			return ErrTransactionDone
		}
	}
	if err := p.host.fenceError(p.startSeq, p.principal, queryObjectIDs(p.request.Query), p.request.Query.Objects); err != nil {
		return err
	}
	if err := p.snapshot.Revalidate(p.plan.Input()); err != nil {
		return err
	}
	return p.params.Validate(p.input.ParamTypes)
}

func (p *PendingPlan) reject(err error) {
	p.host.observers.emit(Event{
		Type:        EventPlanRejected,
		PlanKey:     p.plan.Key(),
		PrincipalID: p.principal,
		PolicyEpoch: p.snapshot.PolicyEpoch,
		Err:         err,
	})
}

func queryObjectIDs(q Query) []ObjectID {
	out := make([]ObjectID, 0, len(q.Objects))
	for _, ref := range q.Objects {
		out = append(out, ref.ObjectID)
	}
	return out
}

func (h *Host) fenceError(after uint64, principal PrincipalID, objects []ObjectID, refs []ObjectRef) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, event := range h.fence {
		if event.Sequence <= after {
			continue
		}
		if event.Principal != "" && event.Principal != principal {
			continue
		}
		if event.ObjectID != "" && !containsObject(objects, event.ObjectID) {
			continue
		}
		if !event.Operations.IsZero() && !operationsOverlap(event.ObjectID, event.Operations, refs) {
			continue
		}
		return &RevocationError{
			Sequence:   event.Sequence,
			Principal:  event.Principal,
			ObjectID:   event.ObjectID,
			Operations: event.Operations,
		}
	}
	return nil
}

func containsObject(objects []ObjectID, want ObjectID) bool {
	for _, id := range objects {
		if id == want {
			return true
		}
	}
	return false
}

func operationsOverlap(objectID ObjectID, ops OperationSet, refs []ObjectRef) bool {
	for _, ref := range refs {
		if objectID != "" && ref.ObjectID != objectID {
			continue
		}
		if ref.Operations&ops != 0 {
			return true
		}
	}
	return false
}
