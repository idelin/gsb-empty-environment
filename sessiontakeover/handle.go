package sessiontakeover

import (
	"sync/atomic"
	"time"
)

// Handle is the connection-scoped view of a Session. When a takeover
// supersedes a connection the old Handle is marked closed: every further
// operation returns ErrHandleClosed so a displaced connection can never keep
// mutating state that now belongs to the new generation.
type Handle struct {
	session    *Session
	generation uint64
	closed     atomic.Bool

	// OnDisplaced is invoked exactly once when another connection takes over
	// this session. The broker uses it to tear down the underlying transport.
	// It must not block on Registry calls.
	OnDisplaced func(reason DisplaceReason)
}

// DisplaceReason explains why a handle was superseded.
type DisplaceReason int

const (
	// DisplaceTakeover: a newer generation of the same subject took over.
	DisplaceTakeover DisplaceReason = iota
	// DisplaceExpired: the session expired or was reaped.
	DisplaceExpired
	// DisplaceTerminated: the session was explicitly terminated.
	DisplaceTerminated
)

func (h *Handle) guard() error {
	if h == nil || h.closed.Load() {
		return ErrHandleClosed
	}
	h.session.mu.RLock()
	match := !h.session.closed && h.session.boundHandle == h
	h.session.mu.RUnlock()
	if !match {
		h.closed.Store(true)
		return ErrHandleClosed
	}
	return nil
}

// Generation returns the connection generation this handle represents.
func (h *Handle) Generation() uint64 {
	if err := h.guard(); err != nil {
		return 0
	}
	return h.generation
}

// Identity returns the owning identity of the underlying session.
func (h *Handle) Identity() (Identity, error) {
	if err := h.guard(); err != nil {
		return Identity{}, err
	}
	return h.session.Identity(), nil
}

// Session exposes the underlying persistent session. The returned pointer
// remains valid across generations, but mutating it through a closed handle's
// sibling methods is rejected; use the current handle's methods instead.
func (h *Handle) Session() (*Session, error) {
	if err := h.guard(); err != nil {
		return nil, err
	}
	return h.session, nil
}

// AddSubscription adds or replaces a subscription on the session.
func (h *Handle) AddSubscription(filter string, qos byte) error {
	if err := h.guard(); err != nil {
		return err
	}
	return h.session.AddSubscription(filter, qos)
}

// RemoveSubscription removes a subscription.
func (h *Handle) RemoveSubscription(filter string) error {
	if err := h.guard(); err != nil {
		return err
	}
	return h.session.RemoveSubscription(filter)
}

// Subscriptions returns the migrated/current subscriptions.
func (h *Handle) Subscriptions() ([]Subscription, error) {
	if err := h.guard(); err != nil {
		return nil, err
	}
	return h.session.Subscriptions(), nil
}

// SetWill replaces the will carried by this session.
func (h *Handle) SetWill(w *Will) error {
	if err := h.guard(); err != nil {
		return err
	}
	return h.session.SetWill(w)
}

// Will returns the current will.
func (h *Handle) Will() (*Will, error) {
	if err := h.guard(); err != nil {
		return nil, err
	}
	return h.session.Will(), nil
}

// EnqueueOffline parks a message until the next resume.
func (h *Handle) EnqueueOffline(msg Message) error {
	if err := h.guard(); err != nil {
		return err
	}
	if !h.session.EnqueueOffline(msg, time.Now()) {
		h.closed.Store(true)
		return ErrHandleClosed
	}
	return nil
}

// DrainOffline atomically removes and returns all queued messages. Call this
// once immediately after a successful resume.
func (h *Handle) DrainOffline() ([]QueuedMessage, error) {
	if err := h.guard(); err != nil {
		return nil, err
	}
	return h.session.drainOffline(), nil
}

// TrackOutbound records an in-flight QoS 1/2 PUBLISH.
func (h *Handle) TrackOutbound(packetID uint16, msg Message) error {
	if err := h.guard(); err != nil {
		return err
	}
	return h.session.TrackOutbound(packetID, msg)
}

// MarkPubRec advances an outbound QoS 2 flow to PUBREL-pending.
func (h *Handle) MarkPubRec(packetID uint16) error {
	if err := h.guard(); err != nil {
		return err
	}
	return h.session.MarkPubRec(packetID)
}

// AckOutbound completes an outbound flow.
func (h *Handle) AckOutbound(packetID uint16) error {
	if err := h.guard(); err != nil {
		return err
	}
	return h.session.AckOutbound(packetID)
}

// Retransmit returns all unacknowledged outbound packets for redelivery.
func (h *Handle) Retransmit() ([]PendingDelivery, error) {
	if err := h.guard(); err != nil {
		return nil, err
	}
	return h.session.Retransmit(), nil
}

// RememberInbound records an inbound QoS 2 packet ID; duplicate is true on a
// replay and the broker must resend the protocol acknowledgement without
// redelivering the application message.
func (h *Handle) RememberInbound(packetID uint16) (duplicate bool, err error) {
	if err := h.guard(); err != nil {
		return false, err
	}
	return h.session.RememberInbound(packetID, time.Now()), nil
}

// IsInboundDuplicate reports whether an inbound QoS 2 packet ID is a replay.
func (h *Handle) IsInboundDuplicate(packetID uint16) (bool, error) {
	if err := h.guard(); err != nil {
		return false, err
	}
	return h.session.IsInboundDuplicate(packetID), nil
}

// ReleaseInbound forgets an inbound QoS 2 packet ID after PUBCOMP.
func (h *Handle) ReleaseInbound(packetID uint16) error {
	if err := h.guard(); err != nil {
		return err
	}
	h.session.ReleaseInbound(packetID)
	return nil
}

// Disconnect parks the session after an ungraceful connection loss. The will
// is retained so the broker can publish it, and every subscription, QoS 1/2
// packet and offline queue entry survives for the next same-subject resume.
func (h *Handle) Disconnect() error {
	if h == nil {
		return ErrHandleClosed
	}
	h.session.mu.Lock()
	if h.closed.Load() || h.session.closed || h.session.boundHandle != h {
		h.session.mu.Unlock()
		h.closed.Store(true)
		return ErrHandleClosed
	}
	h.session.active = false
	h.session.boundHandle = nil
	h.session.mu.Unlock()
	h.closed.Store(true)
	return nil
}

// GracefulDisconnect parks the session for a clean disconnect and clears the
// will, which per MQTT must not be published on an intentional disconnect.
func (h *Handle) GracefulDisconnect() error {
	if h == nil {
		return ErrHandleClosed
	}
	h.session.mu.Lock()
	if h.closed.Load() || h.session.closed || h.session.boundHandle != h {
		h.session.mu.Unlock()
		h.closed.Store(true)
		return ErrHandleClosed
	}
	h.session.active = false
	h.session.boundHandle = nil
	h.session.will = nil
	h.session.mu.Unlock()
	h.closed.Store(true)
	return nil
}

// Terminate closes the handle and marks its underlying session closed.
// Broker code that owns a Registry should call Registry.TerminateSession
// instead so registry bookkeeping is removed as well; this method is
// provided for registries that integrate sessions differently.
func (h *Handle) Terminate() error {
	if h == nil {
		return ErrHandleClosed
	}
	h.session.mu.Lock()
	if h.closed.Load() || h.session.closed || h.session.boundHandle != h {
		h.session.mu.Unlock()
		h.closed.Store(true)
		return ErrHandleClosed
	}
	h.session.closed = true
	h.session.active = false
	h.session.boundHandle = nil
	h.session.mu.Unlock()
	h.closed.Store(true)
	return nil
}
