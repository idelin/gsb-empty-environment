// Package netsec implements negotiation-time downgrade protection for an
// SMB 3.x server. It is transport-agnostic: callers feed whole inbound
// messages to an Engine and apply the returned Decision.
//
// # Enforced policy
//
// The engine protects encrypted shares and servers against negotiation
// tampering that would force an unencrypted SMB 2.1-style session:
//
//   - Under SMB 3.1.1 the encryption algorithm is chosen from the
//     ENCRYPTION_CIPHERS negotiate context. When the client cipher list and
//     the server preference have no intersection, negotiation is rejected.
//   - When the server or the target share requires encryption, a dialect below
//     SMB 3.0, the absence of a usable cipher, and any plaintext request after
//     TREE_CONNECT are all rejected and the transport is closed.
//   - The engine accumulates the pre-authentication integrity hash itself.
//     It starts at 64 zero bytes and computes H = SHA-512(H || message) over
//     the full negotiate request, then over the full negotiate response
//     supplied through ObserveNegotiateResponse.
//   - SESSION_SETUP signatures are validated by a caller-injected
//     SessionSetupVerifier that receives the raw message and the running
//     hash. A failed verification closes the connection.
//   - A second NEGOTIATE after negotiation completed closes the connection
//     immediately.
//
// ActionReject means "send a protocol error, then close"; ActionDisconnect
// means "close without sending anything further". Both leave the engine in
// the terminal closed state.
package netsec
