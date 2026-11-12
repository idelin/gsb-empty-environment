package ticketadmit

import (
	"crypto/tls"
	"errors"
)

// extraTag versions the binding entry stored in SessionState.Extra.
var extraTag = []byte("ticketadmit/v1")

// VhostPolicyFunc returns the admission posture for the virtual host selected
// by the current ClientHello (identified by sni). Implementations commonly
// map this to the tls.Config that would serve the virtual host.
type VhostPolicyFunc func(sni string) VhostPolicy

// TLSAdapter wires an Admitter into crypto/tls as WrapSession and
// UnwrapSession callbacks. It enforces the virtual-host binding on resumption
// and guarantees that tickets it cannot authenticate never abort the
// handshake: they are ignored and crypto/tls falls back to a full handshake.
type TLSAdapter struct {
	admitter *Admitter
	policy   VhostPolicyFunc
}

// NewTLSAdapter builds an adapter. policy must be non-nil.
func NewTLSAdapter(admitter *Admitter, policy VhostPolicyFunc) (*TLSAdapter, error) {
	if admitter == nil {
		return nil, ErrInvalidKey
	}
	if policy == nil {
		return nil, errors.New("ticketadmit: nil VhostPolicyFunc")
	}
	return &TLSAdapter{admitter: admitter, policy: policy}, nil
}

// Configure installs the adapter callbacks on cfg.
func (a *TLSAdapter) Configure(cfg *tls.Config) {
	cfg.WrapSession = a.WrapSession
	cfg.UnwrapSession = a.UnwrapSession
}

// WrapSession seals a tls.SessionState together with the binding claims for
// the connection that produced it.
func (a *TLSAdapter) WrapSession(cs tls.ConnectionState, state *tls.SessionState) ([]byte, error) {
	var fingerprint []byte
	if len(cs.PeerCertificates) > 0 {
		fingerprint = CertificateFingerprint(cs.PeerCertificates[0])
	}

	claims := Claims{
		SNI:                   cs.ServerName,
		ALPN:                  cs.NegotiatedProtocol,
		ClientCertFingerprint: fingerprint,
		CipherSuite:           cs.CipherSuite,
	}
	ticket, err := a.admitter.Issue(claims)
	if err != nil {
		return nil, err
	}

	// Store the binding alongside the session state. It travels inside the
	// encrypted ticket and is re-validated on unwrap.
	entry := make([]byte, 0, len(extraTag)+len(ticket.Raw))
	entry = append(entry, extraTag...)
	entry = append(entry, ticket.Raw...)
	state.Extra = append(state.Extra, entry)
	return state.Bytes()
}

// UnwrapSession decrypts and admits a presented ticket. A ticket that cannot
// be authenticated or that fails policy returns (nil, nil) so crypto/tls
// ignores it and performs a full handshake. This implementation never returns
// a non-nil error for an unrecognised ticket, preserving the "an unknown
// ticket must not terminate the handshake" guarantee.
func (a *TLSAdapter) UnwrapSession(identity []byte, cs tls.ConnectionState) (*tls.SessionState, error) {
	state, err := tls.ParseSessionState(identity)
	if err != nil || state == nil {
		return nil, nil
	}

	rawTicket, ok := extractTicket(state.Extra)
	if !ok {
		return nil, nil
	}

	policy := a.policy(cs.ServerName)
	result := a.admitter.Admit(AdmitRequest{
		RawTicket: rawTicket,
		SNI:       cs.ServerName,
		ALPN:      cs.NegotiatedProtocol,
		Policy:    policy,
	})
	if result.Decision == DecisionReject {
		return nil, nil
	}
	return state, nil
}

// extractTicket locates the binding entry produced by WrapSession.
func extractTicket(extra [][]byte) ([]byte, bool) {
	for _, entry := range extra {
		if len(entry) >= len(extraTag) && string(entry[:len(extraTag)]) == string(extraTag) {
			return entry[len(extraTag):], true
		}
	}
	return nil, false
}
