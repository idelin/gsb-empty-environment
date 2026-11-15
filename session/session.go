// Package session implements the MQTT 5 session restoration step of CONNECT
// processing.
//
// Security properties enforced by [Store.Restore]:
//
//   - A persistent session is bound to the authenticated [Principal] of the
//     connection that created it. A later CONNECT reusing the same Client ID
//     with different credentials can never inherit the old subscriptions or
//     queued messages: the old session is discarded and Session Present is 0.
//   - A same-principal reconnect takes over any live connection. The old
//     [Connection] receives exactly one Session Taken Over notification
//     (DISCONNECT reason 0x8E) and every in-flight outgoing PUBLISH is
//     invalidated: late PUBACK/PUBREC packets from the old connection are
//     rejected and the unacknowledged messages return to the delivery queue.
//   - On restoration every retained subscription and every queued QoS 1
//     message is re-checked against the caller's current [ACL]. Entries whose
//     permission has been revoked are dropped before anything is delivered.
//
// Typical CONNECT integration:
//
//  1. Authenticate the transport and derive the [Principal].
//  2. Call [Store.Restore] with the Client ID, Clean Start flag, current ACL
//     and the network [Connection].
//  3. Send CONNACK with RestoreResult.SessionPresent as the Session Present
//     field; log or audit RestoreResult.DroppedMessages.
//  4. On graceful disconnect call [ConnHandle.Detach] (session retained) or
//     [ConnHandle.Terminate] (clean session removed from the store).
package session

import (
	"context"
	"errors"
	"sync"
)

// ReasonSessionTakenOver is the MQTT 5 DISCONNECT reason code "Session taken
// over" (0x8E) sent to a connection displaced by a newer CONNECT.
const ReasonSessionTakenOver byte = 0x8E

var (
	// ErrEmptyClientID is returned when a restore request carries no Client ID.
	ErrEmptyClientID = errors.New("session: client id must not be empty")

	// ErrNilACL is returned when a restore request carries no ACL.
	// Authorization must always be explicit so default-deny cannot be bypassed.
	ErrNilACL = errors.New("session: acl must not be nil")

	// ErrStaleHandle is returned when a connection superseded by takeover
	// tries to mutate session state, e.g. a late PUBACK on the old socket.
	ErrStaleHandle = errors.New("session: connection has been superseded by session take over")

	// ErrUnknownPacketID is returned when an acknowledgement does not match
	// any tracked in-flight PUBLISH.
	ErrUnknownPacketID = errors.New("session: unknown packet id for in-flight publish")
)

// Principal is the authenticated identity a persistent session is permanently
// bound to for its whole lifetime.
type Principal struct {
	// Name is the stable authenticated subject identifier, e.g. a username or
	// certificate subject. Equality is by Name only.
	Name string
}

// ACL answers authorization questions against the caller's current policy.
// Implementations must be safe for concurrent use. They must not call back
// into the [Store]: the store lock is held while the ACL is consulted during
// restoration, so a re-entrant call would deadlock.
type ACL interface {
	// CanSubscribe reports whether the principal may currently hold a
	// subscription on topicFilter.
	CanSubscribe(ctx context.Context, principal Principal, topicFilter string) bool

	// CanReceive reports whether the principal may currently receive a message
	// published to topicName.
	CanReceive(ctx context.Context, principal Principal, topicName string) bool
}

// Message is an MQTT application message. The payload must not be mutated
// after it has been handed to the session package.
type Message struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
}

// Subscription is a topic-filter subscription retained by a persistent
// session.
type Subscription struct {
	Filter string
	QoS    byte
}

// QueuedMessage is a QoS > 0 message waiting to be delivered to the session's
// client.
type QueuedMessage struct {
	Message
}

// InFlightPublish is a QoS 1/2 PUBLISH already sent to the client but not yet
// acknowledged with PUBACK/PUBREC.
type InFlightPublish struct {
	PacketID uint16
	Message
}

// Connection is the network-level client connection owned by a [ConnHandle].
type Connection interface {
	// SessionTakenOver is invoked exactly once when another CONNECT with the
	// same Client ID supersedes this connection. The implementation must send
	// DISCONNECT with reason code [ReasonSessionTakenOver] and close the
	// socket; every packet still in flight on this connection is dead.
	SessionTakenOver()
}

// RestoreRequest carries the inputs of one CONNECT restoration step.
type RestoreRequest struct {
	ClientID   string
	Principal  Principal
	CleanStart bool
	ACL        ACL
	Conn       Connection
}

// RestoreResult is the outcome of [Store.Restore].
type RestoreResult struct {
	// Handle is the exclusive handle the new connection must use while it
	// stays connected.
	Handle *ConnHandle

	// SessionPresent is the value of the MQTT 5 CONNACK Session Present
	// field. It is true only when an existing same-principal session was
	// resumed.
	SessionPresent bool

	// DroppedMessages lists subscriptions and messages discarded during
	// restoration, either because the principal changed, Clean Start was 1,
	// or the current ACL no longer permits them. It is intended for auditing.
	DroppedMessages []Message
}

// Store holds all persistent sessions keyed by Client ID. The zero value is
// not usable; create one with [NewStore].
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewStore creates an empty session store.
func NewStore() *Store {
	return &Store{sessions: make(map[string]*Session)}
}

// Lookup returns the session currently registered under clientID, or nil.
func (s *Store) Lookup(clientID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[clientID]
}

// Restore performs the session restoration step of a CONNECT packet.
//
// It is safe for concurrent use. Concurrent CONNECT packets sharing one
// Client ID are serialized: exactly one connection ends up owning the
// session and every displaced connection is notified exactly once.
func (s *Store) Restore(ctx context.Context, req RestoreRequest) (*RestoreResult, error) {
	if req.ClientID == "" {
		return nil, ErrEmptyClientID
	}
	if req.ACL == nil {
		return nil, ErrNilACL
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, exists := s.sessions[req.ClientID]
	if !exists {
		sess = &Session{
			store:     s,
			clientID:  req.ClientID,
			principal: req.Principal,
			acl:       req.ACL,
		}
		s.sessions[req.ClientID] = sess
		return sess.attach(req.Conn, false, nil), nil
	}

	sess.mu.Lock()
	samePrincipal := sess.principal.Name == req.Principal.Name
	if req.CleanStart || !samePrincipal {
		// Clean Start = 1 or stolen-Client-ID attempt: wipe every piece of
		// state the old principal could have leaked through the session.
		dropped := sess.snapshotStateLocked()
		oldHandle := sess.handle

		sess.subs = nil
		sess.queue = nil
		sess.inflight = nil
		sess.principal = req.Principal
		sess.acl = req.ACL
		sess.handle = nil
		sess.generation++
		if oldHandle != nil {
			oldHandle.markSupersededLocked(false)
		}
		sess.mu.Unlock()

		if oldHandle != nil {
			oldHandle.notifyTakenOver()
		}
		return sess.attach(req.Conn, false, dropped), nil
	}

	// Same principal, Clean Start = 0: resume the session against the current
	// ACL, then take over the live connection (if any).
	oldHandle := sess.handle
	sess.acl = req.ACL
	dropped := sess.reauthorizeLocked(ctx)
	sess.generation++
	handle := newConnHandle(sess, req.Conn)
	sess.handle = handle
	if oldHandle != nil {
		oldHandle.markSupersededLocked(true)
	}
	sess.mu.Unlock()

	if oldHandle != nil {
		oldHandle.notifyTakenOver()
	}
	return &RestoreResult{Handle: handle, SessionPresent: true, DroppedMessages: dropped}, nil
}

// remove deletes sess from the store. Called with both store and session
// locks held (store lock first).
func (s *Store) remove(sess *Session) {
	if current := s.sessions[sess.clientID]; current == sess {
		delete(s.sessions, sess.clientID)
	}
}

// Session is the MQTT client state surviving across connections.
type Session struct {
	store *Store

	mu        sync.Mutex
	clientID  string
	principal Principal
	acl       ACL
	subs      []Subscription
	queue     []QueuedMessage
	inflight  []InFlightPublish
	handle    *ConnHandle

	// generation is bumped on every attach or replacement. A handle is
	// current only while it matches the latest generation.
	generation uint64
}

// ClientID returns the Client ID this session is registered under.
func (sess *Session) ClientID() string {
	return sess.clientID
}

// Principal returns the authenticated identity bound to the session.
func (sess *Session) Principal() Principal {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.principal
}

// Subscriptions returns a copy of the currently retained subscriptions.
func (sess *Session) Subscriptions() []Subscription {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]Subscription(nil), sess.subs...)
}

// QueuedMessages returns a copy of the messages waiting for delivery.
func (sess *Session) QueuedMessages() []QueuedMessage {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]QueuedMessage(nil), sess.queue...)
}

// InFlightPublishes returns a copy of the unacknowledged outgoing PUBLISHes.
func (sess *Session) InFlightPublishes() []InFlightPublish {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]InFlightPublish(nil), sess.inflight...)
}

// AddSubscription retains sub for the session after checking the current ACL.
// It returns false (and changes nothing) when the subscription is denied.
func (sess *Session) AddSubscription(ctx context.Context, sub Subscription) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !sess.acl.CanSubscribe(ctx, sess.principal, sub.Filter) {
		return false
	}
	for i := range sess.subs {
		if sess.subs[i].Filter == sub.Filter {
			sess.subs[i] = sub
			return true
		}
	}
	sess.subs = append(sess.subs, sub)
	return true
}

// RemoveSubscription drops the subscription on filter, if present.
func (sess *Session) RemoveSubscription(filter string) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	kept := sess.subs[:0]
	for _, sub := range sess.subs {
		if sub.Filter != filter {
			kept = append(kept, sub)
		}
	}
	sess.subs = kept
}

// EnqueueMessage stores an offline QoS 1/2 message for later delivery. The
// broker is expected to have authorized the original PUBLISH; the receive
// permission is re-checked when the session is restored.
func (sess *Session) EnqueueMessage(msg Message) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.queue = append(sess.queue, QueuedMessage{Message: msg})
}

// snapshotStateLocked flattens every retained state entry into a message
// list for auditing when the whole session has to be discarded.
func (sess *Session) snapshotStateLocked() []Message {
	dropped := make([]Message, 0, len(sess.subs)+len(sess.queue)+len(sess.inflight))
	for _, sub := range sess.subs {
		dropped = append(dropped, Message{Topic: sub.Filter, QoS: sub.QoS})
	}
	for _, qm := range sess.queue {
		dropped = append(dropped, qm.Message)
	}
	for _, inf := range sess.inflight {
		dropped = append(dropped, inf.Message)
	}
	return dropped
}

// reauthorizeLocked re-applies the current ACL to every retained subscription
// and queued message, dropping revoked entries in place. Callers hold sess.mu.
func (sess *Session) reauthorizeLocked(ctx context.Context) []Message {
	var dropped []Message

	keptSubs := sess.subs[:0]
	for _, sub := range sess.subs {
		if sess.acl.CanSubscribe(ctx, sess.principal, sub.Filter) {
			keptSubs = append(keptSubs, sub)
		} else {
			dropped = append(dropped, Message{Topic: sub.Filter, QoS: sub.QoS})
		}
	}
	sess.subs = keptSubs

	keptQueue := sess.queue[:0]
	for _, qm := range sess.queue {
		if sess.acl.CanReceive(ctx, sess.principal, qm.Topic) {
			keptQueue = append(keptQueue, qm)
		} else {
			dropped = append(dropped, qm.Message)
		}
	}
	sess.queue = keptQueue

	return dropped
}

// attach installs a new handle on the session. The store lock must be held.
func (sess *Session) attach(conn Connection, present bool, dropped []Message) *RestoreResult {
	sess.mu.Lock()
	sess.generation++
	handle := newConnHandle(sess, conn)
	sess.handle = handle
	sess.mu.Unlock()
	return &RestoreResult{Handle: handle, SessionPresent: present, DroppedMessages: dropped}
}
