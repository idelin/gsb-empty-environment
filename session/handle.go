package session

import "sync"

// ConnHandle is the exclusive per-connection control block over a [Session].
// A handle becomes stale the moment another CONNECT takes the session over.
type ConnHandle struct {
	session *Session
	conn    Connection

	// generation snapshot from when the handle was installed.
	generation uint64

	handleMu sync.Mutex
	closed   bool

	takeover chan struct{}
	once     sync.Once
}

func newConnHandle(sess *Session, conn Connection) *ConnHandle {
	return &ConnHandle{
		session:    sess,
		conn:       conn,
		generation: sess.generation,
		takeover:   make(chan struct{}),
	}
}

// Session returns the session this handle operates on.
func (h *ConnHandle) Session() *Session {
	return h.session
}

// TakenOver blocks until the connection has been superseded by a newer
// CONNECT. A connection read loop can select on it to stop processing packets.
func (h *ConnHandle) TakenOver() <-chan struct{} {
	return h.takeover
}

// IsCurrent reports whether the handle still owns its session. A false result
// means another CONNECT has taken over and all local state is invalid.
func (h *ConnHandle) IsCurrent() bool {
	sess := h.session
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.handle == h && sess.generation == h.generation && !h.isClosed()
}

func (h *ConnHandle) isClosed() bool {
	h.handleMu.Lock()
	defer h.handleMu.Unlock()
	return h.closed
}

// markSupersededLocked marks the handle invalid while the session lock is
// held. When requeue is true, unacknowledged outgoing PUBLISHes return to the
// delivery queue for the new owner; on full session discard they are dropped.
// The actual network notification happens later, outside the lock.
func (h *ConnHandle) markSupersededLocked(requeue bool) {
	h.handleMu.Lock()
	h.closed = true
	h.handleMu.Unlock()

	if requeue {
		// In-flight outgoing PUBLISHes on the dying socket can never be
		// acknowledged reliably. Requeue them so the new connection redelivers.
		h.session.inflight = h.requeueInFlightLocked(h.session.inflight)
	}
}

// requeueInFlightLocked moves this handle's unacknowledged PUBLISHes back to
// the front of the delivery queue, preserving their original relative order.
func (h *ConnHandle) requeueInFlightLocked(inflight []InFlightPublish) []InFlightPublish {
	requeued := make([]QueuedMessage, 0, len(inflight))
	for _, inf := range inflight {
		requeued = append(requeued, QueuedMessage{Message: inf.Message})
	}
	h.session.queue = append(requeued, h.session.queue...)
	return nil
}

// notifyTakenOver delivers the exactly-once Session Taken Over notification.
// It is called after the store/session locks have been released.
func (h *ConnHandle) notifyTakenOver() {
	h.once.Do(func() {
		close(h.takeover)
		if h.conn != nil {
			h.conn.SessionTakenOver()
		}
	})
}

// MarkSending records that a QoS 1/2 PUBLISH with packetID has been handed to
// the socket and is now awaiting PUBACK/PUBREC.
func (h *ConnHandle) MarkSending(packetID uint16, msg Message) error {
	sess := h.session
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !h.isCurrentLocked() {
		return ErrStaleHandle
	}
	sess.inflight = append(sess.inflight, InFlightPublish{PacketID: packetID, Message: msg})
	return nil
}

// AckPublish consumes the PUBACK/PUBREC for packetID. It returns
// [ErrStaleHandle] when the connection has been taken over: the broker must
// drop such late acknowledgements because the message may already have been
// redelivered on the new connection.
func (h *ConnHandle) AckPublish(packetID uint16) error {
	sess := h.session
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !h.isCurrentLocked() {
		return ErrStaleHandle
	}
	kept := sess.inflight[:0]
	found := false
	for _, inf := range sess.inflight {
		if !found && inf.PacketID == packetID {
			found = true
			continue
		}
		kept = append(kept, inf)
	}
	sess.inflight = kept
	if !found {
		return ErrUnknownPacketID
	}
	return nil
}

// NextPending removes and returns the next queued message, or false when the
// queue is empty. The caller then sends it and calls [ConnHandle.MarkSending].
func (h *ConnHandle) NextPending() (QueuedMessage, bool) {
	sess := h.session
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !h.isCurrentLocked() {
		return QueuedMessage{}, false
	}
	if len(sess.queue) == 0 {
		return QueuedMessage{}, false
	}
	msg := sess.queue[0]
	sess.queue = sess.queue[1:]
	return msg, true
}

// isCurrentLocked reports ownership while the session lock is held.
func (h *ConnHandle) isCurrentLocked() bool {
	return h.session.handle == h && h.session.generation == h.generation && !h.isClosed()
}

// Detach releases the connection while retaining the persistent session.
// Unacknowledged outgoing PUBLISHes return to the queue for the next
// connection. It is safe to call after a takeover: it then does nothing.
func (h *ConnHandle) Detach() {
	sess := h.session
	sess.mu.Lock()
	defer sess.mu.Unlock()

	h.handleMu.Lock()
	alreadyClosed := h.closed
	h.closed = true
	h.handleMu.Unlock()

	if alreadyClosed || sess.handle != h {
		return
	}

	sess.inflight = h.requeueInFlightLocked(sess.inflight)
	sess.handle = nil
	sess.generation++
}

// Terminate removes the whole session from the store, used when a Clean Start
// connection disconnects. A superseded handle cannot delete the session that
// now belongs to another connection.
func (h *ConnHandle) Terminate() {
	store := h.session.store
	sess := h.session

	store.mu.Lock()
	defer store.mu.Unlock()
	sess.mu.Lock()
	defer sess.mu.Unlock()

	h.handleMu.Lock()
	h.closed = true
	h.handleMu.Unlock()

	if sess.handle == h {
		store.remove(sess)
		sess.handle = nil
		sess.generation++
	}
}
