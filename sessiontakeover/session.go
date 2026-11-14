package sessiontakeover

import (
	"sync"
	"time"
)

const defaultInboundWindow = 256

type queuedItem struct {
	msg Message
	at  time.Time
}

// inboundLedger remembers recently seen inbound QoS 2 packet IDs so that a
// replayed PUBLISH/PUBREL cannot cause duplicate delivery or state churn.
// The map is bounded: when it grows past the window the oldest entry is
// pruned. Packet IDs that complete the flow are released eagerly.
type inboundLedger struct {
	seen   map[uint16]time.Time
	window int
}

func newInboundLedger(window int) *inboundLedger {
	if window <= 0 {
		window = defaultInboundWindow
	}
	return &inboundLedger{seen: make(map[uint16]time.Time), window: window}
}

// duplicate reports whether the packet ID is part of an in-flight or recently
// completed QoS 2 flow.
func (l *inboundLedger) duplicate(id uint16) bool {
	_, ok := l.seen[id]
	return ok
}

// remember records a packet ID and prunes the oldest entry when over budget.
func (l *inboundLedger) remember(id uint16, now time.Time) {
	if _, ok := l.seen[id]; ok {
		return
	}
	l.seen[id] = now
	if len(l.seen) <= l.window {
		return
	}
	var oldestID uint16
	var oldestAt time.Time
	for pid, at := range l.seen {
		if oldestAt.IsZero() || at.Before(oldestAt) {
			oldestAt = at
			oldestID = pid
		}
	}
	delete(l.seen, oldestID)
}

// release removes a packet ID once its QoS 2 flow fully completes.
func (l *inboundLedger) release(id uint16) { delete(l.seen, id) }

type outboundPacket struct {
	id           uint16
	msg          Message
	qos          byte
	resendPubREL bool
	at           time.Time
}

type outboundLedger struct {
	packets map[uint16]*outboundPacket
}

func newOutboundLedger() outboundLedger {
	return outboundLedger{packets: make(map[uint16]*outboundPacket)}
}

// Session is the persistent per-(tenant, client, subject) MQTT state that
// survives disconnects and migrates across connection generations.
//
// All exported methods are safe for concurrent use.
type Session struct {
	mu sync.RWMutex

	tenant     string
	clientID   string
	subject    string
	generation uint64

	subs     map[string]Subscription
	offline  []queuedItem
	outbound outboundLedger
	inbound  *inboundLedger
	will     *Will

	expiresAt   time.Time // zero means never expires
	createdAt   time.Time
	lastSeen    time.Time
	active      bool
	closed      bool
	boundHandle *Handle
	maxOffline  int
}

// Identity returns the tenant and subject that own this session.
func (s *Session) Identity() Identity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Identity{Tenant: s.tenant, Subject: s.subject}
}

// ClientID returns the MQTT client identifier of this session.
func (s *Session) ClientID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clientID
}

// Generation returns the current connection generation. It starts at 1 and
// increments on every takeover/resume.
func (s *Session) Generation() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation
}

// Active reports whether a handle currently holds the session.
func (s *Session) Active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// ExpiresAt returns the absolute expiry instant. The zero time means the
// session never expires.
func (s *Session) ExpiresAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.expiresAt
}

// CreatedAt returns when the current incarnation of the session was created.
func (s *Session) CreatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.createdAt
}

// LastSeen returns the time of the most recent resume.
func (s *Session) LastSeen() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSeen
}

func (s *Session) isExpired(now time.Time) bool {
	return !s.expiresAt.IsZero() && now.After(s.expiresAt)
}

// AddSubscription stores or replaces a subscription for the filter.
func (s *Session) AddSubscription(filter string, qos byte) error {
	if qos > 2 {
		return ErrInvalidQoS
	}
	if filter == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	s.subs[filter] = Subscription{Filter: filter, QoS: qos}
	return nil
}

// RemoveSubscription removes a subscription filter.
func (s *Session) RemoveSubscription(filter string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	delete(s.subs, filter)
	return nil
}

// Subscriptions returns a defensive copy of the current subscriptions.
func (s *Session) Subscriptions() []Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		out = append(out, sub)
	}
	return out
}

// HasSubscription reports whether the exact filter is subscribed.
func (s *Session) HasSubscription(filter string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.subs[filter]
	return ok
}

// SetWill stores the connection's last will. A nil will clears it.
func (s *Session) SetWill(w *Will) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	s.will = cloneWill(w)
	return nil
}

// Will returns a copy of the current will, or nil if none is set.
func (s *Session) Will() *Will {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneWill(s.will)
}

func cloneWill(w *Will) *Will {
	if w == nil {
		return nil
	}
	cp := *w
	cp.Payload = append([]byte(nil), w.Payload...)
	return &cp
}

func cloneMessage(m Message) Message {
	cp := m
	cp.Payload = append([]byte(nil), m.Payload...)
	return cp
}

// EnqueueOffline stores a message for delivery on the next resume. When the
// queue is bounded and full the oldest message is dropped to make room.
// It returns false when the session is closed.
func (s *Session) EnqueueOffline(msg Message, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	item := queuedItem{msg: cloneMessage(msg), at: now}
	if s.maxOffline > 0 && len(s.offline) >= s.maxOffline {
		s.offline = append(s.offline[:0], s.offline[1:]...)
	}
	s.offline = append(s.offline, item)
	return true
}

// OfflineMessages returns a copy of queued messages without removing them.
func (s *Session) OfflineMessages() []QueuedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]QueuedMessage, 0, len(s.offline))
	for _, item := range s.offline {
		out = append(out, QueuedMessage{Message: cloneMessage(item.msg), EnqueuedAt: item.at})
	}
	return out
}

// OfflineDepth reports how many messages are waiting for the next resume.
func (s *Session) OfflineDepth() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.offline)
}

// drainOffline removes and returns all queued messages. The queue is handed
// over atomically so a concurrent enqueue cannot interleave with delivery.
func (s *Session) drainOffline() []QueuedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]QueuedMessage, 0, len(s.offline))
	for _, item := range s.offline {
		out = append(out, QueuedMessage{Message: cloneMessage(item.msg), EnqueuedAt: item.at})
	}
	s.offline = s.offline[:0]
	return out
}

// TrackOutbound records an in-flight QoS 1/2 PUBLISH so it can be
// retransmitted after a reconnect. QoS 0 packets are not tracked.
func (s *Session) TrackOutbound(packetID uint16, msg Message) error {
	if msg.QoS != 1 && msg.QoS != 2 {
		return ErrInvalidQoS
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	if _, exists := s.outbound.packets[packetID]; exists {
		return ErrDuplicatePacketID
	}
	s.outbound.packets[packetID] = &outboundPacket{
		id:  packetID,
		msg: cloneMessage(msg),
		qos: msg.QoS,
		at:  time.Now(),
	}
	return nil
}

// MarkPubRec records that a QoS 2 PUBLISH reached PUBREC: future retries must
// resend PUBREL rather than the PUBLISH.
func (s *Session) MarkPubRec(packetID uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	pkt, ok := s.outbound.packets[packetID]
	if !ok {
		return ErrUnknownPacketID
	}
	pkt.resendPubREL = true
	return nil
}

// AckOutbound removes a fully acknowledged outbound packet (PUBACK for QoS 1,
// PUBCOMP for QoS 2).
func (s *Session) AckOutbound(packetID uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	if _, ok := s.outbound.packets[packetID]; !ok {
		return ErrUnknownPacketID
	}
	delete(s.outbound.packets, packetID)
	return nil
}

// Retransmit returns every unacknowledged outbound packet. PUBLISH messages
// are flagged Dup; packets already past PUBREC request a PUBREL resend.
func (s *Session) Retransmit() []PendingDelivery {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PendingDelivery, 0, len(s.outbound.packets))
	for _, pkt := range s.outbound.packets {
		msg := cloneMessage(pkt.msg)
		if !pkt.resendPubREL {
			msg.Dup = true
		}
		out = append(out, PendingDelivery{
			PacketID:     pkt.id,
			Message:      msg,
			QoS:          pkt.qos,
			ResendPubREL: pkt.resendPubREL,
			At:           pkt.at,
		})
	}
	return out
}

// RememberInbound records an inbound QoS 2 packet ID and reports whether it
// was already seen (a replay).
func (s *Session) RememberInbound(packetID uint16, now time.Time) (duplicate bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inbound.duplicate(packetID) {
		return true
	}
	s.inbound.remember(packetID, now)
	return false
}

// IsInboundDuplicate reports whether an inbound QoS 2 packet ID is already
// part of a flow.
func (s *Session) IsInboundDuplicate(packetID uint16) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inbound.duplicate(packetID)
}

// ReleaseInbound forgets an inbound QoS 2 packet ID after PUBCOMP.
func (s *Session) ReleaseInbound(packetID uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbound.release(packetID)
}
