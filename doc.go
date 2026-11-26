// Package hsm implements a small PKCS#11 policy state machine for an HSM or
// key-management control plane.
//
// The package models secret AES keys and asymmetric key pairs, the PKCS#11
// attribute vector that governs their use, and a bounded API-level attack
// search over the key-management operations that are routinely exposed to
// applications. The search models the classic wrap-and-decrypt and
// unwrap-with-an-attacker-controlled-template attacks, including mechanism
// confusion and privilege expansion through C_SetAttributeValue and
// C_CopyObject.
//
// A zero Policy is hardened. Use InsecurePolicy to reproduce legacy or
// non-conforming HSM behaviour. Cryptographic operations are real: AES key
// wrap (RFC 3394), AES-CBC with PKCS#7 padding, and AES-GCM. The attack search
// is symbolic in its provenance tracking but drives the same operation
// adjudicator and cryptographic implementation as the online HSM.
package hsm
