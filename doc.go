// Package netsec implements authentication for NATS-style leafnode JWTs.
//
// It verifies operator-signed account JWTs, account-signed user JWTs, account
// and user expiry, account revocation tables, and one-time Ed25519 nonce
// signatures. Tokens use the compact JWT encoding with the "ed25519"
// algorithm used by NATS.
package netsec
