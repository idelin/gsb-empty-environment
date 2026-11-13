// Package netsec implements the response-type policy for an OAuth 2.0 and
// OpenID Connect authorization endpoint.
//
// The policy never permits an access token in a front-channel authorization
// response. A standalone "code" response is allowed, and "id_token" is allowed
// only as part of a registered "code id_token" hybrid response. Any
// response_type containing "token" is rejected before any other rule is
// considered.
//
// Policy implements the pure decision logic. StateMachine wraps the policy in
// an importable state machine that accepts authorization state from the
// authorization service and records the phase of the decision.
package netsec
