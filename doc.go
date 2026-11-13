// Package netsec implements a challenge-response gate for high-risk DNP3-style
// control operations.
//
// A high-risk request is frozen into a RequestIdentity and placed in the
// pending state.  The gateway issues a random 128-bit challenge id, a 32-byte
// nonce, a key id from its fixed key table, and a monotonic deadline.  The
// request is not executed until the peer returns an HMAC-SHA256 response over
// the complete request identity, nonce, challenge id, and key id.
//
// Responses are bound to the exact connection generation and request fields
// that were challenged.  A response cannot be moved to another function code,
// object, application sequence, or connection.  A successfully authorised
// request is executed exactly once; retransmission of the same valid response
// returns the original receipt instead of executing the callback again.
package netsec
