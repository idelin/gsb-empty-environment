// Package netsec provides certificate path validation with protection against
// certificate loops.
//
// A certificate path must not contain two certificates with the same subject
// distinguished name and the same public key. Such a pair is treated as a
// duplicate even when the certificates differ in serial number, validity,
// extensions, or subject alternative names.
package netsec
