// Package netsec provides HTTP entry middleware that binds an HTTP request to
// the identity established by the underlying TLS connection.
//
// On a multiplexed HTTP/2 connection the TLS handshake happens once: a client
// can present A's SNI/certificate in the ClientHello and then send requests
// whose ":authority" names a different virtual host B. The Binder closes that
// confusion by requiring every request host to be covered by the SAN extension
// of the leaf certificate served on the very same connection, and by requiring
// an explicit caller policy before two distinct hosts may share one
// connection. Virtual hosts that demand client certificates are additionally
// pinned to their own handshake. Any violation is answered with HTTP 421
// (Misdirected Request).
package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
)

// Rejection reasons, exposed for logging/metrics through IdentityError.Reason.
const (
	// ReasonNonTLS: the request did not arrive on a TLS connection.
	ReasonNonTLS = "non_tls"
	// ReasonInvalidAuthority: the request authority could not be canonicalized.
	ReasonInvalidAuthority = "invalid_authority"
	// ReasonMissingSNI: the handshake carried no usable server name.
	ReasonMissingSNI = "missing_sni"
	// ReasonInvalidSNI: the handshake server name could not be canonicalized.
	ReasonInvalidSNI = "invalid_sni"
	// ReasonNoLeaf: no verified server leaf certificate is known for the conn.
	ReasonNoLeaf = "no_leaf_certificate"
	// ReasonClientCertHandshake: a client-cert vhost was reached over a
	// handshake authenticated for another host or carrying a different client
	// certificate.
	ReasonClientCertHandshake = "client_cert_own_handshake"
	// ReasonCoalesceDenied: the policy does not allow the two hosts to merge.
	ReasonCoalesceDenied = "coalesce_denied"
	// ReasonSANMismatch: the leaf certificate SAN does not cover the host.
	ReasonSANMismatch = "san_mismatch"
)

// IdentityError describes a rejected request. It is returned from Check and
// passed to the optional OnReject observer.
type IdentityError struct {
	// Reason is one of the Reason* constants.
	Reason string
	// HandshakeHost is the canonicalized SNI (empty when unavailable).
	HandshakeHost string
	// RequestHost is the canonicalized request authority (empty when it could
	// not be parsed).
	RequestHost string
	// RemoteAddr is the peer address, copied from the request when present.
	RemoteAddr string
}

func (e *IdentityError) Error() string {
	return "netsec: connection identity binding rejected (" + e.Reason + ")"
}

// LeafResolver returns the server leaf certificate that was selected during
// the TLS handshake of the request's connection. Returning (nil, nil) is
// treated as "no certificate available" and fails closed.
//
// The standard library does not expose the selected server certificate on
// tls.ConnectionState; implementations typically record it in tls.Config's
// GetCertificate/GetConfigForClient keyed by SNI. SNICertTracker is a
// ready-to-use implementation.
type LeafResolver interface {
	LeafForConnection(*tls.ConnectionState) (*x509.Certificate, error)
}

// LeafResolverFunc adapts a plain function to LeafResolver.
type LeafResolverFunc func(*tls.ConnectionState) (*x509.Certificate, error)

// LeafForConnection implements LeafResolver.
func (f LeafResolverFunc) LeafForConnection(state *tls.ConnectionState) (*x509.Certificate, error) {
	return f(state)
}

// CoalescePolicy decides whether two distinct canonical hosts may be served
// over one connection. The arguments are the handshake SNI host and the
// request authority host; they are never empty when invoked. A nil policy
// allows only identical hosts (no coalescing of distinct names).
type CoalescePolicy func(handshakeHost, requestHost string) bool

// ClientCertPolicy reports whether the named virtual host requires a client
// (mTLS) certificate. Such hosts may only be reached on a handshake dedicated
// to that host.
type ClientCertPolicy func(host string) bool

// Binder is the connection-identity binding middleware. It is safe for
// concurrent use once constructed.
type Binder struct {
	leaf          LeafResolver
	coalesce      CoalescePolicy
	clientCertReq ClientCertPolicy
	onReject      func(*http.Request, *IdentityError)
}

// Option customizes a Binder.
type Option func(*Binder)

// WithLeafResolver supplies the source of the server leaf certificate selected
// at handshake time. Required: without it every request fails closed.
func WithLeafResolver(resolver LeafResolver) Option {
	return func(b *Binder) { b.leaf = resolver }
}

// WithCoalescePolicy installs the policy that permits two distinct hosts to
// share a connection.
func WithCoalescePolicy(policy CoalescePolicy) Option {
	return func(b *Binder) { b.coalesce = policy }
}

// WithClientCertPolicy marks virtual hosts that require a client certificate
// and therefore their own dedicated handshake.
func WithClientCertPolicy(policy ClientCertPolicy) Option {
	return func(b *Binder) { b.clientCertReq = policy }
}

// WithRejectObserver receives every rejection before the 421 is written. It is
// intended for structured logging/metrics and must not write to the response.
func WithRejectObserver(observer func(*http.Request, *IdentityError)) Option {
	return func(b *Binder) { b.onReject = observer }
}

// New builds a Binder from the supplied options.
func New(opts ...Option) *Binder {
	b := &Binder{}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Wrap returns the request-entry middleware.
func (b *Binder) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := b.Check(r); err != nil {
			b.reject(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Check applies the binding rules to a single request and returns an
// *IdentityError when the request must be refused with 421.
func (b *Binder) Check(r *http.Request) *IdentityError {
	reqHost, err := canonicalRequestHost(r)
	identity := &IdentityError{RequestHost: reqHost, RemoteAddr: r.RemoteAddr}
	if err != nil {
		identity.Reason = ReasonInvalidAuthority
		return identity
	}

	state := r.TLS
	if state == nil {
		identity.Reason = ReasonNonTLS
		identity.RequestHost = reqHost
		return identity
	}

	if state.ServerName == "" {
		identity.Reason = ReasonMissingSNI
		identity.RequestHost = reqHost
		return identity
	}
	sniHost, err := canonicalHost(state.ServerName)
	if err != nil {
		identity.Reason = ReasonInvalidSNI
		identity.RequestHost = reqHost
		return identity
	}
	identity.HandshakeHost = sniHost

	if b.leaf == nil {
		identity.Reason = ReasonNoLeaf
		return identity
	}
	leaf, err := b.leaf.LeafForConnection(state)
	if err != nil || leaf == nil {
		identity.Reason = ReasonNoLeaf
		return identity
	}

	distinctHosts := sniHost != reqHost

	// A virtual host requiring a client certificate must use its own handshake:
	// reject any cross-host request, and reject reuse whenever the connection
	// actually carries a verified client certificate for another host.
	if distinctHosts {
		requiresClientCert := b.clientCertReq != nil && b.clientCertReq(reqHost)
		if requiresClientCert || hasVerifiedPeerCert(state) {
			identity.Reason = ReasonClientCertHandshake
			return identity
		}
		if b.coalesce == nil || !b.coalesce(sniHost, reqHost) {
			identity.Reason = ReasonCoalesceDenied
			return identity
		}
	}

	if !leafCoversHost(leaf, reqHost) {
		identity.Reason = ReasonSANMismatch
		return identity
	}

	return nil
}

func (b *Binder) reject(w http.ResponseWriter, r *http.Request, identity *IdentityError) {
	if b.onReject != nil {
		b.onReject(r, identity)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusMisdirectedRequest)
	_, _ = w.Write([]byte("421 Misdirected Request: connection identity does not match request host\n"))
}

// canonicalRequestHost extracts and canonicalizes the request authority host,
// stripping any port and surrounding brackets.
func canonicalRequestHost(r *http.Request) (string, error) {
	authority := r.Host
	if authority == "" && r.URL != nil {
		authority = r.URL.Host
	}
	if authority == "" {
		return "", errEmptyHost
	}
	return canonicalHost(hostOnly(authority))
}

// hasVerifiedPeerCert reports whether the connection completed mutual TLS with
// a verified client certificate.
func hasVerifiedPeerCert(state *tls.ConnectionState) bool {
	return peerLeaf(state) != nil
}

// peerLeaf returns the client leaf presented on the connection when one is
// available. Verified chains are preferred; raw peer certificates are used as
// a fallback so policy failures also prevent silent cross-host reuse.
func peerLeaf(state *tls.ConnectionState) *x509.Certificate {
	if state == nil {
		return nil
	}
	if len(state.VerifiedChains) > 0 && len(state.VerifiedChains[0]) > 0 {
		return state.VerifiedChains[0][0]
	}
	if len(state.PeerCertificates) > 0 {
		return state.PeerCertificates[0]
	}
	return nil
}
