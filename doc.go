// Package netsec provides a key-derivation service built on HKDF-SHA256
// (RFC 5869). A single PRK feeds every derivation; callers receive handles
// scoped to registered purpose labels, and the HKDF info input is always
// assembled internally as length-prefixed TLV records rather than accepted
// as a raw byte string.
package netsec
