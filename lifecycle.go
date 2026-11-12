package netsec

// Stats returns a point-in-time snapshot of lease-layer counters.
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// CurrentGeneration returns the active connection generation. It is mainly
// useful for diagnostics and tests.
func (m *Manager) CurrentGeneration() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen
}

// Close tears down the pooled connection and rejects further operations. It
// blocks until the connection's reader goroutine has exited, guaranteeing no
// stale Recv consumer survives to touch a later connection.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true

	var (
		conn       Conn
		readerDone chan struct{}
	)
	if m.current != nil {
		mc := m.current
		if !mc.dead {
			mc.dead = true
			close(mc.deadCh)
		}
		conn = mc.conn
		readerDone = mc.readerDone
		for id, operation := range mc.ops {
			env := &errorEnvelope{opID: id, err: ErrManagerClosed}
			m.deliverLocked(operation, env)
			delete(mc.ops, id)
			if !operation.terminal {
				operation.terminal = true
				close(operation.done)
			}
		}
		m.current = nil
		m.signalDrainLocked()
	}
	ch := m.bindWait
	m.bindWait = make(chan struct{})
	m.mu.Unlock()
	close(ch)

	var err error
	if conn != nil {
		err = conn.Close()
	}
	if readerDone != nil {
		<-readerDone
	}
	return err
}
