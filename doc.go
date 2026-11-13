// Package netsec provides X.509 name constraints handling for certificate
// path validation.
//
// Unlike a naive reading of crypto/x509, this package distinguishes an
// absent permittedSubtrees field (which imposes no restriction) from a
// present-but-empty one (which permits no names at all).
package netsec
