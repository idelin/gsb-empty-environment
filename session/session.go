// Package session implements MQTT 5 session resumption for a broker's
// CONNECT handler. It binds each persisted session to the authenticated
// principal that created it, refuses cross-principal session inheritance,
// invalidates the old connection on same-principal takeover, and
// re-authorizes subscriptions and queued QoS 1 messages against the
// current ACL before they are delivered.
package session

import (
	"errors"
	"sync"
)

// Principal is the authenticated identity established during CONNECT
// (e.g. from the MQTT username, a client certificate, or a token).
type Principal string

// Message is a queued QoS 1 publish awaiting delivery.
type Message struct {
	Topic    string
	Payload  []byte
	PacketID uint16
	Retain   bool
}

// Subscription is a topic filter the session is subscribed to.
type Subscription struct {
	Filter string
	QoS    byte
}

// ACL authorizes subscribe and delivery decisions. It is consulted at
// resume time and again before every queued delivery, so revocations
// take effect even for sessions persisted earlier.
type ACL interface {
	// AllowSubscribe reports whether p may subscribe to filter.
	AllowSubscribe(p Principal, filter string) bool
	// AllowDeliver reports whether p may receive a message on topic.
	AllowDeliver(p Principal, topic string) bool
}

// ConnectRequest carries the fields the CONNECT handler has already
// authenticated and parsed.
type ConnectRequest struct {
	ClientID   string
	CleanStart bool
	Principal  Principal
}

// ConnectResult reports the outcome of a Connect call.
type ConnectResult struct {
	// SessionPresent is the CONNACK Session Present flag. It is true
	// only when an existing session owned by the same principal was
	// resumed.
	SessionPresent bool
	// TakenOver is true when this connect displaced a live connection.
	TakenOver bool
}

// ErrTakenOver is returned by in-flight operations on a connection that
// has been displaced by a newer connection for the same ClientID.
var ErrTakenOver = errors.New("session: connection taken over")

// Session is the broker-side persisted state for one ClientID.
type Session struct {
	ClientID string
	owner    Principal
	subs     []Subscription
	queue    []Message
}

// Owner returns the principal the session is bound to.
func (s *Session) Owner() Principal { return s.owner }

// Subscriptions returns a copy of the session's subscriptions.
func (s *Session) Subscriptions() []Subscription {
	out := make([]Subscription, len(s.subs))
	copy(out, s.subs)
	return out
}

// Queued returns the number of messages waiting for delivery.
func (s *Session) Queued() int { return len(s.queue) }

// Connection is one live network attachment to a session. A session has
// at most one current connection; a same-principal reconnect takes the
// session over and invalidates the previous connection.
type Connection struct {
	m           *Manager
	clientID    string
	epoch       uint64
	onTakenOver func()

	mu        sync.Mutex
	takenOver bool
	inflight  map[uint16]struct{}
}

// Manager owns all sessions. The CONNECT handler calls Connect; the
// rest of the broker interacts with the returned Connection.
type Manager struct {
	acl ACL

	mu       sync.Mutex
	sessions map[string]*Session
	current  map[string]*Connection
	epoch    uint64
}

// NewManager returns a Manager that authorizes with acl. A nil ACL
// denies nothing.
func NewManager(acl ACL) *Manager {
	return &Manager{
		acl:      acl,
		sessions: make(map[string]*Session),
		current:  make(map[string]*Connection),
	}
}

// Connect resolves the session for req. It is safe for concurrent use.
//
// Rules:
//   - Clean Start 1: any existing session is discarded.
//   - Existing session owned by a different principal: discarded, a
//     fresh session bound to req.Principal is created, Session Present
//     is 0. The attacker inherits nothing.
//   - Existing session owned by req.Principal: the old connection (if
//     any) is notified of the takeover and its in-flight publishes are
//     invalidated; subscriptions and queued QoS 1 messages are
//     re-authorized against the current ACL and revoked entries are
//     dropped; Session Present is 1.
func (m *Manager) Connect(req ConnectRequest) (*Connection, ConnectResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var res ConnectResult
	sess, ok := m.sessions[req.ClientID]

	if ok && (req.CleanStart || sess.owner != req.Principal) {
		// Clean start, or an authenticated principal that does not own
		// the persisted session: drop everything, inherit nothing.
		m.dropLocked(req.ClientID)
		ok = false
	}

	if !ok {
		sess = &Session{ClientID: req.ClientID, owner: req.Principal}
		m.sessions[req.ClientID] = sess
	} else {
		res.SessionPresent = true
		m.reauthorizeLocked(sess)
	}

	if old := m.current[req.ClientID]; old != nil {
		res.TakenOver = true
		m.epoch++
		old.invalidate(m.epoch)
	}

	m.epoch++
	conn := &Connection{
		m:        m,
		clientID: req.ClientID,
		epoch:    m.epoch,
		inflight: make(map[uint16]struct{}),
	}
	m.current[req.ClientID] = conn
	return conn, res
}

// OnTakenOver registers fn to be called at most once, when another
// connection takes over this session. If the takeover already happened,
// fn is invoked immediately.
func (c *Connection) OnTakenOver(fn func()) {
	c.mu.Lock()
	if c.takenOver {
		c.mu.Unlock()
		fn()
		return
	}
	c.onTakenOver = fn
	c.mu.Unlock()
}

// TakenOver reports whether this connection has been displaced.
func (c *Connection) TakenOver() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.takenOver
}

// BeginPublish registers packetID as an in-flight QoS 1 publish on this
// connection. It fails with ErrTakenOver once the session has been
// taken over, so a displaced connection can no longer inject publishes
// into the session.
func (c *Connection) BeginPublish(packetID uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.takenOver || !c.isCurrent() {
		return ErrTakenOver
	}
	c.inflight[packetID] = struct{}{}
	return nil
}

// CompletePublish resolves an in-flight publish. It fails with
// ErrTakenOver if the connection was displaced after BeginPublish, so
// the stale in-flight publish is dropped instead of committed.
func (c *Connection) CompletePublish(packetID uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.takenOver || !c.isCurrent() {
		return ErrTakenOver
	}
	delete(c.inflight, packetID)
	return nil
}

// InFlight reports how many publishes are unresolved on this connection.
func (c *Connection) InFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inflight)
}

// NextDelivery pops the next queued QoS 1 message that the session
// owner is still authorized to receive under the current ACL. Revoked
// messages are discarded silently. ok is false when nothing deliverable
// remains.
func (c *Connection) NextDelivery() (msg Message, ok bool, err error) {
	c.mu.Lock()
	if c.takenOver || !c.isCurrent() {
		c.mu.Unlock()
		return Message{}, false, ErrTakenOver
	}
	c.mu.Unlock()

	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	sess := c.m.sessions[c.clientID]
	if sess == nil {
		return Message{}, false, nil
	}
	for len(sess.queue) > 0 {
		head := sess.queue[0]
		sess.queue = sess.queue[1:]
		if c.m.acl == nil || c.m.acl.AllowDeliver(sess.owner, head.Topic) {
			return head, true, nil
		}
		// Authorization revoked since the message was queued: drop it.
	}
	return Message{}, false, nil
}

// Subscribe adds a subscription to the session if the owner is
// currently authorized for the filter.
func (c *Connection) Subscribe(sub Subscription) error {
	c.mu.Lock()
	if c.takenOver || !c.isCurrent() {
		c.mu.Unlock()
		return ErrTakenOver
	}
	c.mu.Unlock()

	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	sess := c.m.sessions[c.clientID]
	if sess == nil {
		return ErrTakenOver
	}
	if c.m.acl != nil && !c.m.acl.AllowSubscribe(sess.owner, sub.Filter) {
		return errors.New("session: subscribe not authorized")
	}
	sess.subs = append(sess.subs, sub)
	return nil
}

// Enqueue appends a QoS 1 message to the session's offline queue.
func (m *Manager) Enqueue(clientID string, msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[clientID]; ok {
		sess.queue = append(sess.queue, msg)
	}
}

// Session returns the persisted session for clientID, or nil.
func (m *Manager) Session(clientID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[clientID]
}

// dropLocked removes the session and disconnects its live connection.
func (m *Manager) dropLocked(clientID string) {
	delete(m.sessions, clientID)
	if old := m.current[clientID]; old != nil {
		m.epoch++
		old.invalidate(m.epoch)
		delete(m.current, clientID)
	}
}

// reauthorizeLocked drops subscriptions and queued messages the session
// owner is no longer allowed to hold under the current ACL.
func (m *Manager) reauthorizeLocked(sess *Session) {
	if m.acl == nil {
		return
	}
	subs := sess.subs[:0]
	for _, sub := range sess.subs {
		if m.acl.AllowSubscribe(sess.owner, sub.Filter) {
			subs = append(subs, sub)
		}
	}
	sess.subs = subs

	queue := sess.queue[:0]
	for _, msg := range sess.queue {
		if m.acl.AllowDeliver(sess.owner, msg.Topic) {
			queue = append(queue, msg)
		}
	}
	sess.queue = queue
}

// invalidate marks the connection displaced and notifies its listener.
func (c *Connection) invalidate(_ uint64) {
	c.mu.Lock()
	if c.takenOver {
		c.mu.Unlock()
		return
	}
	c.takenOver = true
	c.inflight = make(map[uint16]struct{})
	fn := c.onTakenOver
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// isCurrent reports whether this connection is still the session's
// live attachment. Callers must hold c.mu.
func (c *Connection) isCurrent() bool {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	return c.m.current[c.clientID] == c
}
