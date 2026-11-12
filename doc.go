// Package netsec implements a one-time-secret delivery protocol for Go
// services: a secret is served exactly once, is destroyed on the first
// successfully confirmed delivery, and network failures can never produce a
// false delivery claim.
//
// # Protocol
//
// Put registers a secret in the available state. Claim mints a short-lived,
// session-bound read lease carrying a random nonce, proof key and the payload.
// While the lease is live nobody else may claim the secret; if the delivery
// response or receipt is lost, the original holder calls Retransmit with the
// same session id and nonce. Retransmit never extends the lease deadline.
//
// After the full payload has been received the client proves completion with
// ComputeProof:
//
//	HMAC-SHA256(proofKey, nonce || SHA-256(payload))
//
// Acknowledge verifies that proof in constant time. Only a valid proof
// destroys the backend payload and moves the record to the delivered terminal
// state. Invalid or missing proofs leave the lease untouched, so a dropped
// receipt cannot be mistaken for delivery.
//
// When a lease reaches its TTL (lazily on touch or explicitly via Sweep) the
// payload is destroyed and the record enters the unknown terminal state with
// reason lease_expired. A backend destruction failure, at confirmation or
// expiry, likewise forces unknown with reason destroy_failed: the service
// drops all payload, nonce and proof-key material and never re-serves or
// reissues the secret to anyone, even though the backend blob may physically
// remain until an out-of-band cleanup. All transitions are recorded in an
// audit trail that contains no secret material.
package netsec
