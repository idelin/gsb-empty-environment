// Package mailauth verifies DKIM signatures (RFC 6376) and ARC
// authentication chains (RFC 8617) over raw RFC 5322 message bytes and applies
// anti-spoofing policy decisions for an embedded mail security gateway.
//
// # Security model
//
// The package is fully offline: it never opens a network connection and never
// imports a third party DKIM implementation. Cryptographic operations use
// only the standard library crypto/rsa, crypto/ed25519 and crypto/sha256
// packages. Public keys are injected by the host through the KeyResolver
// interface (MemoryKeyStore is provided for local, in-process use).
//
// The verifier enforces, in addition to cryptographic correctness:
//
//   - Multiple From header fields are always rejected (Multiple-From Spoofing).
//   - A signature whose h= tag does not cover From is rejected.
//   - Oversigning is honored exactly: h= slots beyond the real field count are
//     signed as empty placeholders (RFC 6376 section 5.4.2), so a From field
//     injected after signing cannot bind to a signed slot.
//   - An l= body length limit that is shorter than the canonical body marks the
//     message as a truncation/injection high risk finding and, under the
//     default policy, rejects it.
//   - The x= expiration must still be in the future and, when both exist,
//     x must be later than t=.
//   - ARC instances must be a continuous 1..N sequence with no gaps or
//     duplicates, never exceeding 50 hops; i=1 must declare cv=none, any later
//     cv=fail voids the whole chain, and a cv=pass that does not match real
//     signature validation is a forgery finding.
//
// # Canonicalization
//
// Both "simple" and "relaxed" header and body algorithms are implemented at
// the byte level, including folding whitespace unfolding, WSP collapse,
// trailing empty line stripping, the empty body hash (SHA-256 over a single
// CRLF) and bare-LF/CR normalization to canonical CRLF wire form.
//
// # Typical use
//
//	verifier := mailauth.NewVerifier(localKeyStore, mailauth.DefaultPolicy())
//	verdict, err := verifier.VerifyMessage(rawMessageBytes)
//	if err != nil { ... }
//	if !verdict.Accepted { ... verdict.Reason, verdict.Findings ... }
package mailauth
