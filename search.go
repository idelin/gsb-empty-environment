package netsec

import (
	"context"
	"errors"
	"sync"
)

// opMsgBuffer bounds how many entry messages the reader can queue for a
// cursor before the cursor must consume them.
const opMsgBuffer = 32

// Search starts a directory search under the given lease. The returned
// cursor only ever yields entries whose generation and lease match the
// active binding at call time.
func (m *Manager) Search(ctx context.Context, l Lease, req SearchRequest) (Cursor, error) {
	if err := m.waitWhileBinding(ctx); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	if m.current == nil || m.current.dead {
		m.mu.Unlock()
		return nil, ErrConnUnavailable
	}

	// Lease validation: a revoked or foreign lease may not touch the
	// current connection generation.
	if l == nil || l.Generation() != m.gen || l.LeaseID() != m.leaseID ||
		l.Tenant().ID != m.tenant.ID {
		m.mu.Unlock()
		return nil, ErrLeaseInvalid
	}
	if req.Tenant.ID != "" && req.Tenant.ID != m.tenant.ID {
		m.mu.Unlock()
		return nil, ErrLeaseInvalid
	}

	mc := m.current
	opID := mc.nextOpID
	mc.nextOpID++
	operation := &op{
		id:       opID,
		gen:      mc.gen,
		leaseID:  m.leaseID,
		tenantID: m.tenant.ID,
		msgCh:    make(chan Envelope, opMsgBuffer),
		done:     make(chan struct{}),
	}
	mc.ops[opID] = operation
	m.stats.SearchesStarted++
	conn := mc.conn
	m.mu.Unlock()

	assigned, err := conn.Send(ctx, req)
	if err != nil {
		m.mu.Lock()
		if existing, ok := m.currentOpLocked(opID); ok && existing == operation {
			delete(m.current.ops, opID)
			operation.terminal = true
			close(operation.done)
			m.signalDrainLocked()
		}
		m.mu.Unlock()
		return nil, err
	}
	if assigned != opID {
		// The connection must echo the id the manager assigned; otherwise
		// response attribution cannot be guaranteed.
		m.mu.Lock()
		if existing, ok := m.currentOpLocked(opID); ok && existing == operation {
			delete(m.current.ops, opID)
			operation.terminal = true
			close(operation.done)
			m.signalDrainLocked()
		}
		m.mu.Unlock()
		return nil, errors.New("netsec: directory returned unexpected operation id")
	}

	return &cursor{manager: m, op: operation}, nil
}

// currentOpLocked looks up an op by id on the active connection.
func (m *Manager) currentOpLocked(id int) (*op, bool) {
	if m.current == nil {
		return nil, false
	}
	operation, ok := m.current.ops[id]
	return operation, ok
}

// waitWhileBinding blocks while a bind owns the connection, honoring ctx.
func (m *Manager) waitWhileBinding(ctx context.Context) error {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return ErrManagerClosed
		}
		if !m.binding {
			m.mu.Unlock()
			return nil
		}
		wait := m.bindWait
		m.mu.Unlock()

		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// cursor is the Cursor implementation backed by an op's message channel.
// cursor is the Cursor implementation backed by an op's message channel.
type cursor struct {
	manager *Manager
	op      *op

	mu       sync.Mutex
	closed   bool
	terminal bool
	lastErr  error
}

// OpID returns the directory protocol request id of this search.
func (c *cursor) OpID() int { return c.op.id }

// Next returns the next entry, or false at the terminal response, on
// context cancellation, or when the lease/connection is lost.
func (c *cursor) Next(ctx context.Context) (*Entry, bool) {
	for {
		// Prefer any queued message, including entries buffered ahead of
		// the terminal response. A closed done-channel must never cut off
		// entries already delivered to this cursor.
		select {
		case env := <-c.op.msgCh:
			if c.handle(env) {
				return nil, false
			}
			if entry := env.Entry(); entry != nil {
				return entry, true
			}
			continue
		default:
		}

		select {
		case env := <-c.op.msgCh:
			if c.handle(env) {
				return nil, false
			}
			if entry := env.Entry(); entry != nil {
				return entry, true
			}
			continue
		case <-ctx.Done():
			c.cancel()
			c.mu.Lock()
			c.lastErr = ctx.Err()
			c.mu.Unlock()
			return nil, false
		case <-c.op.done:
			// Connection-failure safety net: drain a buffered message
			// first; otherwise the finished op is terminal.
			select {
			case env := <-c.op.msgCh:
				if c.handle(env) {
					return nil, false
				}
				if entry := env.Entry(); entry != nil {
					return entry, true
				}
				continue
			default:
				c.mu.Lock()
				c.terminal = true
				c.mu.Unlock()
				return nil, false
			}
		}
	}
}

// handle records terminal state for an envelope and reports whether it
// terminates the cursor.
func (c *cursor) handle(env Envelope) bool {
	if env == nil {
		c.mu.Lock()
		c.terminal = true
		c.mu.Unlock()
		return true
	}
	if env.IsTerminal() {
		c.mu.Lock()
		c.terminal = true
		if env.Err() != nil {
			c.lastErr = env.Err()
		}
		c.mu.Unlock()
		return true
	}
	return false
}

// Err returns the terminal error, if any.
func (c *cursor) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// Close abandons the search. The op stays registered until its terminal
// response is observed and discarded; if that never happens, the bind drain
// deadline poisons the connection instead of reusing it.
func (c *cursor) Close() error {
	c.cancel()
	return nil
}

// cancel marks the op cancelled. Late entries are then drained by the
// reader and never delivered.
func (c *cursor) cancel() {
	c.manager.mu.Lock()
	defer c.manager.mu.Unlock()
	c.mu.Lock()
	already := c.closed
	c.closed = true
	c.mu.Unlock()
	if already {
		return
	}
	c.op.canceled = true
}
