package netsec

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors are deliberately specific so tunnel hosts can distinguish
// authentication failures from lifecycle failures in logs and metrics.
var (
	ErrDeviceMismatch        = errors.New("netsec: binding belongs to a different device")
	ErrPeerPublicKeyRequired = errors.New("netsec: peer static public key is required")
	ErrBindingNotFound       = errors.New("netsec: binding not found")
	ErrBindingReclaimed      = errors.New("netsec: binding has been reclaimed")

	ErrIndexInUse     = errors.New("netsec: receiver index is in use")
	ErrIndexNotFound  = errors.New("netsec: receiver index not found")
	ErrIndexExhausted = errors.New("netsec: receiver index space exhausted")

	ErrInvalidState = errors.New("netsec: transition is invalid in the current lifecycle state")
	ErrStaleBinding = errors.New("netsec: binding is retired or otherwise stale")
	ErrGeneration   = errors.New("netsec: handshake generation is stale or non-monotonic")

	ErrAuthMismatch          = errors.New("netsec: handshake authentication mismatch")
	ErrMACMismatch           = errors.New("netsec: handshake MAC mismatch")
	ErrTimestampMismatch     = errors.New("netsec: handshake timestamp mismatch")
	ErrHandshakeHashMismatch = errors.New("netsec: handshake hash mismatch")

	ErrResponseAlreadyAccepted = errors.New("netsec: handshake response was already accepted")

	ErrCookieExpectationMissing    = errors.New("netsec: no cookie expectation is bound to this index")
	ErrCookieMACMismatch           = errors.New("netsec: cookie MAC mismatch")
	ErrCookieDigestMismatch        = errors.New("netsec: cookie message digest mismatch")
	ErrCookieSourceFamilyMismatch  = errors.New("netsec: cookie source address family mismatch")
	ErrCookieSourceAddressMismatch = errors.New("netsec: cookie source address mismatch")
	ErrCookieSourcePortMismatch    = errors.New("netsec: cookie source port mismatch")

	ErrAsyncTaskActive = errors.New("netsec: asynchronous cryptographic work is still active")
	ErrMessageBound    = errors.New("netsec: old-key message upper bound has not been reached")
	ErrReclaimTimeout  = errors.New("netsec: index reclaim timeout has not elapsed")

	ErrManagerClosed = errors.New("netsec: manager is closed")
)

func authMismatch(specific error) error {
	return fmt.Errorf("%w: %w", ErrAuthMismatch, specific)
}

func cookieMismatch(specific error) error {
	return fmt.Errorf("%w: %w", ErrAuthMismatch, specific)
}

// ReclaimStatus explains why an index cannot yet be returned to the allocator.
type ReclaimStatus struct {
	Ready                 bool
	Reclaimed             bool
	State                 LifecycleState
	AwaitingTimeout       bool
	AwaitingMessageBound  bool
	AwaitingCrypto        bool
	QuarantinedAt         time.Time
	EarliestReclaimAt     time.Time
	LegacyMessages        uint64
	LegacyMessageLimit    uint64
	LegacyMaxCounter      uint64
	OldKeyMessageLimit    uint64
	OutstandingCryptoWork int
}
