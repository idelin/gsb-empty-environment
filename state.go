package netsec

import (
	"errors"
	"fmt"
)

// State is the lifecycle state of an authorization code.
//
// A code starts in StateIssued. A single successful redemption moves it to
// StateRedeemed. Any later attempt to redeem the same code is treated as
// replay: every token previously issued from the code is revoked and the
// code moves to StateRevoked.
type State uint8

const (
	// StateUnknown is the zero value and never describes a stored code.
	StateUnknown State = iota
	// StateIssued means the code is available for exactly one redemption.
	StateIssued
	// StateRedeemed means the code has been redeemed once.
	StateRedeemed
	// StateRevoked means the code was reused; all of its tokens are invalid.
	StateRevoked
)

func (s State) String() string {
	switch s {
	case StateIssued:
		return "issued"
	case StateRedeemed:
		return "redeemed"
	case StateRevoked:
		return "revoked"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(s))
	}
}

// Token is a bearer token issued by a successful redemption. It is an alias
// of string so callers can keep the token as either a plain string or a
// named value.
type Token = string

// Action identifies a state-machine event sent by the authorization service.
type Action string

const (
	// ActionIssue makes a new authorization code available for redemption.
	ActionIssue Action = "issue"
	// ActionRedeem attempts to exchange a code for a token.
	ActionRedeem Action = "redeem"
	// ActionRevokeToken invalidates one token.
	ActionRevokeToken Action = "revoke-token"
	// ActionRevokeCode invalidates a code and every token issued from it.
	ActionRevokeCode Action = "revoke-code"
)

// Event is a state transition supplied by the authorization service.
type Event struct {
	Action Action
	Code   string
	Token  string
}

// Result reports the outcome of applying an Event.
type Result struct {
	Token Token
	State State
	OK    bool
	Err   error
}

var (
	// ErrInvalidCode is returned when a code is empty.
	ErrInvalidCode = errors.New("netsec: invalid authorization code")
	// ErrCodeNotFound is returned when a code was never issued.
	ErrCodeNotFound = errors.New("netsec: authorization code not found")
	// ErrCodeAlreadyIssued is returned when the same code is issued twice.
	ErrCodeAlreadyIssued = errors.New("netsec: authorization code already issued")
	// ErrCodeAlreadyUsed is returned for a replay of an already redeemed code.
	// The replay also revokes every token previously issued from that code.
	ErrCodeAlreadyUsed = errors.New("netsec: authorization code already used")
	// ErrCodeRevoked is returned when a code was revoked after replay detection.
	ErrCodeRevoked = fmt.Errorf("%w: code revoked", ErrCodeAlreadyUsed)
	// ErrInvalidToken is returned when a token is empty.
	ErrInvalidToken = errors.New("netsec: invalid token")
	// ErrTokenNotFound is returned when a token was never issued.
	ErrTokenNotFound = errors.New("netsec: token not found")
	// ErrUnknownAction is returned for an event with an unsupported action.
	ErrUnknownAction = errors.New("netsec: unknown action")
)

// ErrCodeExists and ErrCodeUsed are short aliases for the corresponding
// sentinel errors.
var (
	ErrCodeExists  = ErrCodeAlreadyIssued
	ErrCodeUsed    = ErrCodeAlreadyUsed
	ErrNotFound    = ErrCodeNotFound
	ErrAlreadyUsed = ErrCodeAlreadyUsed
	ErrRevoked     = ErrCodeRevoked
)

// NextState computes the next code state for a single event. It is the pure
// transition function used by Machine; token revocation is performed by
// Machine because it has access to the issued-token set.
func NextState(current State, action Action) (State, error) {
	switch action {
	case ActionIssue:
		if current != StateUnknown {
			return current, ErrCodeAlreadyIssued
		}
		return StateIssued, nil
	case ActionRedeem:
		switch current {
		case StateIssued:
			return StateRedeemed, nil
		case StateRedeemed:
			return StateRevoked, nil
		case StateRevoked:
			return StateRevoked, ErrCodeRevoked
		default:
			return current, ErrCodeNotFound
		}
	case ActionRevokeCode:
		if current == StateUnknown {
			return current, ErrCodeNotFound
		}
		return StateRevoked, nil
	case ActionRevokeToken:
		// Token revocation does not change the code lifecycle.
		return current, nil
	default:
		return current, fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
}
