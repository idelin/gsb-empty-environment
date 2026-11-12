package netsec

import "context"

// errorEnvelope synthesizes a terminal error message for an operation, used
// when its connection is poisoned or its reader exits.
type errorEnvelope struct {
	opID int
	err  error
}

func (e *errorEnvelope) OpID() int        { return e.opID }
func (e *errorEnvelope) IsTerminal() bool { return true }
func (e *errorEnvelope) Entry() *Entry    { return nil }
func (e *errorEnvelope) Err() error       { return e.err }

// readLoop is the sole goroutine reading from a physical connection. Every
// envelope is validated against the generation/lease tags of the op it
// addresses before anything is delivered to a cursor.
func (m *Manager) readLoop(mc *managedConn) {
	defer close(mc.readerDone)
	for {
		env, err := mc.conn.Recv(context.Background())
		if err != nil {
			m.handleReaderExit(mc, err)
			return
		}
		m.route(mc, env)
	}
}

// handleReaderExit is invoked when Recv fails. A live connection with
// registered operations is poisoned; a connection already replaced is just
// retired.
func (m *Manager) handleReaderExit(mc *managedConn, recvErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If the connection was already replaced, this is a stale reader and
	// must not touch the new connection.
	if m.current != mc {
		return
	}
	// If poisonLocked already marked this connection dead and failed its
	// ops, we still must clear the reference so ensureConn dials a fresh
	// connection instead of reusing this dead one.
	if !mc.dead {
		mc.dead = true
		close(mc.deadCh)
	}
	alreadyHandled := len(mc.ops) == 0
	if !alreadyHandled {
		m.stats.ConnectionsPoisoned++
		for id, operation := range mc.ops {
			env := &errorEnvelope{opID: id, err: recvErr}
			m.deliverLocked(operation, env)
			delete(mc.ops, id)
			if !operation.terminal {
				operation.terminal = true
				close(operation.done)
			}
		}
		m.signalDrainLocked()
	}
	m.current = nil
}

// route applies generation/lease isolation to one inbound envelope. Nothing
// reaches a cursor unless both the connection generation and the tenant
// lease recorded when the op was created still match the active binding.
func (m *Manager) route(mc *managedConn, env Envelope) {
	m.mu.Lock()

	// The connection has been replaced or poisoned while a message was in
	// flight. Nothing on it belongs to a live lease.
	if m.current != mc || mc.dead {
		m.stats.LateOrDropped++
		m.mu.Unlock()
		return
	}

	operation, ok := mc.ops[env.OpID()]
	if !ok {
		// Orphaned response: no registered operation under the current
		// generation. Never deliver; it cannot be attributed safely.
		m.stats.LateOrDropped++
		m.mu.Unlock()
		return
	}

	// Cross-generation / cross-lease guard. Both tags must match the
	// binding that created the operation.
	if operation.gen != mc.gen || operation.gen != m.gen ||
		operation.leaseID != m.leaseID {
		m.stats.LateOrDropped++
		if env.IsTerminal() {
			m.finishOpLocked(mc, operation, env)
		}
		m.mu.Unlock()
		return
	}

	if env.IsTerminal() {
		m.finishOpLocked(mc, operation, env)
		m.mu.Unlock()
		return
	}

	if operation.canceled {
		// Caller abandoned the search. Drain entries silently; the
		// terminal response is still required before reuse.
		if env.Entry() != nil {
			m.stats.CanceledEntriesDrained++
		}
		m.mu.Unlock()
		return
	}

	if env.Entry() != nil {
		m.stats.EntriesDelivered++
	}
	m.deliverLocked(operation, env)
	m.mu.Unlock()
}

// finishOpLocked delivers the terminal envelope, unregisters the op and
// marks it drained. Caller holds m.mu.
func (m *Manager) finishOpLocked(mc *managedConn, operation *op, env Envelope) {
	if operation.terminal {
		return
	}
	m.deliverLocked(operation, env)
	delete(mc.ops, operation.id)
	operation.terminal = true
	close(operation.done)
	m.signalDrainLocked()
}

// deliverLocked hands an envelope to the cursor without blocking the reader
// indefinitely. Terminals are always delivered (the buffer is sized for at
// least one terminal); non-terminal messages for a cancelled op with a full
// buffer are discarded, since the cursor will never read them.
func (m *Manager) deliverLocked(operation *op, env Envelope) {
	if operation.canceled && !env.IsTerminal() {
		select {
		case operation.msgCh <- env:
		default:
		}
		return
	}
	select {
	case operation.msgCh <- env:
	default:
		// Only reachable for non-terminal messages when the consumer has
		// stopped draining. Such an op must be treated as abandoned.
		operation.canceled = true
		if env.Entry() != nil {
			m.stats.CanceledEntriesDrained++
		}
	}
}
