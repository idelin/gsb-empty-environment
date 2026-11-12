package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrNoCredential is returned when the trusted credential source has
	// no snapshot to offer at receive time.
	ErrNoCredential = errors.New("netsec: no trusted credential snapshot available")
	// ErrGenerationMismatch is returned when the handle and the body of a
	// message were stamped with different credential generations.
	ErrGenerationMismatch = errors.New("netsec: handle and body credential generations differ")
	// ErrDenied is returned when a message's receive-time credential
	// snapshot does not satisfy the requested policy.
	ErrDenied = errors.New("netsec: message credential denied by policy")
)

// Conn is an established local connection. Its identity is recorded once
// at accept time and is used for audit only; it never authorizes anything.
type Conn struct {
	auditID Credential
	peer    string
}

// AuditIdentity returns the identity observed when the connection was
// accepted. It is suitable for audit logs only and must not be used as an
// authorization input.
func (c *Conn) AuditIdentity() Credential { return c.auditID }

// Frame is one received protocol frame (a handle descriptor or a body
// chunk) stamped with the credential generation observed at its arrival.
type Frame struct {
	Gen  Generation
	Data []byte
}

// Message is a fully received message: its handle and body plus the
// trusted credential snapshot taken at receive time. Authorization
// decisions are made against this snapshot alone.
type Message struct {
	cred      Credential
	handleGen Generation
	bodyGen   Generation
	handle    []byte
	body      []byte
}

// Credential returns the trusted snapshot this message is authorized with.
func (m *Message) Credential() Credential { return m.cred }

// Handle returns the message handle. Callers must Authorize first.
func (m *Message) Handle() []byte { return m.handle }

// Body returns the message body. Callers must Authorize first.
func (m *Message) Body() []byte { return m.body }

// Policy decides whether a receive-time credential snapshot is allowed to
// perform an operation.
type Policy func(Credential) bool

// RequireUID allows only snapshots matching the given uid.
func RequireUID(uid uint32) Policy {
	return func(c Credential) bool { return c.UID == uid }
}

// Authorize checks the message's receive-time snapshot against p. The
// connection identity is never consulted.
func (m *Message) Authorize(p Policy) error {
	if !p(m.cred) {
		return fmt.Errorf("%w: uid=%d gid=%d gen=%d", ErrDenied, m.cred.UID, m.cred.GID, m.cred.Gen)
	}
	return nil
}

// Snapshotter supplies trusted credential snapshots. *Registry implements
// it; tests may substitute scripted sources.
type Snapshotter interface {
	Snapshot() (Credential, bool)
}

// Host receives messages over connections and stamps every message with a
// fresh trusted credential snapshot.
type Host struct {
	reg Snapshotter
	mu  sync.Mutex
}

// NewHost returns a Host that snapshots credentials from src.
func NewHost(src Snapshotter) *Host { return &Host{reg: src} }

// Accept records the peer's identity for audit and returns the connection.
// The recorded identity is never used for authorization.
func (h *Host) Accept(peer string, id Credential) *Conn {
	return &Conn{auditID: id, peer: peer}
}

// snapshot returns the current trusted credential or ErrNoCredential.
func (h *Host) snapshot() (Credential, error) {
	cred, ok := h.reg.Snapshot()
	if !ok {
		return Credential{}, ErrNoCredential
	}
	return cred, nil
}

// Receive takes one message (handle plus body) on conn. Both parts are
// stamped with the same generation from a single snapshot, and the
// snapshot becomes the message's only authorization basis.
func (h *Host) Receive(conn *Conn, handle, body []byte) (*Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cred, err := h.snapshot()
	if err != nil {
		return nil, err
	}
	return &Message{
		cred:      cred,
		handleGen: cred.Gen,
		bodyGen:   cred.Gen,
		handle:    handle,
		body:      body,
	}, nil
}

// Assemble combines a separately received handle frame and body frame into
// one message. The two frames must belong to the same credential
// generation; a mixed-generation message is rejected.
func (h *Host) Assemble(conn *Conn, handleFrame, bodyFrame Frame) (*Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if handleFrame.Gen != bodyFrame.Gen {
		return nil, fmt.Errorf("%w: handle gen=%d body gen=%d",
			ErrGenerationMismatch, handleFrame.Gen, bodyFrame.Gen)
	}
	cred, err := h.snapshot()
	if err != nil {
		return nil, err
	}
	if cred.Gen != handleFrame.Gen {
		return nil, fmt.Errorf("%w: frame gen=%d current gen=%d",
			ErrGenerationMismatch, handleFrame.Gen, cred.Gen)
	}
	return &Message{
		cred:      cred,
		handleGen: handleFrame.Gen,
		bodyGen:   bodyFrame.Gen,
		handle:    handleFrame.Data,
		body:      bodyFrame.Data,
	}, nil
}

// Envelope is one item of a batch: a handle and a body received together.
type Envelope struct {
	Handle []byte
	Body   []byte
}

// ReceiveBatch receives a batch of messages, snapshotting credentials
// independently for every item. A demotion landing in the middle of a
// batch therefore takes effect immediately for all later items; items
// received while credentials are missing fail individually without
// aborting the rest of the batch.
func (h *Host) ReceiveBatch(conn *Conn, items []Envelope) ([]*Message, []error) {
	msgs := make([]*Message, len(items))
	errs := make([]error, len(items))
	for i, env := range items {
		msgs[i], errs[i] = h.Receive(conn, env.Handle, env.Body)
	}
	return msgs, errs
}
