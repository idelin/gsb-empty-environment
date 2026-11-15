// Package netsec provides DNSSEC validation helpers.
//
// This package implements the RRSIG wildcard-expansion decision described in
// RFC 4034 section 3.1 (the RRSIG Labels field) and RFC 4035 section 8.2
// (wildcard answers and the NSEC proof of no closer name). A validator must
// never treat an answer whose signature was synthesized from a wildcard as
// proof that the queried owner name exists as a concrete name: the signature
// has to be re-verified against the reconstructed wildcard name
// ("*." plus the closest encloser) and an NSEC proof must establish that no
// name closer to the owner exists.
package netsec
