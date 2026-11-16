// Package netsec provides HKDF-SHA256 key derivation with canonical,
// domain-separated application contexts and a trusted host key authority.
//
// Extract and Expand implement RFC 5869. Contexts bind a derivation to a
// tenant byte string, purpose, protocol version, direction, and epoch using a
// length-prefixed encoding. Root key material remains inside Host; callers
// receive opaque root capabilities and one-shot derived-key handles. A handle
// exposes only a scoped Key view whose methods consume the key without
// revealing its bytes.
package netsec
