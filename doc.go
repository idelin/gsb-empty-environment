// Package netsec provides an importable unseal state machine for a Go key
// service.
//
// The state machine defends against share replay across service restarts and
// generations: every share is cryptographically bound to its generation, seal
// configuration, sealing nonce and holder via HMAC-SHA256, and the underlying
// threshold secret is split with Shamir secret sharing over GF(256). A
// generation only unseals when the configured threshold of distinct, valid
// shares is reached, and unsealing is one-time. Stale-generation,
// future-generation, duplicate and mis-ordered submissions are rejected with
// sentinel errors and recorded in the audit trail. Rotation starts a new
// generation with a fresh nonce, after which only shares of the new generation
// can unseal.
//
// Durable snapshots are authenticated with a separate state key, and importing
// a snapshot that would roll the high-water generation back is refused.
package netsec
