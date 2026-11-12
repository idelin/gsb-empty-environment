package netsec

import (
	"fmt"
	"sync"
)

// Guard applies endpoint user-token-policy admission to ActivateSession calls.
// A Guard is safe for concurrent use when its NonceStore is shared or when it
// is created through NewGuard.
type Guard struct {
	Endpoint Endpoint
	Nonces   *NonceStore

	nonceMu sync.Mutex
}

// Admission is an alias for Guard.
type Admission = Guard

// NewGuard creates a Guard with its own nonce store. Servers that own multiple
// endpoints should share a single NonceStore so that a nonce cannot be replayed
// through another Guard instance.
func NewGuard(endpoint Endpoint) *Guard {
	return &Guard{
		Endpoint: endpoint,
		Nonces:   NewNonceStore(),
	}
}

// NewGuardWithNonceStore creates a Guard that uses the supplied store. A nil
// store is replaced with a new one.
func NewGuardWithNonceStore(endpoint Endpoint, nonces *NonceStore) *Guard {
	if nonces == nil {
		nonces = NewNonceStore()
	}
	return &Guard{
		Endpoint: endpoint,
		Nonces:   nonces,
	}
}

// ActivateSession performs admission checks and consumes the server nonce for
// successfully signed certificate tokens. It does not validate user passwords
// or certificate trust; those remain the server's authentication and PKI
// responsibilities.
func (g *Guard) ActivateSession(request ActivateSessionRequest) error {
	if g == nil {
		return fmt.Errorf("%w: nil Guard", ErrUnsupportedTokenType)
	}

	switch request.EffectiveTokenType() {
	case TokenTypeUserName:
		return ValidateUserNameToken(g.Endpoint, request.PolicyID, request.EncryptionAlgorithm)

	case TokenTypeCertificate:
		if err := VerifyCertificateToken(g.Endpoint, request); err != nil {
			return err
		}
		nonces := g.nonceStore()
		if !nonces.Consume(request.ServerNonce) {
			return fmt.Errorf("%w: PolicyId %q", ErrNonceReused, request.PolicyID)
		}
		return nil

	case TokenTypeAnonymous:
		_, err := g.Endpoint.LookupUserTokenPolicy(request.PolicyID, TokenTypeAnonymous)
		return err

	case TokenTypeIssuedToken:
		if _, err := g.Endpoint.LookupUserTokenPolicy(request.PolicyID, TokenTypeIssuedToken); err != nil {
			return err
		}
		return fmt.Errorf("%w: %q", ErrUnsupportedTokenType, request.PolicyID)

	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedTokenType, request.EffectiveTokenType())
	}
}

func (g *Guard) nonceStore() *NonceStore {
	g.nonceMu.Lock()
	defer g.nonceMu.Unlock()
	if g.Nonces == nil {
		g.Nonces = NewNonceStore()
	}
	return g.Nonces
}

// Check is an alias for ActivateSession.
func (g *Guard) Check(request ActivateSessionRequest) error {
	return g.ActivateSession(request)
}

// Admit is an alias for ActivateSession.
func (g *Guard) Admit(request ActivateSessionRequest) error {
	return g.ActivateSession(request)
}

// ValidateActivateSession is a one-shot helper. Pass a persistent NonceStore
// when validating multiple requests; a nil store creates a store that lives
// only for this call and therefore cannot detect cross-request replay.
func ValidateActivateSession(endpoint Endpoint, request ActivateSessionRequest, nonces *NonceStore) error {
	return NewGuardWithNonceStore(endpoint, nonces).ActivateSession(request)
}
