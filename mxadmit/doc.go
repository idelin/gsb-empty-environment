// Package mxadmit is an admission layer for mail delivery hosts that resume
// authenticated connections across signed MX and certificate-directory
// updates.
//
// The central rule is deliberately conservative: a receipt may be resumed
// only when its actual MX, public key, authentication-path digest, DNS policy
// generation and message domain are all present in one signed configuration.
// Values from two otherwise valid configurations are never combined.  A
// re-handshake may supply a new certificate path (and the public key carried
// by that path), but it never manufactures DNS authorization that is absent
// from a signed configuration.
//
// A Batch describes signed configurations, activations observed at endpoints,
// partial order constraints between those activations, existing receipts and
// deliveries.  Orderings are interpreted at integer ticks.  Signature
// validity intervals are closed: a signature is effective at both of its
// endpoints.
package mxadmit
