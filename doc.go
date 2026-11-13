// Package netsec verifies selectively disclosed elements of an array.
//
// A signed array is represented by one digest per element, in the original
// element order. A disclosure is the base64url-encoded JSON array
// [salt, value]. The verifier computes the digest of every disclosure and
// locates the corresponding commitment by digest, never by a caller-supplied
// index. The declared index is only checked against the position derived from
// the digest; it is never used to reinterpret the element.
package netsec
