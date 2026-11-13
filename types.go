package netsec

import (
	"errors"
	"io"
)

const (
	// SAPManagement identifies the management client used for protected
	// disconnect-control actions.
	SAPManagement uint16 = 1
	// SAPPublic identifies the unprivileged public client.
	SAPPublic uint16 = 16

	// ClassDisconnectControl is the COSEM class identifier for disconnect
	// control objects.
	ClassDisconnectControl uint16 = 70
	// MethodRemoteDisconnect and MethodRemoteReconnect are the only action
	// methods permitted on a disconnect-control object.
	MethodRemoteDisconnect uint8 = 1
	MethodRemoteReconnect  uint8 = 2
)

// Access is the COSEM service being requested.
type Access uint8

const (
	AccessRead Access = iota + 1
	AccessWrite
	AccessAction

	// Get, Set and Invoke are aliases matching the COSEM service names.
	Get    = AccessRead
	Set    = AccessWrite
	Invoke = AccessAction
)

// ObjectID identifies a COSEM object by its class and OBIS logical name.
type ObjectID struct {
	ClassID uint16
	OBIS    [6]byte
}

// NewObjectID constructs an object identifier.
func NewObjectID(classID uint16, obis [6]byte) ObjectID {
	return ObjectID{ClassID: classID, OBIS: obis}
}

// AccessRequest describes a service request made by an associated client.
type AccessRequest struct {
	SAP    uint16
	Object ObjectID
	Access Access
	Method uint8
}

// Decision is the result of an access check. When Allowed is false, Reason
// identifies the policy or authentication failure.
type Decision struct {
	Allowed bool
	Reason  error
}

// Config configures an AccessController. Zero-valued SAP fields use the
// standard SAP 1 and SAP 16 values.
type Config struct {
	PublicSAP     uint16
	ManagementSAP uint16

	// HLSSecret is the shared secret used for the built-in HMAC-SHA256
	// challenge-response verifier. If HLSVerifier is set, it takes precedence.
	HLSSecret []byte
	// HLSVerifier is an optional custom challenge-response verifier.
	HLSVerifier func(challenge, response []byte) bool
	// HLSChallengeSize controls the number of random bytes in a challenge.
	HLSChallengeSize int
	// Random supplies challenge bytes. crypto/rand.Reader is used by default.
	Random io.Reader

	// LLSPassword is the configured low-level security password. An empty
	// password is always rejected, even if this field is empty.
	LLSPassword []byte

	// RegisteredObjects are objects that clients may read.
	RegisteredObjects []ObjectID
}

// AssociationState is a snapshot of one SAP's association state.
type AssociationState struct {
	Associated          bool
	HLSChallengePending bool
	HLSComplete         bool
	LLSComplete         bool
}

var (
	ErrNotAssociated          = errors.New("COSEM client is not associated")
	ErrUnknownSAP             = errors.New("unknown COSEM client SAP")
	ErrAccessDenied           = errors.New("COSEM access denied")
	ErrObjectNotRegistered    = errors.New("COSEM object is not registered for reads")
	ErrActionNotAllowed       = errors.New("COSEM action is not allowed")
	ErrMethodNotAllowed       = errors.New("COSEM method is not allowed")
	ErrHLSRequired            = errors.New("HLS challenge-response is required")
	ErrHLSChallengeRequired   = errors.New("HLS challenge has not been started")
	ErrHLSResponseInvalid     = errors.New("HLS challenge-response is invalid")
	ErrHLSNotAllowed          = errors.New("HLS is only available to the management client")
	ErrEmptyPassword          = errors.New("LLS password must not be empty")
	ErrInvalidPassword        = errors.New("LLS password is invalid")
	ErrLLSNotAllowed          = errors.New("LLS is only available to the management client")
	ErrInvalidAccess          = errors.New("invalid COSEM access type")
	ErrInvalidChallengeConfig = errors.New("invalid HLS challenge configuration")
)
