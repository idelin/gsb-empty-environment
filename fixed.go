package parity

import (
	"math/big"
	"sync"
)

// ErrRejected is an alias of ErrAuthRequired for hosts that prefer this name.
var ErrRejected = ErrAuthRequired

// SecureCallback is an alternative name for FixedCallback.
type SecureCallback = FixedCallback

// FixedCallback is the remediation reference: before authentication it does
// NOT decrypt and returns the SAME uniform rejection for every input.
//
// The rejection is returned as (ParityRejected, ErrAuthRequired). A caller that
// correctly checks the error can never confuse it with ParityEven. After
// Authenticate, calls are forwarded to the optional inner parity Callback.
type FixedCallback struct {
	mu            sync.Mutex
	authenticated bool
	inner         Callback
}

// NewFixedCallback creates a fixed callback. inner may be nil if the
// authenticated service should not expose parity at all.
func NewFixedCallback(inner Callback) *FixedCallback {
	return &FixedCallback{inner: inner}
}

// NewRejectingCallback returns a fixed callback that uniformly rejects every
// query and never decrypts, even after Authenticate (no inner oracle).
func NewRejectingCallback() *FixedCallback {
	return &FixedCallback{}
}

// NewSecureCallback aliases NewFixedCallback.
func NewSecureCallback(inner Callback) *FixedCallback {
	return NewFixedCallback(inner)
}

// Authenticate enables the post-authentication decryption path.
func (f *FixedCallback) Authenticate() {
	f.mu.Lock()
	f.authenticated = true
	f.mu.Unlock()
}

// IsAuthenticated reports the auth state.
func (f *FixedCallback) IsAuthenticated() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authenticated
}

// QueryParity implements Callback/Oracle.
//
// Before authentication no private operation occurs and every x receives the
// identical ErrAuthRequired rejection, regardless of validity.
func (f *FixedCallback) QueryParity(x *big.Int) (Parity, error) {
	f.mu.Lock()
	auth := f.authenticated
	inner := f.inner
	f.mu.Unlock()
	if !auth {
		return ParityRejected, ErrAuthRequired
	}
	if inner == nil {
		return ParityRejected, ErrNoOracle
	}
	return inner(x)
}

// Callback binds the method as a Callback function value.
func (f *FixedCallback) Callback() Callback { return f.QueryParity }

// UniformReject is a Callback that rejects every query with ErrAuthRequired
// without decrypting anything. It models the fully fixed pre-auth endpoint.
func UniformReject(_ *big.Int) (Parity, error) {
	return ParityRejected, ErrAuthRequired
}

// RejectAll returns a Callback that uniformly rejects with the supplied error
// (defaulting to ErrAuthRequired). Useful for host regression vectors that
// bring their own rejection sentinel.
func RejectAll(err error) Callback {
	reject := err
	if reject == nil {
		reject = ErrAuthRequired
	}
	return func(*big.Int) (Parity, error) {
		return ParityRejected, reject
	}
}
