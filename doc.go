// Package netsec implements a Noise_XX_25519_ChaChaPoly_SHA256 handshake
// with Ed25519 identity payload binding.
//
// Identity payloads authenticate both the message-local handshake hash and
// the peer's X25519 static public key. The first XX message must have an
// empty payload, and the remote identity is not published to the application
// until the handshake completes and verification succeeds.
package netsec
