// Package netsec implements the DH-CHAP negotiation and validation logic
// invoked by the AUTH_ELS handler of a Fibre Channel switch port.
//
// DH-CHAP negotiates hash algorithms and Diffie-Hellman groups without any
// integrity protection, so an on-path attacker can tamper with the offer
// lists. This package therefore enforces:
//
//   - Policy-gated negotiation: only identifiers explicitly listed in
//     Policy are acceptable, lists are strength-ordered strongest-first, and
//     the null DH group is selectable only when Policy.AllowNullDH is true.
//   - Small-subgroup protection: every peer DH public value must satisfy
//     2 <= y <= p-2, rejecting 0, 1 and p-1.
//   - Single-use challenges: a challenge value cannot be reused within one
//     transaction and must be at least 16 bytes long.
//   - No downgrade fallback: a failed negotiation terminates the transaction
//     rather than retrying with plain (null-DH) CHAP.
//
// The wire layer (ParseAuthMessage/MarshalAuthMessage) handles the
// AUTH_ELS TLV framing; Transaction drives the exchange state machine.
package netsec
