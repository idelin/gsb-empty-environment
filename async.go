package netsec

import "sync"

// CryptoTask pins one asynchronous cryptographic operation to a binding. The
// task stores the binding pointer and opaque ID, never a numeric index. If the
// index is reused while the task is outstanding, finishing the task can only
// release or destroy the old binding's material.
type CryptoTask struct {
	manager   *Manager
	binding   *Binding
	bindingID BindingID
	sequence  uint64

	mu       sync.Mutex
	finished bool
}

// StartCryptoTask registers asynchronous work against a live binding.
func (m *Manager) StartCryptoTask(b *Binding) (*CryptoTask, error) {
	if b == nil || b.mgr != m {
		return nil, ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if _, ok := m.bindings[b.id]; !ok {
		return nil, ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reclaimed {
		return nil, ErrBindingReclaimed
	}
	if b.state == StateRetired || b.state == StateQuarantine {
		return nil, ErrStaleBinding
	}
	b.asyncTasks++
	b.asyncSequence++
	return &CryptoTask{
		manager:   m,
		binding:   b,
		bindingID: b.id,
		sequence:  b.asyncSequence,
	}, nil
}

// Binding returns the exact binding captured by the task.
func (t *CryptoTask) Binding() *Binding {
	if t == nil {
		return nil
	}
	return t.binding
}

// BindingID returns the opaque capability handle captured by the task.
func (t *CryptoTask) BindingID() BindingID {
	if t == nil {
		return BindingID{}
	}
	return t.bindingID
}

// Sequence is a per-binding task sequence number for diagnostics.
func (t *CryptoTask) Sequence() uint64 {
	if t == nil {
		return 0
	}
	return t.sequence
}

// Finished reports whether Done has completed successfully.
func (t *CryptoTask) Finished() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.finished
}

// Done releases the asynchronous-work gate. It is idempotent. If this was the
// last task for a retired binding, temporary key material is destroyed on the
// old binding only; no index lookup occurs.
func (t *CryptoTask) Done() bool {
	if t == nil || t.manager == nil || t.binding == nil {
		return false
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return false
	}
	t.finished = true
	t.mu.Unlock()

	m := t.manager
	m.mu.Lock()
	defer m.mu.Unlock()

	b := t.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.id != t.bindingID || b.mgr != m {
		return false
	}
	if b.asyncTasks > 0 {
		b.asyncTasks--
	}
	if b.asyncTasks == 0 && (b.state == StateRetired || b.state == StateQuarantine) {
		b.destroyMaterialsLocked()
	}
	return true
}
