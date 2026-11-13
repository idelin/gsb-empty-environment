package netsec

import "errors"

var (
	ErrInvalidToken      = errors.New("netsec: invalid token")
	ErrPrimaryToken      = errors.New("netsec: primary token cannot be entered as a client identity")
	ErrDifferentHost     = errors.New("netsec: token belongs to a different host")
	ErrTokenExpired      = errors.New("netsec: token deadline has passed")
	ErrRequestEnded      = errors.New("netsec: request has ended")
	ErrRequestInactive   = errors.New("netsec: request is not active")
	ErrConnectionClosed  = errors.New("netsec: connection is closed")
	ErrGenerationRevoked = errors.New(
		"netsec: request generation has been revoked",
	)
	ErrTokenRequestMismatch = errors.New("netsec: token does not belong to the request")
	ErrRequestMismatch      = errors.New("netsec: request does not match token binding")
	ErrNoCurrentRequest     = errors.New("netsec: thread has no current request")
	ErrWrongRequest         = errors.New("netsec: token belongs to another current request")
	ErrNoImpersonation      = errors.New("netsec: impersonation stack is empty")
	ErrStackMismatch        = errors.New("netsec: token is not the exact stack top")
	ErrNestedImpersonation  = errors.New("netsec: refusing to clear an outer impersonation scope")

	ErrIdentityCannotAccessResources = errors.New(
		"netsec: Identification tokens cannot access resources",
	)
	ErrRemoteRequiresDelegation = errors.New(
		"netsec: remote resources require a Delegation token",
	)
	ErrInvalidResourceKind = errors.New("netsec: invalid resource kind")
	ErrInvalidLevel        = errors.New("netsec: invalid authority level")
	ErrInvalidDeadline     = errors.New("netsec: deadline must be a host monotonic instant")

	ErrPolicyRequired = errors.New("netsec: SpawnAsync requires an explicit NoIdentity or Capture policy")
	ErrInvalidPolicy  = errors.New("netsec: invalid spawn policy")
	ErrNilCallback    = errors.New("netsec: nil async callback")

	ErrGrantRequired          = errors.New("netsec: elevated capture requires a host authorization grant")
	ErrInvalidGrant           = errors.New("netsec: invalid or mismatched host authorization grant")
	ErrGrantRevoked           = errors.New("netsec: host authorization grant has been revoked")
	ErrGrantInsufficient      = errors.New("netsec: host authorization grant has insufficient level")
	ErrDeadlineExceedsRequest = errors.New(
		"netsec: task deadline may not exceed the request deadline",
	)
	ErrDeadlineExceedsToken = errors.New(
		"netsec: captured task deadline may not exceed the source token deadline",
	)
	ErrDeadlineExceedsGrant = errors.New(
		"netsec: task deadline may not exceed the authorization grant deadline",
	)
	ErrDeadlineInPast    = errors.New("netsec: deadline is not in the future")
	ErrCaptureEscalation = errors.New(
		"netsec: captured identity may not exceed the source token level",
	)

	ErrTaskRevoked      = errors.New("netsec: task identity has been revoked")
	ErrTaskExpired      = errors.New("netsec: task deadline has passed")
	ErrTaskFinished     = errors.New("netsec: task handle is no longer usable")
	ErrTaskAlreadyRun   = errors.New("netsec: task has already been started")
	ErrTaskCanceled     = errors.New("netsec: task was canceled before it started")
	ErrTaskNotRunning   = errors.New("netsec: task handle is not running")
	ErrNoIdentity       = errors.New("netsec: task was spawned with NoIdentity")
	ErrNoIdentityOption = errors.New(
		"netsec: NoIdentity cannot be combined with capture options",
	)

	ErrConnectionBusy      = errors.New("netsec: connection already has an active request")
	ErrConnectionExists    = errors.New("netsec: connection id already exists")
	ErrConnectionNotFound  = errors.New("netsec: connection id was not found")
	ErrInvalidConnectionID = errors.New("netsec: connection id must not be empty")
	ErrInvalidRequestID    = errors.New("netsec: request id must not be empty")
	ErrSchedulerClosed     = errors.New("netsec: scheduler is closed")
)
