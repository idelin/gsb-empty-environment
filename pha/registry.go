// Package pha implements the server-side registry for TLS 1.3 post-handshake
// authentication (RFC 8446, Section 4.6.2).
//
// Go's crypto/tls package (as of Go 1.24) does not implement post-handshake
// authentication: its CertificateRequest always carries an empty
// certificate_request_context and the server rejects post-handshake handshake
// messages. This package therefore provides the protocol state machine and
// RFC 8446 message framing itself, behind the Conn transport interface so it
// can be attached to any TLS 1.3 connection implementation.
//
// Security properties enforced here:
//   - A CertificateRequest is only sent on a connection whose ClientHello
//     advertised the post_handshake_auth extension.
//   - Every request gets a fresh, unpredictable context of at least 8 bytes.
//   - At most MaxOutstanding contexts are outstanding per connection.
//   - A client Certificate must echo a currently outstanding context byte for
//     byte; the context is consumed exactly once. Empty, unknown, or repeated
//     contexts are rejected with a fatal illegal_parameter alert.
package pha

import (
	"bytes"
	"errors"
	"sync"
)

// MaxOutstanding is the per-connection limit on unacknowledged
// post-handshake CertificateRequest messages.
const MaxOutstanding = 4

// Sentinel errors. Errors caused by client context violations are
// *AlertError values containing alertIllegalParameter.
var (
	// ErrPostHandshakeAuthNotAdvertised is returned when the peer did not
	// include the post_handshake_auth extension in its ClientHello.
	ErrPostHandshakeAuthNotAdvertised = errors.New("pha: client did not advertise post_handshake_auth")

	// ErrTooManyOutstanding is returned when a connection already holds
	// MaxOutstanding unacknowledged CertificateRequest messages.
	ErrTooManyOutstanding = errors.New("pha: too many outstanding post-handshake requests")

	// ErrUnknownConnection is returned for a Certificate arriving on a
	// connection that has no registry entry.
	ErrUnknownConnection = errors.New("pha: unknown connection")
)

// Conn is the transport a registry drives. Implementations carry traffic inside
// the established TLS 1.3 record layer (application traffic keying material);
// the framing helpers in this package produce the handshake messages.
//
// WriteHandshake sends one framed handshake message. WriteAlert sends a TLS
// alert record (level, description). ReadHandshake blocks until the next
// post-handshake handshake message arrives; non-handshake traffic must be
// demultiplexed by the implementation before reaching the registry.
type Conn interface {
	WriteHandshake(msg []byte) error
	WriteAlert(level, description byte) error
	ReadHandshake() ([]byte, error)
}

// Registry tracks post-handshake authentication state across connections.
// It is safe for concurrent use; a single Registry may serve many connections.
type Registry struct {
	mu    sync.Mutex
	conns map[string]*connection
}

type connection struct {
	conn       Conn
	advertised bool

	// outstanding maps string(context) -> context, so byte-identical contexts
	// compare exactly and each context can only be consumed once.
	outstanding map[string][]byte
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{conns: make(map[string]*connection)}
}

// Register enrolls a connection. id must uniquely identify the TLS connection
// (for example its record-layer connection ID). advertised reports whether the
// ClientHello carried the post_handshake_auth (0x0031) extension.
func (r *Registry) Register(id string, conn Conn, advertised bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[id] = &connection{
		conn:        conn,
		advertised:  advertised,
		outstanding: make(map[string][]byte),
	}
}

// Release removes a connection's state. Pending contexts become unanswerable.
func (r *Registry) Release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, id)
}

// Outstanding reports how many CertificateRequest messages on the connection
// have not yet been answered.
func (r *Registry) Outstanding(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.conns[id]
	if c == nil {
		return 0
	}
	return len(c.outstanding)
}

// CertificateRequest issues a post-handshake CertificateRequest on id. It
// returns the fresh certificate_request_context that was sent, or an error if
// the extension was not advertised or the outstanding limit is reached.
func (r *Registry) CertificateRequest(id string, signatureAlgorithms []uint16) (context []byte, err error) {
	r.mu.Lock()
	c := r.conns[id]
	if c == nil {
		r.mu.Unlock()
		return nil, ErrUnknownConnection
	}
	if !c.advertised {
		r.mu.Unlock()
		return nil, ErrPostHandshakeAuthNotAdvertised
	}
	if len(c.outstanding) >= MaxOutstanding {
		r.mu.Unlock()
		return nil, ErrTooManyOutstanding
	}
	r.mu.Unlock()

	// Generate outside the lock; the state is rechecked under it below.
	ctx, err := randomContext()
	if err != nil {
		return nil, err
	}

	msg, err := MarshalCertificateRequest(ctx, signatureAlgorithms)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	c = r.conns[id]
	if c == nil {
		return nil, ErrUnknownConnection
	}
	if !c.advertised {
		return nil, ErrPostHandshakeAuthNotAdvertised
	}
	if len(c.outstanding) >= MaxOutstanding {
		return nil, ErrTooManyOutstanding
	}
	key := string(ctx)
	if _, exists := c.outstanding[key]; exists {
		return nil, errors.New("pha: duplicate random context")
	}
	if err := c.conn.WriteHandshake(msg); err != nil {
		return nil, err
	}
	c.outstanding[key] = ctx
	return append([]byte(nil), ctx...), nil
}

// HandleCertificate validates a framed post-handshake Certificate message
// against the outstanding context of id. On success the context is consumed
// exactly once and the leaf DER is returned. Any context violation (empty,
// unknown, or already consumed) sends a fatal illegal_parameter alert on the
// connection and returns the corresponding *AlertError.
func (r *Registry) HandleCertificate(id string, record []byte) (leafDER []byte, err error) {
	ctx, der, perr := ParseCertificate(record)
	if perr != nil {
		r.sendFatal(id, perr)
		return nil, perr
	}
	return r.handleParsedCertificate(id, ctx, der)
}

func (r *Registry) handleParsedCertificate(id string, ctx, der []byte) (leafDER []byte, err error) {

	r.mu.Lock()
	c := r.conns[id]
	var alert *AlertError
	var conn Conn
	if c == nil {
		alert = FatalIllegalParameter()
	} else if alert = validateContext(ctx, c); alert == nil {
		delete(c.outstanding, string(ctx))
		conn = c.conn
	} else {
		conn = c.conn
	}
	r.mu.Unlock()

	if alert != nil {
		if conn != nil {
			writeAlert(conn, alert)
		}
		return nil, alert
	}
	return der, nil
}

func writeAlert(conn Conn, alert *AlertError) {
	_ = conn.WriteAlert(alert.Level, alert.Description)
}

// validateContext enforces RFC 8446, Section 4.6.2: the context must be
// non-empty and match an outstanding request of this exact connection.
func validateContext(ctx []byte, c *connection) *AlertError {
	if len(ctx) == 0 {
		return FatalIllegalParameter()
	}
	pending, ok := c.outstanding[string(ctx)]
	if !ok {
		return FatalIllegalParameter()
	}
	if !bytes.Equal(pending, ctx) {
		return FatalIllegalParameter()
	}
	return nil
}

// sendFatal translates err into a TLS alert and sends it outside the registry
// lock, so a slow transport cannot block unrelated connections.
func (r *Registry) sendFatal(id string, err error) {
	r.mu.Lock()
	c := r.conns[id]
	var conn Conn
	if c != nil {
		conn = c.conn
	}
	r.mu.Unlock()
	if conn == nil {
		return
	}
	var alert *AlertError
	if !errors.As(err, &alert) {
		alert = &AlertError{Level: 2, Description: alertDecodeError}
	}
	writeAlert(conn, alert)
}

// Certificate is an accepted post-handshake client certificate.
type Certificate struct {
	Context []byte
	DER     []byte
}

// Serve runs the receive loop for a connection until ReadHandshake fails.
// Valid Certificate messages are validated through HandleCertificate; every
// accepted certificate is delivered on the returned channel. The loop ends
// when the transport errors, the connection is Released, or a fatal alert is
// sent for an invalid message.
func (r *Registry) Serve(id string) (<-chan Certificate, error) {
	r.mu.Lock()
	c := r.conns[id]
	r.mu.Unlock()
	if c == nil {
		return nil, ErrUnknownConnection
	}

	results := make(chan Certificate)
	go func() {
		defer close(results)
		for {
			record, err := c.conn.ReadHandshake()
			if err != nil {
				return
			}
			msgType, _, perr := ParseHandshakeHeader(record)
			if perr != nil {
				r.sendFatal(id, perr)
				return
			}
			if msgType != typeCertificate {
				r.sendFatal(id, &AlertError{Level: 2, Description: alertUnexpectedMessage})
				return
			}
			ctx, der, perr := ParseCertificate(record)
			if perr != nil {
				r.sendFatal(id, perr)
				return
			}
			der, err = r.handleParsedCertificate(id, ctx, der)
			if err != nil {
				return
			}
			results <- Certificate{Context: ctx, DER: der}
		}
	}()
	return results, nil
}
