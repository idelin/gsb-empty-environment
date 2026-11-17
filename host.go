package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ThreadID identifies a host thread. The package models thread state; the
// embedding host is responsible for mapping IDs to real OS threads.
type ThreadID uint64

// ThreadState is a snapshot of one thread's installed chain.
type ThreadState struct {
	ID    ThreadID
	Chain *Chain
}

// InstallFunc applies next to a real thread. Returning an error means the
// thread was not changed or was changed atomically by the host itself.
type InstallFunc func(thread ThreadID, previous, next *Chain) error

// RollbackFunc restores previous on a thread for which InstallFunc previously
// succeeded. It is called in reverse installation order when a later thread
// fails.
type RollbackFunc func(thread ThreadID, previous, next *Chain) error

// ThreadBackend groups the two host-side operations used by Host.
type ThreadBackend interface {
	Install(thread ThreadID, previous, next *Chain) error
	Rollback(thread ThreadID, previous, next *Chain) error
}

// InstallError describes which thread rejected a synchronized installation.
type InstallError struct {
	Thread      ThreadID
	Err         error
	RollbackErr error
}

func (e *InstallError) Error() string {
	if e.RollbackErr != nil {
		return fmt.Sprintf("%s: thread %d: %v (rollback: %v)", ErrInstallFailed, e.Thread, e.Err, e.RollbackErr)
	}
	return fmt.Sprintf("%s: thread %d: %v", ErrInstallFailed, e.Thread, e.Err)
}

// Unwrap exposes both the installation error and any rollback error.
func (e *InstallError) Unwrap() []error {
	if e.RollbackErr != nil {
		return []error{e.Err, e.RollbackErr}
	}
	return []error{e.Err}
}

// Host owns the process-wide chain and the set of registered threads. All
// registered threads share the same immutable chain snapshot.
type Host struct {
	mu         sync.RWMutex
	chain      *Chain
	threads    map[ThreadID]*ThreadState
	installFn  InstallFunc
	rollbackFn RollbackFunc
}

// Sandbox and ThreadGroup are aliases for Host.
type (
	Sandbox     = Host
	ThreadGroup = Host
)

// NewHost creates a host with the supplied thread IDs already registered.
func NewHost(threads ...ThreadID) *Host {
	h := &Host{
		chain:   &Chain{},
		threads: make(map[ThreadID]*ThreadState, len(threads)),
	}
	for _, id := range threads {
		h.threads[id] = &ThreadState{ID: id, Chain: h.chain}
	}
	return h
}

// AddThread registers a thread. A new thread inherits the current chain, so
// all threads continue to share one immutable snapshot.
func (h *Host) AddThread(id ThreadID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ensureLocked()
	if _, exists := h.threads[id]; exists {
		return fmt.Errorf("%w: %d", ErrThreadExists, id)
	}
	h.threads[id] = &ThreadState{ID: id, Chain: h.chain}
	return nil
}

// RemoveThread unregisters a thread.
func (h *Host) RemoveThread(id ThreadID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ensureLocked()
	if _, exists := h.threads[id]; !exists {
		return fmt.Errorf("%w: %d", ErrUnknownThread, id)
	}
	delete(h.threads, id)
	return nil
}

// Chain returns the process-wide immutable chain.
func (h *Host) Chain() *Chain {
	h.mu.RLock()
	chain := h.chain
	h.mu.RUnlock()
	if chain != nil {
		return chain
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.ensureLocked()
	return h.chain
}

// Current is an alias for Chain.
func (h *Host) Current() *Chain {
	return h.Chain()
}

// ThreadChain returns the chain visible to one thread.
func (h *Host) ThreadChain(id ThreadID) (*Chain, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	thread, exists := h.threads[id]
	if !exists {
		return nil, fmt.Errorf("%w: %d", ErrUnknownThread, id)
	}
	return thread.Chain, nil
}

// Threads returns a snapshot of all registered threads.
func (h *Host) Threads() []ThreadState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]ThreadState, 0, len(h.threads))
	for _, id := range h.threadIDsLocked() {
		thread := h.threads[id]
		out = append(out, *thread)
	}
	return out
}

// ThreadIDs returns registered IDs in ascending order.
func (h *Host) ThreadIDs() []ThreadID {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.threadIDsLocked()
}

// ThreadCount returns the number of registered threads.
func (h *Host) ThreadCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.threads)
}

// SetInstallFunc installs the host-side apply hook used by Install.
func (h *Host) SetInstallFunc(fn InstallFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.installFn = fn
}

// SetRollbackFunc installs the compensating hook used after a failed
// all-thread installation.
func (h *Host) SetRollbackFunc(fn RollbackFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rollbackFn = fn
}

// SetBackend sets both host-side hooks.
func (h *Host) SetBackend(backend ThreadBackend) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if backend == nil {
		h.installFn = nil
		h.rollbackFn = nil
		return
	}
	h.installFn = backend.Install
	h.rollbackFn = backend.Rollback
}

// Install appends filters and applies the resulting chain to every registered
// thread as one transaction.
//
// The candidate chain is composed and validated before any thread is touched.
// If the host-side install hook fails for a thread, already-applied threads
// are rolled back in reverse order and the Host's state remains exactly the
// previous chain. Thus a failed synchronized installation leaves no partial
// chain visible to ThreadChain, Chain, or a newly added thread.
func (h *Host) Install(filters ...Filter) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ensureLocked()

	if len(filters) == 0 {
		return nil
	}
	next, err := h.chain.Append(filters...)
	if err != nil {
		return err
	}
	previous := h.chain

	ids := h.threadIDsLocked()
	applied := make([]ThreadID, 0, len(ids))
	if h.installFn != nil {
		for _, id := range ids {
			if err := h.installFn(id, previous, next); err != nil {
				rollbackErr := h.rollbackLocked(applied, previous, next)
				return &InstallError{
					Thread:      id,
					Err:         fmt.Errorf("%w: %w", ErrInstallFailed, err),
					RollbackErr: rollbackErr,
				}
			}
			applied = append(applied, id)
		}
	}

	h.chain = next
	for _, thread := range h.threads {
		thread.Chain = next
	}
	return nil
}

// Append is an alias for Install.
func (h *Host) Append(filters ...Filter) error {
	return h.Install(filters...)
}

// InstallFilter installs one filter.
func (h *Host) InstallFilter(filter Filter) error {
	return h.Install(filter)
}

// InstallAll appends filters to a host. It is a convenience wrapper for hosts
// that prefer a function over a method call.
func InstallAll(host *Host, filters ...Filter) error {
	if host == nil {
		return errors.New("netsec: nil host")
	}
	return host.Install(filters...)
}

func (h *Host) ensureLocked() {
	if h.chain == nil {
		h.chain = &Chain{}
	}
	if h.threads == nil {
		h.threads = make(map[ThreadID]*ThreadState)
	}
}

func (h *Host) rollbackLocked(threads []ThreadID, previous, next *Chain) error {
	if h.rollbackFn == nil || len(threads) == 0 {
		return nil
	}
	var errs []error
	for i := len(threads) - 1; i >= 0; i-- {
		id := threads[i]
		if err := h.rollbackFn(id, previous, next); err != nil {
			errs = append(errs, fmt.Errorf("thread %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (h *Host) threadIDsLocked() []ThreadID {
	ids := make([]ThreadID, 0, len(h.threads))
	for id := range h.threads {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
