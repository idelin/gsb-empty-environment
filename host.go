package netsec

import (
	"context"
	"errors"
	"sync"
)

// FrameKind identifies the role of an incoming frame.
type FrameKind int

const (
	// FrameHandle passes a kernel object handle (e.g. a passed file
	// descriptor) that authorizes a later body frame.
	FrameHandle FrameKind = iota + 1
	// FrameBody is the message payload that consumes a previously received
	// handle.
	FrameBody
)

// Errors reported by the host. Wrapped errors are preserved via errors.Is.
var (
	// ErrUnknownConnection means a frame arrived on a connection the host
	// never registered.
	ErrUnknownConnection = errors.New("netsec: unknown connection")
	// ErrMissingCredential means no trusted credential exists for the
	// peer in the generation in force when the frame was received.
	ErrMissingCredential = errors.New("netsec: missing trusted credential")
	// ErrGenerationMismatch means a body belongs to a different generation
	// than its handle. The handle may never outlive a downgrade.
	ErrGenerationMismatch = errors.New("netsec: handle and body must share one credential generation")
	// ErrOrphanBody means a body arrived without a pending handle.
	ErrOrphanBody = errors.New("netsec: message body received without handle")
	// ErrUnauthorized means the policy denied the request.
	ErrUnauthorized = errors.New("netsec: request denied by policy")
)

// Handle is a kernel object reference received on a connection, bound to the
// credential generation observed when it arrived.
type Handle struct {
	ID    string
	Value any
}

// IncomingFrame is one frame read off a connection. Peer is the identity
// observed by the transport for this single receive call; it must be
// re-read per frame rather than cached at connect time.
type IncomingFrame struct {
	Connection string
	Kind       FrameKind
	Peer       PeerCred
	Handle     Handle
	Body       []byte
	Requires   []Capability
}

// FrameTransport is the per-message transport the host pumps.
type FrameTransport interface {
	Receive(ctx context.Context) (IncomingFrame, error)
}

// PeerIdentity is the connection-time identity. It is retained for audit
// only and can never substitute for the per-message credential snapshot.
type PeerIdentity struct {
	Connection string
	Peer       PeerCred
	Principal  string
}

// RequireCap is one authorization check attached to a request.
type RequireCap struct {
	Capability Capability
	Granted    bool
}

// Request is the per-message, fully-stamped authorization request handed to
// the policy. Credential is the snapshot observed when the body was
// received; the connection identity is deliberately not passed to the
// policy.
type Request struct {
	Connection string
	Epoch      uint64
	Credential Credential
	Body       []byte
	Handle     Handle
	Requires   []RequireCap
}

// Decision is the policy verdict for one request.
type Decision struct {
	Allow  bool
	Reason string
}

// Policy authorizes one request using only the credential stamped at
// message receive time.
type Policy interface {
	Authorize(req Request) Decision
}

// PolicyFunc adapts a function to the Policy interface.
type PolicyFunc func(req Request) Decision

// Authorize implements Policy.
func (f PolicyFunc) Authorize(req Request) Decision { return f(req) }

// AuditEntry records everything needed to reconstruct why a frame was
// accepted or rejected, including both the per-message snapshot and the
// connection-level identity.
type AuditEntry struct {
	Connection     string
	Kind           FrameKind
	Epoch          uint64
	Principal      string
	ConnectionPeer PeerCred
	MessagePeer    PeerCred
	Allowed        bool
	Err            error
	Reason         string
	HandleID       string
}

type pendingHandle struct {
	handle Handle
	epoch  uint64
}

// Host is the message host. Every frame is authorized against the trusted
// credential snapshot of the generation in force at the instant it is
// received; connection-level identity is recorded for audit only.
type Host struct {
	epochs *CredentialEpoch
	policy Policy

	mu          sync.Mutex
	connections map[string]PeerIdentity
	pending     map[string]map[string]pendingHandle
	audit       []AuditEntry
}

// NewHost builds a host backed by the credential-generation component and
// the given per-message policy.
func NewHost(epochs *CredentialEpoch, policy Policy) *Host {
	return &Host{
		epochs:      epochs,
		policy:      policy,
		connections: make(map[string]PeerIdentity),
		pending:     make(map[string]map[string]pendingHandle),
	}
}

// RegisterConnection records the connect-time identity for audit. It does
// not grant any privilege.
func (h *Host) RegisterConnection(id string, peer PeerCred) {
	h.mu.Lock()
	defer h.mu.Unlock()
	principal := ""
	if cred, _, ok := h.epochs.Lookup(peer); ok {
		principal = cred.Principal
	}
	h.connections[id] = PeerIdentity{Connection: id, Peer: peer, Principal: principal}
	if _, ok := h.pending[id]; !ok {
		h.pending[id] = make(map[string]pendingHandle)
	}
}

// CloseConnection drops the connection and every pending handle it owned.
func (h *Host) CloseConnection(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.connections, id)
	delete(h.pending, id)
}

// HandleFrame processes one received frame. The returned error is nil when
// the frame was accepted; otherwise it is one of the Err* sentinels
// (possibly wrapped) and the frame is refused.
func (h *Host) HandleFrame(frame IncomingFrame) error {
	h.mu.Lock()
	connIdentity, known := h.connections[frame.Connection]
	h.mu.Unlock()
	if !known {
		return h.recordDeny(frame, PeerCred{}, 0, "", ErrUnknownConnection, "")
	}

	// Per-message trusted snapshot: the credential is resolved for this
	// single receive against the generation currently in force. The
	// connection identity never participates in the lookup.
	cred, epoch, ok := h.epochs.Lookup(frame.Peer)
	if !ok {
		if frame.Kind == FrameHandle {
			// A handle received without a credential must never be parked.
			return h.recordDeny(frame, connIdentity.Peer, epoch, "", ErrMissingCredential, "")
		}
		// A body without a credential invalidates any matching pending
		// handle so it cannot be completed later under a new identity.
		h.mu.Lock()
		if pending := h.pending[frame.Connection]; pending != nil {
			delete(pending, frame.Handle.ID)
		}
		h.mu.Unlock()
		return h.recordDeny(frame, connIdentity.Peer, epoch, "", ErrMissingCredential, "")
	}

	switch frame.Kind {
	case FrameHandle:
		h.mu.Lock()
		pending := h.pending[frame.Connection]
		if _, dup := pending[frame.Handle.ID]; dup {
			h.mu.Unlock()
			return h.recordDeny(frame, connIdentity.Peer, epoch, cred.Principal, ErrUnauthorized, "duplicate handle")
		}
		pending[frame.Handle.ID] = pendingHandle{handle: frame.Handle, epoch: epoch}
		h.mu.Unlock()
		h.record(AuditEntry{
			Connection:     frame.Connection,
			Kind:           FrameHandle,
			Epoch:          epoch,
			Principal:      cred.Principal,
			ConnectionPeer: connIdentity.Peer,
			MessagePeer:    frame.Peer,
			Allowed:        true,
			HandleID:       frame.Handle.ID,
		})
		return nil

	case FrameBody:
		h.mu.Lock()
		pending := h.pending[frame.Connection]
		ph, exists := pending[frame.Handle.ID]
		h.mu.Unlock()
		if !exists {
			return h.recordDeny(frame, connIdentity.Peer, epoch, cred.Principal, ErrOrphanBody, "")
		}
		// Same-message invariant: handle and body must be stamped with one
		// generation, so a downgrade between the two receives invalidates
		// the handle instead of extending its old privileges.
		if ph.epoch != epoch {
			h.mu.Lock()
			delete(pending, frame.Handle.ID)
			h.mu.Unlock()
			return h.recordDeny(frame, connIdentity.Peer, epoch, cred.Principal, ErrGenerationMismatch, "")
		}

		checks := make([]RequireCap, 0, len(frame.Requires))
		allowed := true
		for _, capName := range frame.Requires {
			granted := cred.Caps.Contains(capName)
			checks = append(checks, RequireCap{Capability: capName, Granted: granted})
			if !granted {
				allowed = false
			}
		}

		req := Request{
			Connection: frame.Connection,
			Epoch:      epoch,
			Credential: cred,
			Body:       append([]byte(nil), frame.Body...),
			Handle:     ph.handle,
			Requires:   checks,
		}
		decision := h.policy.Authorize(req)
		if !allowed || !decision.Allow {
			h.mu.Lock()
			delete(pending, frame.Handle.ID)
			h.mu.Unlock()
			return h.recordDeny(frame, connIdentity.Peer, epoch, cred.Principal, ErrUnauthorized, decision.Reason)
		}

		h.mu.Lock()
		delete(pending, frame.Handle.ID)
		h.mu.Unlock()
		h.record(AuditEntry{
			Connection:     frame.Connection,
			Kind:           FrameBody,
			Epoch:          epoch,
			Principal:      cred.Principal,
			ConnectionPeer: connIdentity.Peer,
			MessagePeer:    frame.Peer,
			Allowed:        true,
			Reason:         decision.Reason,
			HandleID:       frame.Handle.ID,
		})
		return nil

	default:
		return h.recordDeny(frame, connIdentity.Peer, epoch, cred.Principal, ErrUnauthorized, "unknown frame kind")
	}
}

// HandleBatch processes a batch whose frames were received together. Each
// frame is still authorized independently against its own receive-time
// snapshot; one denied frame never aborts the rest.
func (h *Host) HandleBatch(frames []IncomingFrame) []error {
	errs := make([]error, len(frames))
	for i, frame := range frames {
		errs[i] = h.HandleFrame(frame)
	}
	return errs
}

// Pump drives a transport until it errors or the context is canceled.
func (h *Host) Pump(ctx context.Context, transport FrameTransport) error {
	for {
		frame, err := transport.Receive(ctx)
		if err != nil {
			return err
		}
		_ = h.HandleFrame(frame)
	}
}

// Audit returns a copy of the audit trail.
func (h *Host) Audit() []AuditEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]AuditEntry, len(h.audit))
	copy(out, h.audit)
	return out
}

func (h *Host) record(entry AuditEntry) {
	h.mu.Lock()
	h.audit = append(h.audit, entry)
	h.mu.Unlock()
}

func (h *Host) recordDeny(frame IncomingFrame, connPeer PeerCred, epoch uint64, principal string, err error, reason string) error {
	h.record(AuditEntry{
		Connection:     frame.Connection,
		Kind:           frame.Kind,
		Epoch:          epoch,
		Principal:      principal,
		ConnectionPeer: connPeer,
		MessagePeer:    frame.Peer,
		Allowed:        false,
		Err:            err,
		Reason:         reason,
		HandleID:       frame.Handle.ID,
	})
	return err
}
