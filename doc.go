// Package netsec provides dynamic code identity tracking for a Go sandbox
// host. It models 64-byte pages, virtual-to-physical mappings, W^X
// permissions, write provenance, and immutable execution identities.
//
// An execution is authorized only when the virtual page is executable, the
// range is inside the page, no alias of the physical page is writable, and the
// captured full-page SHA-256 identity still matches the current physical
// generation. Detection rules receive only the digest and a private immutable
// snapshot. Merely unpacking or writing code does not produce a malicious
// verdict.
package netsec
