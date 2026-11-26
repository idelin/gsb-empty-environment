// Package threshold implements accountable threshold Schnorr/FROST DKG and
// two-round signing state machines for Go key-custody hosts.
//
// Cryptographic group operations are supplied through Group. The package
// includes a real NIST P-256 implementation via P256Group. DKG round one
// binds a Schnorr proof of possession to (context, participant ID, C0);
// round two verifies Feldman shares and produces independently verifiable
// complaints. Signing uses canonical FROST binding factors, one-time nonce
// bundles, per-share verification, and AccountabilityReport culprit IDs.
package threshold
