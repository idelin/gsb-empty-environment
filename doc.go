// Package symexec implements bounded symbolic execution for small
// authentication protocols.
//
// A Scenario contains initial attacker knowledge and instantiated role
// scripts. Analyze explores every interleaving of the role steps in
// breadth-first order. Each receive step is fed an attacker-constructible
// message; the attacker can project known pairs, decrypt with known keys, and
// compose new pairs/encryptions up to a bounded depth. The search stops at the
// shortest trace in which an end(principal, nonce) event has no earlier
// begin(principal, nonce), and every attacker message carries a replayable
// Derivation certificate.
package symexec
