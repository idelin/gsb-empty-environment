package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"time"
)

// KeyID names an entry in the gateway's fixed master-key table.  A response
// may carry only this identifier; it can never supply key material.
type KeyID string

// Challenge is the material sent to the master for a pending high-risk
// request.  It contains no secret key bytes.
type Challenge struct {
	ChallengeID   ChallengeID
	KeyID         KeyID
	Nonce         [32]byte
	Deadline      time.Time
	PolicyVersion string
	Identity      RequestIdentity
}

// Response is the master's authentication response.
type Response struct {
	ChallengeID ChallengeID
	KeyID       KeyID
	MAC         [32]byte
}

// Execution is passed to the operation callback after a response has been
// verified and the pending challenge has been atomically moved to Authorized.
type Execution struct {
	ChallengeID   ChallengeID
	KeyID         KeyID
	Nonce         [32]byte
	PolicyVersion string
	Identity      RequestIdentity
}

// MACInput returns the exact MAC-covered bytes for this execution.
func (e Execution) MACInput() []byte {
	return MACInput(e.Identity, e.Nonce, e.ChallengeID, e.KeyID)
}

// Executor performs the originally challenged operation exactly once.
type Executor func(Execution) ([]byte, error)

// Receipt is the durable result of an authorised execution.  A retransmitted
// valid response returns this same receipt.  ExecutionError records a failure
// reported by the operation callback; Respond itself returns nil once the
// response was authorised and the receipt was stored.
type Receipt struct {
	ChallengeID    ChallengeID
	KeyID          KeyID
	PolicyVersion  string
	Identity       RequestIdentity
	Payload        []byte
	ExecutionError string
	ExecutedAt     time.Time
}

// Clone returns a deep copy of the receipt.
func (r Receipt) Clone() Receipt {
	out := r
	out.Identity = r.Identity.Clone()
	out.Payload = cloneBytes(r.Payload)
	return out
}

// ChallengeState is the lifecycle state of one challenge.
type ChallengeState string

const (
	StatePending    ChallengeState = "pending"
	StateAuthorized ChallengeState = "authorized"
	StateCompleted  ChallengeState = "completed"
	StateCanceled   ChallengeState = "canceled"
)

// CancelReason explains why a pending challenge was discarded.
type CancelReason string

const (
	CancelExpired          CancelReason = "expired"
	CancelConnectionReset  CancelReason = "connection_reset"
	CancelSequenceReuse    CancelReason = "application_sequence_reuse"
	CancelPolicyChange     CancelReason = "policy_change"
	CancelSequenceOccupied CancelReason = "application_sequence_occupied"
	CancelManual           CancelReason = "manual"
)

// ChallengeStatus is a read-only snapshot of a challenge.
type ChallengeStatus struct {
	State        ChallengeState
	CancelReason CancelReason
	Deadline     time.Time
	Identity     RequestIdentity
}

// ComputeResponseMAC computes HMAC-SHA256 over MACInput with a key taken from
// the fixed key table.  The response structure cannot carry key material.
func ComputeResponseMAC(secret []byte, id RequestIdentity, nonce [32]byte, challengeID ChallengeID, keyID KeyID) [32]byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(MACInput(id, nonce, challengeID, keyID))
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

var (
	ErrInvalidConfig         = errors.New("netsec: invalid gateway configuration")
	ErrInvalidRequest        = errors.New("netsec: invalid request identity")
	ErrInvalidChallengeID    = errors.New("netsec: invalid challenge id")
	ErrRequestDigestMismatch = errors.New("netsec: request digest does not match frozen identity")
	ErrNoKeys                = errors.New("netsec: no master keys configured")
	ErrKeyNotConfigured      = errors.New("netsec: no key configured for master")
	ErrKeyNotFound           = errors.New("netsec: challenge key id is not in the fixed key table")
	ErrKeyMismatch           = errors.New("netsec: response key id does not match challenge")
	ErrUnknownChallenge      = errors.New("netsec: unknown challenge id")
	ErrChallengeCanceled     = errors.New("netsec: challenge canceled")
	ErrChallengeExpired      = errors.New("netsec: challenge deadline elapsed")
	ErrAuthenticationFailed  = errors.New("netsec: challenge response authentication failed")
	ErrRateLimited           = errors.New("netsec: master failed-response rate limit exceeded")
	ErrSequenceReuse         = errors.New("netsec: application sequence reused")
	ErrSequenceOccupied      = errors.New("netsec: application sequence occupied by another request")
	ErrConnectionReset       = errors.New("netsec: connection generation has been reset")
	ErrTooManyPending        = errors.New("netsec: too many pending challenges on connection")
	ErrRandomSource          = errors.New("netsec: random source failed")
	ErrFragmentGap           = errors.New("netsec: fragment index gap")
	ErrDuplicateFragment     = errors.New("netsec: duplicate fragment index")
	ErrFragmentMismatch      = errors.New("netsec: fragment fields do not match assembly")
	ErrFragmentTooLarge      = errors.New("netsec: reassembled request exceeds limit")
)
