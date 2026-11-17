// Package netsec implements verification of custom, Ed25519-signed delegation
// certificates. It is intentionally independent of X.509.
//
// Certificates grant a subject public key a set of operations and a set of
// component-wise resource path prefixes. A verifier searches the supplied
// certificate pool for one acyclic chain rooted in host-configured trust
// anchors, intersecting authority at every step. The returned chain is ordered
// from leaf to root and excludes the trust anchor.
package netsec
