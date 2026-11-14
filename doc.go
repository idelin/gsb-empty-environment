// Package netsec implements identity-bound public key records for a key
// directory.
//
// A key is never selected by a short identifier. Authoritative lookups require
// both a complete fingerprint and a signed user-identifier binding. The full
// fingerprint is derived from the key material, so a publisher cannot attach a
// victim's fingerprint to a different key. Bindings and revocation
// certificates are self-signed by the key and are re-verified whenever a key
// is returned.
package netsec
