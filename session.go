// Package netsec implements the security-sensitive part of MQTT 5 session
// recovery.
//
// A session is bound to the authenticated principal that first created it.
// Resuming with the same ClientID and Clean Start 0 is allowed only for that
// principal. A different principal may reuse the ClientID, but it gets a new,
// empty session and Session Present is false. A same-principal connection
// takes the session over: the previous connection is told to disconnect with
// the MQTT "Session taken over" reason and its in-flight publishes are
// invalidated before the takeover notification is delivered.
//
// Every restored subscription and every queued QoS 1 message is checked
// against the current ACL. The checks are performed again immediately before
// each delivery from the restored queue, so a permission revoked after
// CONNECT is still enforced.
package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// ReasonSessionTakenOver is the MQTT 5 DISCONNECT reason code (0x8E) that a
// superseded connection must send. Connection implementations should use it in
// SessionTakenOver.
const ReasonSessionTakenOver byte = 0x8E

var (
	// ErrEmptyClientID is returned when a CONNECT request has no ClientID.
	ErrEmptyClientID = errors.New("netsec: empty client ID")
	// ErrEmptyPrincipal is returned when authentication did not produce a
	// principal.
	ErrEmptyPrincipal = errors.New("netsec: empty authenticated principal")
	// ErrInvalidSubscription is returned for an invalid topic filter or QoS.
	ErrInvalidSubscription = errors.New("netsec: invalid subscription")
	// ErrInvalidMessage is returned when a queued message is not a valid QoS 1
	// message.
	ErrInvalidMessage = errors.New("netsec: queued message must be QoS 1")
	// ErrNoSubscription is returned when a message does not match any live
	// subscription.
	ErrNoSubscription = errors.New("netsec: no subscription matches message topic")
	// ErrNotAuthorized is returned when the current ACL denies an operation.
	ErrNotAuthorized = errors.New("netsec: operation not authorized")
	// ErrNoSession is returned by Manager.Enqueue when no session exists.
	ErrNoSession = errors.New("netsec: no session for client ID")
	// ErrSessionGone is returned when the connection calling Recovery no
	// longer owns the session, for example after a takeover.
	ErrSessionGone = errors.New("netsec: connection no longer owns session")
)

// Connection is the broker-side view of a client connection. Both methods are
// invoked for a connection that loses a same-principal takeover race.
//
// InvalidateInflight is called first. The implementation must stop accepting
// acknowledgements or sending publishes that belong to the old session and
// must discard its in-flight publish state. SessionTakenOver is called second;
// the implementation must close the network connection after sending a
// DISCONNECT packet with ReasonSessionTakenOver.
//
// The Manager has already invalidated the connection's Recovery handle before
// either callback runs, so Recovery.Valid and Recovery.PublishAllowed return
// false for the old connection as well.
type Connection interface {
	InvalidateInflight()
	SessionTakenOver()
}

// ConnectionFuncs adapts functions to Connection. A nil function is a no-op.
type ConnectionFuncs struct {
	Invalidate func()
	TakenOver  func()
}

// InvalidateInflight implements Connection.
func (c ConnectionFuncs) InvalidateInflight() {
	if c.Invalidate != nil {
		c.Invalidate()
	}
}

// SessionTakenOver implements Connection.
func (c ConnectionFuncs) SessionTakenOver() {
	if c.TakenOver != nil {
		c.TakenOver()
	}
}

// Subscription is a restored or newly created session subscription.
type Subscription struct {
	Filter string
	QoS    byte
}

// QueuedMessage is a message waiting for delivery to a session. Only QoS 1
// messages are placed in a session queue by this package.
type QueuedMessage struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
}

// Delivery is a reauthorized queued message together with the subscription
// that caused it to be queued.
type Delivery struct {
	Message      QueuedMessage
	Subscription Subscription
}

// ConnectRequest is the session-relevant part of an MQTT 5 CONNECT packet.
type ConnectRequest struct {
	ClientID   string
	Principal  string
	CleanStart bool
}

type sessionState struct {
	clientID  string
	principal string
	subs      []Subscription
	queue     []QueuedMessage
	owner     *binding
	discarded bool
}

func newSessionState(clientID, principal string) *sessionState {
	return &sessionState{clientID: clientID, principal: principal}
}

func (s *sessionState) addSubscription(sub Subscription) {
	for i := range s.subs {
		if s.subs[i].Filter == sub.Filter {
			s.subs[i] = sub
			return
		}
	}
	s.subs = append(s.subs, sub)
}

func (s *sessionState) matchSubscription(topic string) (Subscription, bool) {
	for _, sub := range s.subs {
		if TopicMatches(sub.Filter, topic) {
			return sub, true
		}
	}
	return Subscription{}, false
}

type binding struct {
	id      uint64
	session *sessionState
	conn    Connection
	active  bool
}

// Manager owns all sessions for a broker. Its methods are safe for concurrent
// use.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	authz    Authorizer
	nextID   uint64
}

// NewManager returns an empty Manager. A nil Authorizer is treated as
// DenyAll.
func NewManager(authz Authorizer) *Manager {
	if authz == nil {
		authz = DenyAll
	}
	return &Manager{
		sessions: make(map[string]*sessionState),
		authz:    authz,
	}
}

// SetAuthorizer replaces the authorizer used for subsequent checks. It is
// useful when an ACL file is reloaded while sessions are active.
func (m *Manager) SetAuthorizer(authz Authorizer) {
	if authz == nil {
		authz = DenyAll
	}
	m.mu.Lock()
	m.authz = authz
	m.mu.Unlock()
}

func (m *Manager) initLocked() {
	if m.sessions == nil {
		m.sessions = make(map[string]*sessionState)
	}
	if m.authz == nil {
		m.authz = DenyAll
	}
}

// Recovery is the result of a successful CONNECT session negotiation. The
// broker must keep it for the lifetime of the connection. All returned data
// is a copy and can be used after the call without holding a lock.
type Recovery struct {
	manager        *Manager
	binding        *binding
	sessionPresent bool
}

// SessionPresent reports the MQTT 5 Session Present flag for CONNACK.
func (r *Recovery) SessionPresent() bool {
	if r == nil {
		return false
	}
	return r.sessionPresent
}

// ClientID returns the negotiated ClientID.
func (r *Recovery) ClientID() string {
	if r == nil || r.binding == nil {
		return ""
	}
	return r.binding.session.clientID
}

// Principal returns the authenticated principal bound to the session.
func (r *Recovery) Principal() string {
	if r == nil || r.binding == nil {
		return ""
	}
	return r.binding.session.principal
}

// Valid reports whether this connection still owns the session. It becomes
// false as soon as a same-principal connection takes over, or when the
// session is discarded because a different principal reused the ClientID.
//
// A broker must check Valid (or PublishAllowed) immediately before completing
// an in-flight publish.
func (r *Recovery) Valid() bool {
	if r == nil || r.manager == nil || r.binding == nil {
		return false
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	return r.validLocked()
}

// PublishAllowed is a semantic alias for Valid, intended for in-flight
// publish checks.
func (r *Recovery) PublishAllowed() bool {
	return r.Valid()
}

func (r *Recovery) validLocked() bool {
	return r.binding.active && !r.binding.session.discarded
}

// Subscriptions returns the session's subscriptions after applying the
// current ACL. Revoked subscriptions are removed from the session. The result
// is a copy.
func (r *Recovery) Subscriptions() []Subscription {
	if r == nil || r.manager == nil || r.binding == nil {
		return nil
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !r.validLocked() {
		return nil
	}
	m.reauthorizeLocked(r.binding.session)
	return append([]Subscription(nil), r.binding.session.subs...)
}

// QueuedMessages returns the session's currently authorized queue. Revoked
// messages and messages whose subscription no longer matches are removed. The
// result is a copy.
//
// QueuedMessages is a snapshot. Use NextDelivery to obtain messages for
// actual delivery, because that method rechecks the ACL immediately before
// handing out each message.
func (r *Recovery) QueuedMessages() []QueuedMessage {
	if r == nil || r.manager == nil || r.binding == nil {
		return nil
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !r.validLocked() {
		return nil
	}
	m.reauthorizeLocked(r.binding.session)
	return cloneMessages(r.binding.session.queue)
}

// AddSubscription adds or replaces a subscription on the session. The caller
// should normally invoke this after its SUBSCRIBE handler has authenticated
// the request; the ACL is checked here as well.
func (r *Recovery) AddSubscription(sub Subscription) error {
	if err := validateSubscription(sub); err != nil {
		return err
	}
	if r == nil || r.manager == nil || r.binding == nil {
		return ErrSessionGone
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !r.validLocked() {
		return ErrSessionGone
	}
	session := r.binding.session
	if !m.authz.Authorize(session.principal, ActionSubscribe, sub.Filter) {
		return ErrNotAuthorized
	}
	session.addSubscription(sub)
	return nil
}

// Enqueue adds a QoS 1 message to this connection's session queue. The message
// must match a live subscription, and both subscription and receive
// permissions are checked against the current ACL.
func (r *Recovery) Enqueue(msg QueuedMessage) error {
	if err := validateQueuedMessage(msg); err != nil {
		return err
	}
	if r == nil || r.manager == nil || r.binding == nil {
		return ErrSessionGone
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !r.validLocked() {
		return ErrSessionGone
	}
	return m.enqueueLocked(r.binding.session, msg)
}

// NextDelivery returns the next queued QoS 1 message that is still authorized
// by the current ACL. Revoked messages are discarded. It returns false when
// the queue is empty or this connection no longer owns the session.
//
// The ACL is evaluated at call time, immediately before delivery, not only at
// CONNECT time.
func (r *Recovery) NextDelivery() (Delivery, bool) {
	if r == nil || r.manager == nil || r.binding == nil {
		return Delivery{}, false
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !r.validLocked() {
		return Delivery{}, false
	}

	session := r.binding.session
	m.reauthorizeLocked(session)
	for len(session.queue) > 0 {
		msg := session.queue[0]
		session.queue[0] = QueuedMessage{}
		session.queue = session.queue[1:]
		if msg.QoS != 1 {
			continue
		}
		sub, ok := session.matchSubscription(msg.Topic)
		if !ok {
			continue
		}
		if !m.authz.Authorize(session.principal, ActionSubscribe, sub.Filter) {
			continue
		}
		if !m.authz.Authorize(session.principal, ActionReceive, msg.Topic) {
			continue
		}
		return Delivery{Message: cloneMessage(msg), Subscription: sub}, true
	}
	return Delivery{}, false
}

// Close detaches this connection from the session while retaining the session
// for a later Clean Start 0 resume. Calling Close after a takeover is a no-op
// and cannot disturb the new owner.
func (r *Recovery) Close() {
	if r == nil || r.manager == nil || r.binding == nil {
		return
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	b := r.binding
	if !b.active {
		return
	}
	b.active = false
	if b.session.owner == b {
		b.session.owner = nil
	}
}

// Recover performs the session-recovery decision for a CONNECT packet.
//
// It is atomic with respect to other Recover and Close calls for the same
// ClientID. If a same-principal connection is replaced, the old connection's
// callbacks are invoked after the new owner is installed, outside the
// Manager lock.
func (m *Manager) Recover(req ConnectRequest, conn Connection) (*Recovery, error) {
	if req.ClientID == "" {
		return nil, ErrEmptyClientID
	}
	if req.Principal == "" {
		return nil, ErrEmptyPrincipal
	}

	m.mu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			m.mu.Unlock()
		}
	}()
	m.initLocked()
	existing := m.sessions[req.ClientID]

	var (
		session        *sessionState
		old            *binding
		sessionPresent bool
		notify         bool
	)

	switch {
	case existing == nil:
		session = newSessionState(req.ClientID, req.Principal)
		m.sessions[req.ClientID] = session

	case req.CleanStart:
		old = existing.owner
		existing.discarded = true
		session = newSessionState(req.ClientID, req.Principal)
		m.sessions[req.ClientID] = session
		notify = old != nil && existing.principal == req.Principal

	case existing.principal != req.Principal:
		// The ClientID is being reused by another identity. Never expose the
		// previous owner's subscriptions or queue. Do not notify the previous
		// connection: otherwise anyone who guesses a ClientID could force a
		// disconnect of the legitimate client.
		old = existing.owner
		existing.discarded = true
		session = newSessionState(req.ClientID, req.Principal)
		m.sessions[req.ClientID] = session

	default:
		session = existing
		sessionPresent = true
		old = existing.owner
		notify = old != nil
		m.reauthorizeLocked(session)
	}

	if old != nil {
		old.active = false
		if old.session.owner == old {
			old.session.owner = nil
		}
	}

	m.nextID++
	b := &binding{
		id:      m.nextID,
		session: session,
		conn:    conn,
		active:  true,
	}
	session.owner = b
	recovery := &Recovery{
		manager:        m,
		binding:        b,
		sessionPresent: sessionPresent,
	}
	m.mu.Unlock()
	unlocked = true

	if notify && old != nil && old.conn != nil {
		old.conn.InvalidateInflight()
		old.conn.SessionTakenOver()
	}

	return recovery, nil
}

// Enqueue adds a QoS 1 message to an existing session, including a session
// whose client is currently offline. It is the routing-side counterpart to
// Recovery.Enqueue.
func (m *Manager) Enqueue(clientID string, msg QueuedMessage) error {
	if err := validateQueuedMessage(msg); err != nil {
		return err
	}
	m.mu.Lock()
	m.initLocked()
	defer m.mu.Unlock()
	session := m.sessions[clientID]
	if session == nil || session.discarded {
		return ErrNoSession
	}
	return m.enqueueLocked(session, msg)
}

// Delete removes a session. It is normally used when the broker decides to
// expire an offline session.
func (m *Manager) Delete(clientID string) {
	m.mu.Lock()
	m.initLocked()
	defer m.mu.Unlock()
	session := m.sessions[clientID]
	if session == nil {
		return
	}
	session.discarded = true
	if session.owner != nil {
		session.owner.active = false
		session.owner = nil
	}
	delete(m.sessions, clientID)
}

func (m *Manager) enqueueLocked(session *sessionState, msg QueuedMessage) error {
	if session.discarded {
		return ErrNoSession
	}
	if _, ok := session.matchSubscription(msg.Topic); !ok {
		return ErrNoSubscription
	}
	_, ok := m.authorizedSubscription(session, msg.Topic)
	if !ok {
		return ErrNotAuthorized
	}
	if !m.authz.Authorize(session.principal, ActionReceive, msg.Topic) {
		return ErrNotAuthorized
	}
	session.queue = append(session.queue, cloneMessage(msg))
	return nil
}

func (m *Manager) reauthorizeLocked(session *sessionState) {
	subs := session.subs
	keptSubs := subs[:0]
	for _, sub := range subs {
		if m.authz.Authorize(session.principal, ActionSubscribe, sub.Filter) {
			keptSubs = append(keptSubs, sub)
		}
	}
	for i := len(keptSubs); i < len(subs); i++ {
		subs[i] = Subscription{}
	}
	session.subs = keptSubs

	queue := session.queue
	keptQueue := queue[:0]
	for _, msg := range queue {
		if msg.QoS != 1 {
			continue
		}
		if _, ok := m.authorizedSubscription(session, msg.Topic); !ok {
			continue
		}
		if !m.authz.Authorize(session.principal, ActionReceive, msg.Topic) {
			continue
		}
		keptQueue = append(keptQueue, msg)
	}
	for i := len(keptQueue); i < len(queue); i++ {
		queue[i] = QueuedMessage{}
	}
	session.queue = keptQueue
}

func (m *Manager) authorizedSubscription(session *sessionState, topic string) (Subscription, bool) {
	for _, sub := range session.subs {
		if TopicMatches(sub.Filter, topic) &&
			m.authz.Authorize(session.principal, ActionSubscribe, sub.Filter) {
			return sub, true
		}
	}
	return Subscription{}, false
}

func validateSubscription(sub Subscription) error {
	if !ValidTopicFilter(sub.Filter) || sub.QoS > 2 {
		return fmt.Errorf("%w: filter %q qos %d", ErrInvalidSubscription, sub.Filter, sub.QoS)
	}
	return nil
}

func validateQueuedMessage(msg QueuedMessage) error {
	if msg.QoS != 1 || !ValidTopicName(msg.Topic) {
		return fmt.Errorf("%w: topic %q qos %d", ErrInvalidMessage, msg.Topic, msg.QoS)
	}
	return nil
}

func cloneMessage(msg QueuedMessage) QueuedMessage {
	if msg.Payload != nil {
		msg.Payload = append([]byte(nil), msg.Payload...)
	}
	return msg
}

func cloneMessages(msgs []QueuedMessage) []QueuedMessage {
	if msgs == nil {
		return nil
	}
	out := make([]QueuedMessage, len(msgs))
	for i, msg := range msgs {
		out[i] = cloneMessage(msg)
	}
	return out
}
