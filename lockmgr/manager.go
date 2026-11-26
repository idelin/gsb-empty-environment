package lockmgr

import "sync"

// Manager is the lock manager. A single Manager owns the whole DB -> Table ->
// Page -> Row hierarchy and serializes every lock decision with one mutex,
// which makes queue ordering and deadlock arbitration deterministic.
type Manager struct {
	mu sync.Mutex

	seq       uint64 // global monotonic sequence (start / acquisition order)
	nextTxnID uint64
	nextSpID  uint64

	txns      map[uint64]*Transaction
	resources map[string]*resourceState

	resolutions []Resolution
}

// NewManager creates an empty lock manager.
func NewManager() *Manager {
	return &Manager{
		txns:      make(map[uint64]*Transaction),
		resources: make(map[string]*resourceState),
	}
}

func (m *Manager) nextSeq() uint64 {
	m.seq++
	return m.seq
}

// Begin starts a new transaction. When id is 0 the manager assigns the next
// positive identifier; otherwise id must be unique among live transactions.
func (m *Manager) Begin(id uint64) *Transaction {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == 0 {
		m.nextTxnID++
		id = m.nextTxnID
	}
	if _, exists := m.txns[id]; exists {
		panic("lockmgr: duplicate transaction id")
	}
	if id > m.nextTxnID {
		m.nextTxnID = id
	}
	txn := &Transaction{
		id:     id,
		start:  m.nextSeq(),
		active: true,
		locks:  make(map[string]*grantedLock),
	}
	m.txns[id] = txn
	return txn
}

// lockStep is one (resource, mode) pair of a multi-resource Lock call.
type lockStep struct {
	resource Resource
	mode     LockMode
	upgrade  bool
}

// Lock requests mode on resource r for transaction t, blocking until granted.
// It first walks the ancestor chain top-down, acquiring or escalating the
// required intention locks, then locks r. Any blocked step enters the normal
// per-resource queue and participates in waiting-for graph deadlock
// detection; a victim receives ErrDeadlockRollback.
func (m *Manager) Lock(t *Transaction, r Resource, mode LockMode) error {
	if err := r.validate(); err != nil {
		return err
	}

	m.mu.Lock()
	if !t.active {
		m.mu.Unlock()
		return ErrTxnNotActive
	}
	if t.pending != nil {
		m.mu.Unlock()
		return ErrTxnBusy
	}

	steps, err := m.planSteps(t, r, mode)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	// All-or-nothing pre-check: every step must be grantable right now,
	// otherwise the request blocks without taking any ancestor escalation.
	for _, step := range steps {
		st := m.state(step.resource)
		if held, ok := st.holders[t.id]; ok && held.mode.covers(step.mode) {
			continue
		}
		if len(st.queue) != 0 || !m.compatibleWithHolders(st, t.id, step.mode) {
			w := m.enqueue(st, t, step.mode, st.holders[t.id] != nil)
			w.cont = remainingAfter(steps, step)
			m.resolveDeadlocks()
			m.mu.Unlock()
			return <-w.ch
		}
	}

	var blocked *waiter
	for i, step := range steps {
		st := m.state(step.resource)
		held, hasHeld := st.holders[t.id]

		// Re-entrant request: the transaction already covers this mode.
		if hasHeld && held.mode.covers(step.mode) {
			continue
		}
		step.upgrade = hasHeld

		if len(st.queue) == 0 && m.compatibleWithHolders(st, t.id, step.mode) {
			m.grant(st, t, step.mode)
			continue
		}
		blocked = m.enqueue(st, t, step.mode, step.upgrade)
		blocked.cont = steps[i+1:]
		break
	}

	if blocked == nil {
		m.mu.Unlock()
		return nil
	}

	// Enqueueing and arbitration are separate steps so that a cycle formed by
	// concurrent requests is observed once all of its edges are present.
	m.resolveDeadlocks()
	m.mu.Unlock()
	return <-blocked.ch
}

// TryLock attempts the full request without blocking. It is all-or-nothing:
// every ancestor escalation and the target lock must be immediately
// grantable, otherwise ErrIncompatible is returned without taking any lock.
func (m *Manager) TryLock(t *Transaction, r Resource, mode LockMode) error {
	if err := r.validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if !t.active {
		return ErrTxnNotActive
	}
	if t.pending != nil {
		return ErrTxnBusy
	}

	steps, err := m.planSteps(t, r, mode)
	if err != nil {
		return err
	}
	for _, step := range steps {
		st := m.state(step.resource)
		if held, ok := st.holders[t.id]; ok && held.mode.covers(step.mode) {
			continue
		}
		if len(st.queue) != 0 || !m.compatibleWithHolders(st, t.id, step.mode) {
			return ErrIncompatible
		}
	}
	for _, step := range steps {
		st := m.state(step.resource)
		if held, ok := st.holders[t.id]; ok && held.mode.covers(step.mode) {
			continue
		}
		m.grant(st, t, step.mode)
	}
	return nil
}

// planSteps builds the ordered ancestor-first lock list of a request and
// validates the top-down intention prerequisite: every ancestor must already
// be held. An ancestor held in an insufficient mode contributes an escalation
// step rather than a missing-lock error.
func (m *Manager) planSteps(t *Transaction, r Resource, want LockMode) ([]lockStep, error) {
	need := minParentMode(want)
	steps := make([]lockStep, 0, len(r.ancestors())+1)
	for _, anc := range r.ancestors() {
		held := t.locks[anc.key()]
		if held == nil {
			return nil, ErrIntentionViolation
		}
		if !parentModeAllows(held.mode, need) {
			steps = append(steps, lockStep{resource: anc, mode: unionMode(held.mode, need), upgrade: true})
		}
	}
	steps = append(steps, lockStep{resource: r, mode: want})
	return steps, nil
}

// remainingAfter returns the steps that follow target in steps.
func remainingAfter(steps []lockStep, target lockStep) []lockStep {
	for i, step := range steps {
		if step.resource.key() == target.resource.key() {
			return append([]lockStep(nil), steps[i+1:]...)
		}
	}
	return nil
}

// Unlock releases the transaction's lock on r. Locks are normally released
// implicitly by Commit or Abort.
func (m *Manager) Unlock(t *Transaction, r Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.active {
		return ErrTxnNotActive
	}
	if t.pending != nil {
		return ErrTxnBusy
	}
	st, ok := m.resources[r.key()]
	if !ok {
		return nil
	}
	if _, held := st.holders[t.id]; !held {
		return nil
	}
	m.releaseHolder(st, t)
	m.pump(st)
	return nil
}

// Commit releases every lock held by t and marks the transaction finished.
func (m *Manager) Commit(t *Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.active {
		return ErrTxnNotActive
	}
	m.finishTransaction(t, nil, false)
	return nil
}

// Abort cancels any pending request, releases every lock and marks the
// transaction finished.
func (m *Manager) Abort(t *Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.active {
		return ErrTxnNotActive
	}
	m.finishTransaction(t, ErrTxnNotActive, true)
	return nil
}

// parentModeAllows reports whether a parent lock of mode parent permits a
// child request whose minimum ancestor requirement is need.
func parentModeAllows(parent, need LockMode) bool {
	switch need {
	case IS:
		// Any real lock mode on the parent suffices.
		return parent != None
	case IX:
		return parent == IX || parent == SIX || parent == X
	default:
		return false
	}
}

func (m *Manager) state(r Resource) *resourceState {
	st, ok := m.resources[r.key()]
	if !ok {
		st = newResourceState(r)
		m.resources[r.key()] = st
	}
	return st
}

// compatibleWithHolders reports whether mode can be granted while other
// transactions keep their current locks on the resource.
func (m *Manager) compatibleWithHolders(st *resourceState, self uint64, mode LockMode) bool {
	for id, holder := range st.holders {
		if id == self {
			continue
		}
		if !compatible(holder.mode, mode) {
			return false
		}
	}
	return true
}

func (m *Manager) canGrant(st *resourceState, w *waiter) bool {
	return m.compatibleWithHolders(st, w.txn.id, w.want)
}

// grant records a newly granted (or upgraded) lock. The caller guarantees the
// grant is compatible with every other holder.
func (m *Manager) grant(st *resourceState, t *Transaction, mode LockMode) {
	seq := m.nextSeq()
	if held, ok := st.holders[t.id]; ok {
		prev := held.mode
		held.mode = unionMode(prev, mode)
		held.history = append(held.history, modeStep{from: prev, to: held.mode, seq: seq})
		t.locks[st.resource.key()] = held
		return
	}
	gl := &grantedLock{
		resource: st.resource,
		mode:     mode,
		history:  []modeStep{{from: None, to: mode, seq: seq}},
	}
	st.holders[t.id] = gl
	t.locks[st.resource.key()] = gl
}

func (m *Manager) enqueue(st *resourceState, t *Transaction, mode LockMode, upgrade bool) *waiter {
	w := &waiter{
		txn:      t,
		resource: st.resource,
		want:     mode,
		upgrade:  upgrade,
		ch:       make(chan error, 1),
	}
	t.pending = w
	if upgrade {
		// Upgrade waiters jump ahead of ordinary new waiters but preserve
		// FIFO order among themselves.
		idx := 0
		for idx < len(st.queue) && st.queue[idx].upgrade {
			idx++
		}
		st.queue = append(st.queue, nil)
		copy(st.queue[idx+1:], st.queue[idx:])
		st.queue[idx] = w
	} else {
		st.queue = append(st.queue, w)
	}
	return w
}

// pump grants every head waiter that is currently compatible, in strict queue
// order, stopping at the first waiter that must keep waiting. When a blocked
// multi-resource request reaches its front, the remaining steps of the same
// Lock call are resumed here as well.
func (m *Manager) pump(st *resourceState) {
	for len(st.queue) > 0 {
		w := st.queue[0]
		if !w.txn.active {
			st.queue = st.queue[1:]
			continue
		}
		if !m.canGrant(st, w) {
			return
		}
		st.queue = st.queue[1:]
		w.txn.pending = nil
		m.grant(st, w.txn, w.want)

		if w.cont == nil {
			w.ch <- nil
			continue
		}

		// Resume the remaining steps of a multi-resource request.
		m.resumeSteps(w)
		w.ch <- nil
	}
}

// resumeSteps processes the remaining steps of a multi-resource request once
// its current step has been granted. A blocking later step is enqueued on its
// own resource; its queue will be pumped separately by the caller.
func (m *Manager) resumeSteps(prev *waiter) {
	t := prev.txn
	remaining := prev.cont
	for len(remaining) > 0 {
		step := remaining[0]
		remaining = remaining[1:]
		st := m.state(step.resource)
		if held, ok := st.holders[t.id]; ok && held.mode.covers(step.mode) {
			continue
		}
		step.upgrade = st.holders[t.id] != nil
		if len(st.queue) == 0 && m.compatibleWithHolders(st, t.id, step.mode) {
			m.grant(st, t, step.mode)
			continue
		}
		next := m.enqueue(st, t, step.mode, step.upgrade)
		next.ch = prev.ch
		next.cont = remaining
		return
	}
}

// pumpKeyed repumps the resources identified by keys, skipping deleted ones.
func (m *Manager) pumpKeyed(keys []string) {
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		if st, ok := m.resources[key]; ok {
			m.pump(st)
		}
	}
}

// releaseHolder removes t's lock from st and from the transaction bookkeeping.
func (m *Manager) releaseHolder(st *resourceState, t *Transaction) {
	delete(st.holders, t.id)
	delete(t.locks, st.resource.key())
}

// finishTransaction implements Commit and Abort. reason is sent to a pending
// waiter on abort; a nil reason means a normal commit.
func (m *Manager) finishTransaction(t *Transaction, reason error, aborted bool) {
	affected := make([]string, 0, len(t.locks))
	for key := range t.locks {
		affected = append(affected, key)
	}

	if t.pending != nil {
		w := t.pending
		if st, ok := m.resources[w.resource.key()]; ok {
			m.removeWaiter(st, w)
			m.pump(st)
		}
		t.pending = nil
		if reason != nil {
			w.ch <- reason
		}
	}

	for _, key := range affected {
		if st, ok := m.resources[key]; ok {
			m.releaseHolder(st, t)
		}
	}
	t.locks = map[string]*grantedLock{}
	t.savepoints = nil
	t.active = false
	t.aborted = aborted
	m.pumpKeyed(affected)
	delete(m.txns, t.id)
}

func (m *Manager) removeWaiter(st *resourceState, target *waiter) {
	for i, w := range st.queue {
		if w == target {
			st.queue = append(st.queue[:i], st.queue[i+1:]...)
			return
		}
	}
}
