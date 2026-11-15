// Package netsec provides domain-isolated HKDF-SHA256 key derivation for an
// encryption host.
//
// Extract and Expand follow RFC 5869 and are verified against the RFC test
// vectors. Derived keys bind a length-prefixed application context (tenant,
// purpose, protocol version, direction and epoch), which guarantees that
// distinct contexts can never collide after concatenation. Root keys are held
// exclusively by the Host: callers only receive one-time Handle values that
// expose key material through callbacks, never as raw exported keys.
package netsec
