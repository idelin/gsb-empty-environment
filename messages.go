package netsec

import (
	"net/netip"
)

// ResponseAuth is the tuple that a valid handshake response must match for a
// particular binding.
type ResponseAuth struct {
	MAC           MAC
	Timestamp     Timestamp
	HandshakeHash HandshakeHash
}

// IsZero reports whether all response authentication fields are zero.
func (a ResponseAuth) IsZero() bool {
	return a.MAC == (MAC{}) && a.Timestamp.IsZero() && a.HandshakeHash == (HandshakeHash{})
}

// Equal reports whether two authentication tuples are equal.
func (a ResponseAuth) Equal(other ResponseAuth) bool {
	return a.MAC == other.MAC && a.Timestamp == other.Timestamp && a.HandshakeHash == other.HandshakeHash
}

// CookieNATPolicy selects how cookie source addresses are compared after a
// NAT rebinding.
type CookieNATPolicy uint8

const (
	// CookieNATStrict requires the exact source IP address and port. This is
	// the safe default and matches WireGuard's address-bound cookie behavior.
	CookieNATStrict CookieNATPolicy = iota
	// CookieNATAllowPortRebind accepts the same source IP address with a
	// different source port. This is the explicit policy for common NAT port
	// rebinding.
	CookieNATAllowPortRebind
	// CookieNATAllowPortAndIPRebind accepts any source endpoint in the same
	// address family. It does not permit crossing IPv4/IPv6 families.
	CookieNATAllowPortAndIPRebind
)

// String returns a stable policy name.
func (p CookieNATPolicy) String() string {
	switch p {
	case CookieNATStrict:
		return "strict"
	case CookieNATAllowPortRebind:
		return "allow-port-rebind"
	case CookieNATAllowPortAndIPRebind:
		return "allow-port-and-ip-rebind"
	default:
		return "unknown"
	}
}

// CookieAuth binds a cookie reply to the original message digest and source.
type CookieAuth struct {
	MAC           MAC
	MessageDigest MessageDigest
	Source        netip.AddrPort
	// Generation and HandshakeHash are optional binding-identity extensions.
	// When set, they prevent a cookie from a previous index owner from being
	// accepted solely because the numeric index, source, and offending-message
	// digest collide.
	Generation    uint64
	HandshakeHash HandshakeHash
}

// NewCookieAuth constructs and normalizes a cookie expectation.
func NewCookieAuth(mac MAC, digest MessageDigest, source netip.AddrPort) CookieAuth {
	return CookieAuth{
		MAC:           mac,
		MessageDigest: digest,
		Source:        normalizeAddrPort(source),
	}
}

// SourceFamily returns the family of the bound source address.
func (a CookieAuth) SourceFamily() AddressFamily {
	if !a.Source.IsValid() {
		return AddressFamilyUnknown
	}
	return FamilyOf(a.Source.Addr())
}

// HandshakeResponse is the subset of a WireGuard handshake response needed to
// authenticate it against a receiver-index capability. ReceiverIndex is an
// alias accepted for host adapters that already use that field name.
type HandshakeResponse struct {
	Index         Index
	ReceiverIndex Index
	Generation    uint64
	MAC           MAC
	MAC1          MAC
	Timestamp     Timestamp
	HandshakeHash HandshakeHash
}

func (r HandshakeResponse) effectiveIndex() Index {
	if r.ReceiverIndex != 0 {
		return r.ReceiverIndex
	}
	return r.Index
}

func (r HandshakeResponse) effectiveMAC() MAC {
	if r.MAC != (MAC{}) {
		return r.MAC
	}
	return r.MAC1
}

// CookieReply is a received cookie reply. Source is the endpoint observed on
// the wire. The address family is always checked; port handling follows
// Manager.CookieNATPolicy.
type CookieReply struct {
	Index         Index
	ReceiverIndex Index
	Generation    uint64
	MAC           MAC
	MAC1          MAC
	MessageDigest MessageDigest
	HandshakeHash HandshakeHash
	Source        netip.AddrPort
}

func (r CookieReply) effectiveIndex() Index {
	if r.ReceiverIndex != 0 {
		return r.ReceiverIndex
	}
	return r.Index
}

func (r CookieReply) effectiveMAC() MAC {
	if r.MAC != (MAC{}) {
		return r.MAC
	}
	return r.MAC1
}

// SetResponseAuth updates the expected response tuple while the binding is
// still waiting for its first response. Real hosts should derive this tuple
// from the authenticated Noise handshake.
func (m *Manager) SetResponseAuth(b *Binding, auth ResponseAuth) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != StateInitiationSent {
		return ErrInvalidState
	}
	b.response = auth
	return nil
}

// SetCookieAuth binds a cookie expectation to a binding. It is only legal
// while the initiation is still outstanding.
func (m *Manager) SetCookieAuth(b *Binding, auth CookieAuth) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	if !auth.Source.IsValid() {
		return ErrCookieSourceAddressMismatch
	}
	if auth.Generation != 0 && auth.Generation != b.generation {
		return ErrGeneration
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != StateInitiationSent {
		return ErrInvalidState
	}
	if auth.HandshakeHash != (HandshakeHash{}) && auth.HandshakeHash != b.response.HandshakeHash {
		return ErrHandshakeHashMismatch
	}
	auth.Source = normalizeAddrPort(auth.Source)
	if auth.Generation == 0 {
		auth.Generation = b.generation
	}
	if auth.HandshakeHash == (HandshakeHash{}) {
		auth.HandshakeHash = b.response.HandshakeHash
	}
	b.cookie = auth
	b.hasCookie = true
	return nil
}

// HandleResponse authenticates a handshake response and, on success, moves
// exactly one matching initiation from InitiationSent to ResponseAccepted.
// Several bindings may share one numeric index during a forced collision; only
// the binding whose MAC, timestamp, and handshake hash all match is changed.
func (m *Manager) HandleResponse(msg HandshakeResponse) (*Binding, error) {
	idx := msg.effectiveIndex()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	candidates := m.byIndex[idx]
	if len(candidates) == 0 {
		return nil, ErrIndexNotFound
	}

	var mismatch error
	for i := len(candidates) - 1; i >= 0; i-- {
		b := candidates[i]
		b.mu.Lock()
		if msg.Generation != 0 && msg.Generation != b.generation {
			if mismatch == nil {
				mismatch = ErrGeneration
			}
			b.mu.Unlock()
			continue
		}
		authErr := b.matchesResponseLocked(msg)
		if authErr != nil {
			if mismatch == nil {
				mismatch = authErr
			}
			b.mu.Unlock()
			continue
		}

		switch b.state {
		case StateInitiationSent:
			now := m.now()
			if err := b.transitionLocked(StateResponseAccepted, now, "response accepted"); err != nil {
				b.mu.Unlock()
				return nil, err
			}
			b.mu.Unlock()
			return b, nil
		case StateResponseAccepted:
			b.mu.Unlock()
			return nil, ErrResponseAlreadyAccepted
		case StateKeypairActive:
			b.mu.Unlock()
			return nil, ErrResponseAlreadyAccepted
		default:
			b.mu.Unlock()
			return nil, ErrStaleBinding
		}
	}
	if mismatch != nil {
		return nil, authMismatch(mismatch)
	}
	return nil, ErrIndexNotFound
}

// HandleCookie authenticates a cookie reply against the binding that created
// the initiation. A cookie reply does not advance the lifecycle state, but a
// successful match records that the challenge was answered.
func (m *Manager) HandleCookie(msg CookieReply) (*Binding, error) {
	idx := msg.effectiveIndex()
	source := normalizeAddrPort(msg.Source)
	if !source.IsValid() {
		return nil, ErrCookieSourceAddressMismatch
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	candidates := m.byIndex[idx]
	if len(candidates) == 0 {
		return nil, ErrIndexNotFound
	}

	var mismatch error
	for i := len(candidates) - 1; i >= 0; i-- {
		b := candidates[i]
		b.mu.Lock()
		if !b.hasCookie {
			if mismatch == nil {
				mismatch = ErrCookieExpectationMissing
			}
			b.mu.Unlock()
			continue
		}
		authErr := b.matchesCookieLocked(msg, m.cookieNAT, source)
		if authErr != nil {
			if mismatch == nil {
				mismatch = authErr
			}
			b.mu.Unlock()
			continue
		}
		if b.state != StateInitiationSent {
			b.mu.Unlock()
			return nil, ErrStaleBinding
		}
		b.cookiesSeen++
		b.lastCookieAt = m.now()
		b.mu.Unlock()
		return b, nil
	}
	if mismatch != nil {
		return nil, cookieMismatch(mismatch)
	}
	return nil, ErrIndexNotFound
}

// CookieNATPolicy returns the manager's explicit NAT rebinding policy.
func (m *Manager) CookieNATPolicy() CookieNATPolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cookieNAT
}

// SetCookieNATPolicy changes the source-port/IP rebinding policy. It affects
// subsequent cookie validations and never changes lifecycle state.
func (m *Manager) SetCookieNATPolicy(policy CookieNATPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cookieNAT = policy
}
