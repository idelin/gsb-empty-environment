// Package netsec verifies OpenPGP key revocations with designated revokers.
//
// The security-critical rule in this package is that a designated revoker is
// identified by its complete fingerprint, never by its 64-bit key ID.  A key
// revocation is accepted only when it was made by the target key itself or by
// a revoker whose full fingerprint appears in a valid self-signature over the
// target key.  The revocation signature must also verify under that exact
// public key.
//
// Revocation Key subpackets are trusted only when they occur in the hashed
// (signed) area of a valid self-signature.  An attacker cannot inject a
// designation into the unhashed area.
package netsec
