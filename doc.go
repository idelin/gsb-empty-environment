// Package netsec implements the pre-injection authorization boundary for
// delivering disk-wrapping keys to an AMD SEV guest.
//
// A Gate binds a disk key to the expected guest identity, the expected
// firmware-derived launch measurement, and a single-use attestation nonce.
// Policy words containing AMD's DEBUG bit are never accepted. A package that
// fails any of these checks is not encrypted and is never returned for
// injection.
package netsec
