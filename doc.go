// Package netsec implements a small, self-contained multi-party Schnorr
// signing protocol over NIST P-256.
//
// The protocol deliberately separates the coordinator's public transcript from
// the signer's private nonce state. Signers persist a nonce together with its
// session, message, and roster binding before releasing a nonce commitment.
// Consuming a nonce and storing the resulting partial signature are one atomic
// store transition. A restarted signer can therefore return the original
// partial signature, but cannot sign a second challenge with the same nonce.
package netsec
