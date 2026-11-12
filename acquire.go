package netsec

import (
	"context"
	"fmt"
	"time"
)

// Acquire (re)binds the pooled connection for the requested tenant and
// returns a lease scoped to the resulting binding.
//
// Acquire is exclusive: concurrent searches block until the bind, its drain
// of prior queries, and its post-bind identity confirmation all complete.
func (m *Manager) Acquire(ctx context.Context, req BindRequest) (Lease, error) {
	// Anonymous-bind downgrade guard. A non-empty DN with an empty password
	// is an anonymous bind per RFC 4513 section 5.1.2; refuse before any
	// network traffic, so even a server "success" cannot authorize it.
	if req.Username != "" && req.Password == "" {
		m.mu.Lock()
		m.stats.AnonymousRefused++
		m.mu.Unlock()
		return nil, ErrEmptyPassword
	}

	if err := m.beginExclusive(ctx); err != nil {
		return nil, err
	}
	defer m.endExclusive()

	m.mu.Lock()
	m.stats.BindsAttempted++
	m.mu.Unlock()

	if err := m.ensureConn(ctx); err != nil {
		return nil, err
	}

	mc := m.current
	if err := mc.conn.Bind(ctx, req.Username, req.Password); err != nil {
		m.mu.Lock()
		m.stats.BindsFailed++
		readerDone := m.poisonLocked()
		m.mu.Unlock()
		if readerDone != nil {
			<-readerDone
		}
		return nil, fmt.Errorf("%w: %v", ErrBindFailed, err)
	}

	// Defense in depth: a successful protocol bind with an empty password
	// for a named principal must never be treated as authenticated.
	if req.Username != "" && req.Password == "" {
		m.mu.Lock()
		m.stats.AnonymousRefused++
		readerDone := m.poisonLocked()
		m.mu.Unlock()
		if readerDone != nil {
			<-readerDone
		}
		return nil, ErrEmptyPassword
	}

	// Confirm the actual bound principal through the injected callback.
	principal, err := m.confirm.Confirm(ctx, mc.conn)
	if err != nil {
		m.mu.Lock()
		m.stats.IdentityRejected++
		m.stats.BindsFailed++
		readerDone := m.poisonLocked()
		m.mu.Unlock()
		if readerDone != nil {
			<-readerDone
		}
		return nil, fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
	}
	if req.Username != "" && principal == "" {
		m.mu.Lock()
		m.stats.IdentityRejected++
		m.stats.BindsFailed++
		readerDone := m.poisonLocked()
		m.mu.Unlock()
		if readerDone != nil {
			<-readerDone
		}
		return nil, ErrNoIdentity
	}
	if req.Username != "" && principal != req.Username {
		m.mu.Lock()
		m.stats.IdentityRejected++
		m.stats.BindsFailed++
		readerDone := m.poisonLocked()
		m.mu.Unlock()
		if readerDone != nil {
			<-readerDone
		}
		return nil, fmt.Errorf("%w: confirmed %q, requested %q",
			ErrIdentityMismatch, principal, req.Username)
	}

	m.mu.Lock()
	m.gen++
	m.leaseID++
	m.current.gen = m.gen
	issued := &lease{
		tenant:  req.Tenant,
		gen:     m.gen,
		leaseID: m.leaseID,
	}
	m.tenant = req.Tenant
	m.stats.BindsSucceeded++
	m.mu.Unlock()

	return issued, nil
}

// beginExclusive drains all in-flight searches and then holds the
// connection for a bind. Searches started while binding block in
// waitWhileBinding.
func (m *Manager) beginExclusive(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	if m.binding {
		wait := m.bindWait
		m.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
		m.mu.Lock()
	}

	m.binding = true
	m.bindWait = make(chan struct{})

	// Cancel every outstanding search and remember how many terminal
	// responses are still owed. Entry messages for cancelled operations are
	// drained by the reader; the connection is only released once every
	// terminal response arrives (or the drain deadline forces a poison).
	pending := 0
	if m.current != nil {
		for _, operation := range m.current.ops {
			operation.canceled = true
			pending++
		}
	}
	m.mu.Unlock()

	if pending == 0 {
		return nil
	}

	deadline := time.NewTimer(m.drainTO)
	defer deadline.Stop()

	// Poll the in-flight count under the lock. Reader goroutines decrement
	// it in finishOpLocked/handleReaderExit, and broadcast via drainSig.
	ticker := time.NewTimer(0)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		remaining := m.inflightCountLocked()
		m.mu.Unlock()

		// Drain completes exactly when no registered operations remain.
		// Poisoning removes them synchronously, so this also unblocks the
		// timeout/cancellation branches.
		if remaining == 0 {
			return nil
		}

		m.mu.Lock()
		sig := m.drainSig
		m.mu.Unlock()

		ticker.Reset(2 * time.Millisecond)
		select {
		case <-sig:
		case <-ticker.C:
		case <-deadline.C:
			m.poisonCurrent()
			m.abortExclusive()
			return ErrDrainTimeout
		case <-ctx.Done():
			m.poisonCurrent()
			m.abortExclusive()
			return ctx.Err()
		}
	}
}

// abortExclusive releases the bind lock after a failed drain so that the
// binding flag never leaks and subsequent Acquire/Search calls can proceed.
func (m *Manager) abortExclusive() {
	m.mu.Lock()
	m.binding = false
	ch := m.bindWait
	m.bindWait = make(chan struct{})
	m.mu.Unlock()
	close(ch)
}

// endExclusive releases the bind lock and wakes any blocked searches.
func (m *Manager) endExclusive() {
	m.mu.Lock()
	m.binding = false
	ch := m.bindWait
	m.bindWait = make(chan struct{})
	m.mu.Unlock()
	close(ch)
}

// inflightCountLocked reports the number of registered operations on the
// current connection. The caller must hold m.mu.
func (m *Manager) inflightCountLocked() int {
	if m.current == nil {
		return 0
	}
	return len(m.current.ops)
}

// ensureConn dials and starts a reader when no usable connection exists.
func (m *Manager) ensureConn(ctx context.Context) error {
	m.mu.Lock()
	if m.current != nil && !m.current.dead {
		m.mu.Unlock()
		return nil
	}
	dial := m.dial
	m.mu.Unlock()

	c, err := dial(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConnUnavailable, err)
	}

	mc := &managedConn{
		deadCh:     make(chan struct{}),
		readerDone: make(chan struct{}),
		nextOpID:   1,
		ops:        make(map[int]*op),
	}

	m.mu.Lock()
	m.stats.ConnectionsDialed++
	// A replacement physical connection always starts a fresh generation so
	// no stale lease can ever address it.
	m.gen++
	mc.gen = m.gen
	mc.readerID = m.gen
	mc.conn = c
	m.current = mc
	// Re-arm the drain signal for the new connection.
	m.drainSig = make(chan struct{})
	m.mu.Unlock()

	go m.readLoop(mc)
	return nil
}

// poisonCurrent marks the active connection unusable and tears it down.
func (m *Manager) poisonCurrent() {
	m.mu.Lock()
	readerDone := m.poisonLocked()
	m.mu.Unlock()
	if readerDone != nil {
		<-readerDone
	}
}

// poisonLocked closes and discards the current connection, failing any
// operations still registered on it. It returns the reader's completion
// channel (if a reader was active) which the caller must wait on outside
// the lock before dialing a replacement. The caller must hold m.mu.
func (m *Manager) poisonLocked() chan struct{} {
	if m.current == nil {
		return nil
	}
	mc := m.current
	if !mc.dead {
		mc.dead = true
		close(mc.deadCh)
		_ = mc.conn.Close()
	}
	m.stats.ConnectionsPoisoned++

	for id, operation := range mc.ops {
		env := &errorEnvelope{opID: id, err: ErrConnUnavailable}
		m.deliverLocked(operation, env)
		delete(mc.ops, id)
		if !operation.terminal {
			operation.terminal = true
			close(operation.done)
		}
	}
	m.current = nil
	m.signalDrainLocked()
	return mc.readerDone
}

// signalDrainLocked wakes a waiting bind. The drain channel is replaced on
// every broadcast so each wait observes a fresh signal.
func (m *Manager) signalDrainLocked() {
	if m.drainSig != nil {
		close(m.drainSig)
	}
	m.drainSig = make(chan struct{})
}
