package capacitycred

import (
	"context"
	"errors"
	"sync"
)

// Engine is the importable capacity credential service component. It owns
// online event merging, finality receipts and the local KDF task lifecycle.
type Engine struct {
	cb        Callbacks
	local     NodeID
	allocator Allocator
	mu        sync.Mutex
	k         *kernel
	tasks     map[TaskID]*taskState
}

type taskState struct {
	id      TaskID
	cred    CredID
	tenant  TenantID
	params  Params
	size    uint64
	slot    Slot
	phase   taskPhase
	release bool
	runMu   sync.Mutex
}

type taskPhase int

const (
	phaseReserved taskPhase = iota
	phaseRunning
	phaseFinished
)

// NewEngine rejects construction when either mandatory callback or the
// allocator is missing. It never falls back to default crypto behavior.
func NewEngine(cb Callbacks, local NodeID, allocator Allocator) (*Engine, error) {
	if err := cb.valid(); err != nil {
		return nil, err
	}
	if local == "" {
		return nil, ErrWrongOwner
	}
	if allocator == nil {
		return nil, ErrMissingCallback
	}
	return &Engine{
		cb:        cb,
		local:     local,
		allocator: allocator,
		k:         newKernel(cb),
		tasks:     make(map[TaskID]*taskState),
	}, nil
}

// Receive is the inbound event entry. Callbacks are re-checked here so a
// per-delivery nil callback is rejected immediately instead of being skipped.
func (e *Engine) Receive(envelope Envelope) error {
	if err := envelope.Callbacks.valid(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.k.ingest(envelope.Callbacks, envelope.Event)
}

// ReceiveReceipt accepts a caller finality receipt through the same strict
// callback and signature checks as event ingestion.
func (e *Engine) ReceiveReceipt(cb Callbacks, r Receipt) error {
	if err := cb.valid(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.k.acceptReceipt(cb, r)
}

// Snapshot returns the deterministic current state. Task buffers are local
// runtime state and are represented through HeldBytes only.
func (e *Engine) Snapshot() *Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.k.snapshot(e.local)
}

// ReplayAll recomputes state from an immutable event set and returns a
// snapshot identical to online incremental merging of the same set.
func (e *Engine) ReplayAll(envelopes []Envelope) (*Snapshot, error) {
	if err := e.cb.valid(); err != nil {
		return nil, err
	}
	for _, envelope := range envelopes {
		if err := envelope.Callbacks.valid(); err != nil {
			return nil, err
		}
	}
	return Recompute(envelopes, e.local)
}

// ReservationRequest opens exactly one reservation per TaskID. Re-entering
// after a crash with the same request is idempotent; a changed request fails.
type ReservationRequest struct {
	TaskID TaskID
	CredID CredID
	Tenant TenantID
	Params Params
}

func (e *Engine) Reserve(req ReservationRequest) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	size, err := MemoryBytes(req.Params.SCost)
	if err != nil {
		return err
	}
	if existing, ok := e.tasks[req.TaskID]; ok {
		if existing.cred != req.CredID || existing.tenant != req.Tenant ||
			existing.size != size || !sameParams(existing.params, req.Params) {
			return ErrTaskReentry
		}
		return nil
	}

	cred, ok := e.k.creds[req.CredID]
	if !ok {
		return ErrUnknownCred
	}
	if cred.tenant != req.Tenant {
		return ErrTenantMismatch
	}
	if cred.owner != e.local {
		return ErrWrongOwner
	}
	if e.k.blocked(cred.id) {
		return ErrBlocked
	}
	if _, ok := e.k.receipts[cred.root]; !ok {
		return ErrNoFinality
	}
	commit, err := ParamsCommitment(e.cb, req.Params)
	if err != nil {
		return err
	}
	if string(commit) != string(cred.commitment) {
		return ErrParamsMismatch
	}
	free, ok := e.k.freeBytes(cred.id)
	if !ok || free < size {
		return ErrInsufficient
	}
	slot, err := e.allocator.Acquire(size)
	if err != nil {
		return errors.Join(ErrAllocator, err)
	}

	e.tasks[req.TaskID] = &taskState{
		id:     req.TaskID,
		cred:   req.CredID,
		tenant: req.Tenant,
		params: cloneParams(req.Params),
		size:   size,
		slot:   slot,
		phase:  phaseReserved,
	}
	e.k.holds[cred.id], err = checkedAdd(e.k.holds[cred.id], size)
	if err != nil {
		slot.Erase()
		e.allocator.Release(slot)
		delete(e.tasks, req.TaskID)
		return err
	}
	return nil
}

// Run invokes the host hash callback once per task. A repeated Run of the same
// task never occupies another buffer. A panic, context cancellation or
// callback error still leaves the delivered buffer owned until Complete or
// Cancel; the quota is released exactly once and only after Erase.
func (e *Engine) Run(ctx context.Context, id TaskID) (output []byte, err error) {
	return e.RunWithHash(ctx, id, e.cb.Hash)
}

// RunWithHash runs a task with a host-supplied hash callback. The component
// still schedules no other primitive; a nil callback is rejected immediately.
func (e *Engine) RunWithHash(ctx context.Context, id TaskID, hash HashFunc) (output []byte, err error) {
	if hash == nil {
		return nil, ErrMissingCallback
	}
	e.mu.Lock()
	task, ok := e.tasks[id]
	if !ok {
		e.mu.Unlock()
		return nil, ErrTaskNotFound
	}
	task.runMu.Lock()
	defer task.runMu.Unlock()
	if task.phase == phaseFinished {
		e.mu.Unlock()
		return nil, ErrTaskCompleted
	}
	if task.phase == phaseRunning {
		e.mu.Unlock()
		return nil, ErrTaskRunning
	}
	task.phase = phaseRunning
	e.mu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			e.mu.Lock()
			task.phase = phaseReserved
			e.mu.Unlock()
			err = ErrTaskRunning
			if panicErr, ok := recovered.(error); ok {
				err = errors.Join(ErrTaskRunning, panicErr)
			}
		}
	}()

	body := taskInput(task)
	result, hashErr := hash(ctx, body)

	e.mu.Lock()
	defer e.mu.Unlock()
	if hashErr != nil {
		task.phase = phaseReserved
		return nil, hashErr
	}
	task.phase = phaseReserved
	return result, nil
}

// Complete finishes a task. The buffer is erased first; only then is quota
// returned. A second completion cannot release the same bytes twice.
func (e *Engine) Complete(id TaskID) error {
	return e.finish(id, false)
}

// Cancel aborts a task with the same exactly-once release semantics.
func (e *Engine) Cancel(id TaskID) error {
	return e.finish(id, true)
}

func (e *Engine) finish(id TaskID, cancel bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if task.release {
		return ErrTaskCompleted
	}
	if task.phase == phaseRunning {
		return ErrTaskRunning
	}
	task.slot.Erase()
	e.allocator.Release(task.slot)
	task.slot = nil
	task.release = true
	task.phase = phaseFinished
	held, err := checkedSub(e.k.holds[task.cred], task.size)
	if err != nil {
		return ErrOverflow
	}
	e.k.holds[task.cred] = held
	if cancel {
		delete(e.tasks, id)
	}
	return nil
}

func sameParams(a, b Params) bool {
	return a.SCost == b.SCost && a.Iterations == b.Iterations && a.Parallel == b.Parallel &&
		string(a.Salt) == string(b.Salt) && string(a.Password) == string(b.Password) &&
		a.Label == b.Label
}

func cloneParams(p Params) Params {
	p.Salt = append([]byte(nil), p.Salt...)
	p.Password = append([]byte(nil), p.Password...)
	return p
}

func taskInput(t *taskState) []byte {
	b := new(canonicalBuilder)
	b.tag(1).id(string(t.id))
	b.tag(2).id(string(t.tenant))
	b.tag(3).id(string(t.cred))
	b.tag(4).u64(t.params.SCost)
	b.tag(5).u64(t.params.Iterations)
	b.tag(6).u32(t.params.Parallel)
	b.tag(7).bytes(t.params.Salt)
	b.tag(8).bytes(t.params.Password)
	b.tag(9).str(t.params.Label)
	return b.buf
}
