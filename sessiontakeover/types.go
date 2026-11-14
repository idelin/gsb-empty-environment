package sessiontakeover

import (
	"errors"
	"time"
)

// ConflictPolicy decides what happens when a different authenticated subject
// connects with a client ID already owned by another subject inside the same
// tenant.
type ConflictPolicy int

const (
	// ConflictReject denies the new connection. The original subject's
	// session is left untouched and keeps accumulating offline messages.
	ConflictReject ConflictPolicy = iota

	// ConflictIsolate gives the new subject its own subject-scoped session
	// while the original session remains intact. Nothing is silently
	// overwritten.
	ConflictIsolate
)

// Outcome describes the high-level result of a Connect call.
type Outcome int

const (
	// OutcomeRejected means the takeover was denied; the handle is nil.
	OutcomeRejected Outcome = iota
	// OutcomeCreated means a brand new session was created.
	OutcomeCreated
	// OutcomeResumed means an existing session of the same subject was
	// migrated onto the new connection.
	OutcomeResumed
	// OutcomeIsolated means a different subject connected under
	// ConflictIsolate and received its own isolated session.
	OutcomeIsolated
)

// Reason refines an Outcome with machine-readable context.
type Reason int

const (
	ReasonNone Reason = iota
	// ReasonDifferentPrincipal: another subject already owns the client ID.
	ReasonDifferentPrincipal
	// ReasonCredentialRevoked: the presented credential is revoked.
	ReasonCredentialRevoked
	// ReasonCrossTenant: a session exists for the same client ID in another
	// tenant. Tenant isolation forbids migration, so a fresh session is used.
	ReasonCrossTenant
	// ReasonSessionExpired: the previous session expired and was discarded.
	ReasonSessionExpired
	// ReasonCleanStart: the client asked for a clean start.
	ReasonCleanStart
	// ReasonInspectorUnavailable: the credential inspector returned an error.
	ReasonInspectorUnavailable
	// ReasonInvalidRequest: the request was malformed.
	ReasonInvalidRequest
)

// Sentinel errors returned by the package. Callers can use errors.Is.
var (
	ErrTakeoverRejected  = errors.New("sessiontakeover: takeover rejected")
	ErrCredentialRevoked = errors.New("sessiontakeover: presented credential is revoked")
	ErrRegistryClosed    = errors.New("sessiontakeover: registry is closed")
	ErrHandleClosed      = errors.New("sessiontakeover: session handle has been closed or superseded")
	ErrInvalidRequest    = errors.New("sessiontakeover: invalid request")
	ErrInvalidQoS        = errors.New("sessiontakeover: QoS must be 0, 1 or 2")
	ErrDuplicatePacketID = errors.New("sessiontakeover: packet ID is already in flight")
	ErrUnknownPacketID   = errors.New("sessiontakeover: unknown packet ID")
)

// Identity is the authenticated credential subject, scoped by tenant. Two
// subjects with the same name in different tenants are distinct identities.
type Identity struct {
	Tenant  string
	Subject string
}

// CredentialRef identifies the credential presented on a connection. The
// inspector decides whether it is still valid; the registry records it on the
// session so every takeover can be re-validated.
type CredentialRef struct {
	// ID is the credential identifier (certificate serial, token ID, ...).
	ID string
	// Identity is the subject asserted by the credential.
	Identity Identity
}

// Message is an application MQTT message.
type Message struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
	Dup     bool
}

// QueuedMessage is a Message held in the offline queue with enqueue metadata.
type QueuedMessage struct {
	Message
	EnqueuedAt time.Time
}

// Will is an MQTT Last Will and Testament message.
type Will struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
}

// Subscription is a topic filter with its granted QoS.
type Subscription struct {
	Filter string
	QoS    byte
}

// PendingDelivery is an outbound QoS 1/2 message that has not been fully
// acknowledged and must be retransmitted when the session resumes.
type PendingDelivery struct {
	PacketID uint16
	Message  Message
	QoS      byte
	// ResendPubREL is true when a PUBREC was already received: the broker
	// must resend PUBREL instead of a DUP PUBLISH.
	ResendPubREL bool
	At           time.Time
}

// ConnectRequest carries the parameters of an incoming MQTT CONNECT.
type ConnectRequest struct {
	Tenant     string
	ClientID   string
	CleanStart bool
	Presented  CredentialRef
	// Expiry overrides Config.DefaultSessionExpiry for this session.
	// A value <= 0 means "use the default"; a zero final duration means the
	// session never expires while the registry runs.
	Expiry time.Duration
	// Will, when non-nil, replaces any will migrated from the old connection.
	Will *Will
}

// ConnectResult reports what the registry did with a Connect call.
type ConnectResult struct {
	Outcome Outcome
	Reason  Reason
	// SessionPresent is true only when state from an existing session was
	// migrated onto the returned handle.
	SessionPresent bool
	Generation     uint64
}

// Stats is a point-in-time snapshot of registry occupancy.
type Stats struct {
	Sessions       int
	ActiveSessions int
}
