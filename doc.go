// Package netsec implements storage envelopes with an independent HMAC-based
// key commitment in front of a host-provided AEAD.
//
// A record is opened only after its unique key identifier resolves through the
// host's read-only key ring and the commitment over the record version, key
// identifier, nonce, AAD, lengths, and ciphertext verifies in constant time.
// Unknown or duplicate key identifiers, commitment failures, and AEAD failures
// all surface as ErrOpen without plaintext.
//
// The commitment relies on HMAC-SHA256; it does not assert that the injected
// AEAD is itself key-committing.
package netsec
