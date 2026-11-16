// Package netsec audits hierarchical deterministic wallet export material.
//
// It models the BIP-32 exposure closure caused by exporting extended public
// keys, extended private keys, and leaf private keys. Extended public keys are
// harmless alone, but an xpub plus any non-hardened child private key recovers
// the xpub's private key. Hardened edges block both public derivation and
// upward private-key recovery.
package netsec
