// Package pqpin implements post-quantum key-exchange capability pinning for
// TLS 1.3 clients.
//
// An online network attacker can strip the hybrid X25519MLKEM768 group from a
// ClientHello so that a handshake which should negotiate a post-quantum hybrid
// key exchange silently falls back to a classical group such as X25519. After
// a server has once completed a verified hybrid handshake, Pinner remembers
// that capability for a configurable lifetime and aborts later connections to
// the same endpoint whose ServerHello, HelloRetryRequest, or resumed session
// no longer uses a hybrid key exchange.
//
// Pins are only written by Commit, which must be called after the TLS
// handshake completes and the Finished messages have been verified (that is,
// after a successful tls.Conn.Handshake). A handshake that fails, or one in
// which a downgrade was detected, never creates or refreshes a pin.
package pqpin

import (
	"crypto/tls"
	"net"
	"sync"
	"time"
)

// DefaultTTL is the pin lifetime used by New when no WithTTL option is given.
const DefaultTTL = 7 * 24 * time.Hour

// hybridGroups lists the post-quantum hybrid key exchange groups recognized by
// this package. The values match the TLS NamedGroup registry; see
// tls.X25519MLKEM768 in Go 1.24+.
var hybridGroups = map[uint16]bool{
	uint16(tls.X25519MLKEM768): true, // 0x11EC, X25519+ML-KEM-768
	// Future hybrid groups (e.g. X25519MLKEM1024, SecP256r1MLKEM768) are
	// added here as the standard library gains CurveID constants for them.
}

// IsHybridGroup reports whether group is a recognized post-quantum hybrid
// TLS key exchange group.
func IsHybridGroup(group uint16) bool {
	return hybridGroups[group]
}

// PinRecord is the pinned capability for one server endpoint.
type PinRecord struct {
	// Group is the hybrid group negotiated by the handshake that wrote the pin.
	Group uint16
	// CreatedAt is the time the pin was first written (unix nanoseconds).
	CreatedAt int64
	// ExpiresAt is the end of the pin lifetime (unix nanoseconds).
	ExpiresAt int64
}

// Expired reports whether the pin is expired at time now. The boundary is
// inclusive: a pin is valid at exactly ExpiresAt and expired afterwards.
func (p *PinRecord) Expired(now time.Time) bool {
	return now.UnixNano() > p.ExpiresAt
}

// Observation is the evidence gathered from one connection attempt. The
// connection-establishment code (or the Dialer in this package) fills it after
// the TLS handshake and hands it to Evaluate and Commit.
type Observation struct {
	// ServerID identifies the endpoint, normally host:port.
	ServerID string
	// NegotiatedGroup is the key share group selected in the final
	// ServerHello (RFC 8446 key_share.server_share.group). Zero if no key
	// share was observed (for example a TLS 1.2 RSA key exchange).
	NegotiatedGroup uint16
	// HRRGroup is the group named in the HelloRetryRequest key_share
	// extension, when an HRR was sent. Zero when there was no HRR.
	HRRGroup uint16
	// HadHRR reports whether a HelloRetryRequest was observed.
	HadHRR bool
	// Resumed reports whether the connection used session resumption (PSK).
	Resumed bool
	// ResumeHybrid reports, when Resumed is true, whether the original
	// handshake that minted the resumed session used a hybrid group.
	ResumeHybrid bool
}

// Pinner stores hybrid-capability pins and enforces them against connection
// observations. It is safe for concurrent use.
type Pinner struct {
	mu   sync.RWMutex
	pins map[string]*PinRecord
	ttl  time.Duration
	now  func() time.Time
}

// Option configures a Pinner.
type Option func(*Pinner)

// WithTTL sets the pin lifetime. Non-positive values fall back to DefaultTTL.
func WithTTL(ttl time.Duration) Option {
	return func(p *Pinner) {
		if ttl > 0 {
			p.ttl = ttl
		}
	}
}

// WithClock overrides the time source, primarily for testing expiry.
func WithClock(now func() time.Time) Option {
	return func(p *Pinner) {
		if now != nil {
			p.now = now
		}
	}
}

// New creates a Pinner with the given options.
func New(opts ...Option) *Pinner {
	p := &Pinner{
		pins: make(map[string]*PinRecord),
		ttl:  DefaultTTL,
		now:  time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Lookup returns a copy of the pin for serverID, or nil when there is no pin
// or the existing pin has expired. Expired pins are removed lazily.
func (p *Pinner) Lookup(serverID string) *PinRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.pins[serverID]
	if rec == nil {
		return nil
	}
	if rec.Expired(p.now()) {
		delete(p.pins, serverID)
		return nil
	}
	cp := *rec
	return &cp
}

// Evaluate enforces the pin against the evidence of a connection attempt. It
// returns a *DowngradeError when an unexpired pin exists and the connection
// downgraded away from a hybrid key exchange. When no pin exists, or the pin
// has expired, the connection is allowed and nil is returned.
//
// Evaluate does not modify the store: call Commit separately only after the
// handshake has completed and Finished has been verified.
func (p *Pinner) Evaluate(obs Observation) error {
	rec := p.Lookup(obs.ServerID)
	if rec == nil {
		return nil
	}

	// The HelloRetryRequest check happens first: an attacker-chosen classic
	// group in the HRR already forces the connection onto a non-hybrid path.
	if obs.HadHRR && !IsHybridGroup(obs.HRRGroup) {
		return &DowngradeError{
			ServerID:      obs.ServerID,
			Reason:        ReasonHRR,
			SelectedGroup: obs.HRRGroup,
			ExpiresAt:     rec.ExpiresAt,
		}
	}

	if obs.Resumed {
		if !obs.ResumeHybrid {
			return &DowngradeError{
				ServerID:  obs.ServerID,
				Reason:    ReasonResume,
				ExpiresAt: rec.ExpiresAt,
			}
		}
		return nil
	}

	if !IsHybridGroup(obs.NegotiatedGroup) {
		return &DowngradeError{
			ServerID:      obs.ServerID,
			Reason:        ReasonServerHello,
			SelectedGroup: obs.NegotiatedGroup,
			ExpiresAt:     rec.ExpiresAt,
		}
	}
	return nil
}

// Commit writes or refreshes the pin for the observed server. It must only be
// called after the TLS handshake has completed successfully, meaning the
// peer's Finished message was verified. It refuses to write anything unless
// the observed key exchange is itself a hybrid group, so a classic handshake
// can never create, refresh, or extend a pin.
func (p *Pinner) Commit(obs Observation) error {
	if obs.ServerID == "" {
		return errEmptyServerID
	}
	if !IsHybridGroup(obs.NegotiatedGroup) {
		return &DowngradeError{
			ServerID:      obs.ServerID,
			Reason:        ReasonServerHello,
			SelectedGroup: obs.NegotiatedGroup,
		}
	}

	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()

	rec := p.pins[obs.ServerID]
	if rec == nil {
		rec = &PinRecord{
			Group:     obs.NegotiatedGroup,
			CreatedAt: now.UnixNano(),
		}
		p.pins[obs.ServerID] = rec
	} else {
		rec.Group = obs.NegotiatedGroup
	}
	rec.ExpiresAt = now.Add(p.ttl).UnixNano()
	return nil
}

// Delete removes the pin for serverID, if any.
func (p *Pinner) Delete(serverID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.pins, serverID)
}

// NormalizeServerID canonicalizes a host and port into the host:port key used
// by the store. A missing port defaults to 443, matching tls.Dial.
func NormalizeServerID(host string, port string) string {
	if port == "" {
		port = "443"
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "[" + host + "]:" + port
	}
	return net.JoinHostPort(host, port)
}
