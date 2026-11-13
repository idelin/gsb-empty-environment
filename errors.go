package netsec

import "errors"

var (
	ErrSessionClosed         = errors.New("netsec: session is closed")
	ErrSessionExists         = errors.New("netsec: another session for this peer is already active")
	ErrStaleGeneration       = errors.New("netsec: transport generation must increase on reconnect")
	ErrInvalidSession        = errors.New("netsec: invalid session identity")
	ErrInvalidRoute          = errors.New("netsec: invalid route")
	ErrPolicyRejected        = errors.New("netsec: import policy rejected route")
	ErrVRPRejected           = errors.New("netsec: VRP rejected route")
	ErrRouteNotFound         = errors.New("netsec: route not found")
	ErrStaleRouteForFlowSpec = errors.New("netsec: stale route is not eligible for FlowSpec validation")
	ErrStaleRouteExport      = errors.New("netsec: stale route is not eligible for export")
	ErrInvalidConfiguration  = errors.New("netsec: invalid configuration")
)
