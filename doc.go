// Package netsec implements an age-compatible header parser and decryption
// entry point with an explicit, tamper-evident ordering between recipient
// handling, header authentication and payload exposure.
//
// An age file starts with a version line, one or more recipient stanzas
// ("lines") and a single "--- hmac <tag>" line, followed by the encrypted
// payload. The HMAC is keyed from the file key and covers every header byte
// from the version line through the "---" prefix, so modifying, deleting or
// inserting any stanza changes the authenticated input.
//
// # Security ordering
//
// Decryption must happen strictly in this order:
//
//  1. ParseHeader parses stanzas without trusting them and returns a sealed
//     PayloadReader. Reading it now returns ErrHeaderNotAuthenticated and
//     zero bytes.
//  2. An Identity recovers the file key from a recipient stanza (for example
//     ScryptIdentity unwraps the passphrase-based scrypt stanza).
//  3. Header.VerifyMAC checks the HMAC over the exact header bytes. Unknown
//     stanza types are retained verbatim and therefore always covered by the
//     MAC; the check uses constant-time comparison.
//  4. Only after successful verification does PayloadReader unlock and begin
//     decrypting the STREAM payload.
//
// The high-level Decrypt helper performs all four steps. A tampered header
// therefore never causes a single payload byte to be returned.
//
// # Recipient rules
//
// A scrypt stanza is passphrase based and may never share a header with other
// recipient stanzas: both encryption and decryption enforce that the scrypt
// stanza is the only recipient line. Key-based recipients such as X25519 may
// appear multiple times.
//
// The package depends only on the Go standard library. Its ChaCha20-Poly1305
// (RFC 8439) and scrypt (RFC 7914) implementations are pinned to the published
// test vectors in the test suite.
package netsec
