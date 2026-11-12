// Package netsec implements strict ZMTP 3.x greeting negotiation and
// replay protection for CURVE MESSAGE commands.
//
// The package deliberately treats the greeting as a security boundary:
// malformed signatures, pre-3.0 peers, mechanism downgrades, non-zero
// mechanism padding, and role confusion all fail closed. Callers may
// perform the mechanism-specific handshake after Handshake returns, but
// must use Session.ReadFrame or Session.ReadCurveMessage for all incoming
// traffic so that CURVE nonces remain monotonic.
//
// This is a negotiation and replay-protection layer for an existing
// message library. It does not replace the Curve25519/XSalsa20-Poly1305
// message-box implementation that decrypts the CURVE box.
package netsec
