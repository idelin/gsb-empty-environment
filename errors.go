package netsec

import "errors"

var (
	// ErrInvalidRequest reports a nil or malformed request.
	ErrInvalidRequest = errors.New("netsec: invalid request")
	// ErrInvalidExpression reports an expression that is nil, cyclic in
	// construction, or not one of the package's immutable node types.
	ErrInvalidExpression = errors.New("netsec: invalid expression")
	// ErrInvalidSource reports a source identifier outside [0, MaxSources).
	ErrInvalidSource = errors.New("netsec: invalid secret source")
	// ErrMissingSource reports a read of a source absent from the request.
	ErrMissingSource = errors.New("netsec: secret source not supplied by request")
	// ErrUnknownVariable reports a local variable that is not in scope.
	ErrUnknownVariable = errors.New("netsec: unknown local variable")
	// ErrMissingAuthorization reports a missing or forged authorization.
	ErrMissingAuthorization = errors.New("netsec: missing or forged authorization")
	// ErrAuthorizationMismatch reports a valid authorization used for another
	// request, another expression node, or through a wrapper that is not the
	// registrar's own authorization value.
	ErrAuthorizationMismatch = errors.New("netsec: authorization is not bound to this request and node")
	// ErrAuthorizationReuse reports an authorization used more than once.
	ErrAuthorizationReuse = errors.New("netsec: authorization already used")
	// ErrSecretOutput reports an output whose label is not empty.
	ErrSecretOutput = errors.New("netsec: public output carries a non-empty label")
	// ErrEvaluationDepth reports an expression deeper than the executor's
	// defensive recursion limit.
	ErrEvaluationDepth = errors.New("netsec: expression nesting limit exceeded")
)
