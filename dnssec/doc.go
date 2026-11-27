// Package dnssec is an importable, dependency-free DNSSEC validation core
// implementing the recursive-resolver primitives of RFC 4034, RFC 4035 and
// RFC 5155 on top of the Go standard library's cryptographic primitives.
//
// # Capabilities
//
// Canonical ordering and signing (RFC 4034):
//   - canonical name comparison from the root label, case-insensitive
//   - canonical RDATA with lowercase, uncompressed embedded names and
//     deterministically re-encoded NSEC/NSEC3 type bitmaps
//   - RRset ordering by owner name then RDATA, with duplicate RDATA removed
//   - RRSIG signed-data assembly and ECDSA P-256/SHA-256 (alg 13) plus
//     ECDSA P-384/SHA-384 (alg 14) verification of the raw R||S signature
//
// Trust chain and timing:
//   - DNSKEY key-tag computation (appendix B) and ZONE/PROTOCOL/REVOKE checks
//   - DS digest computation/verification for SHA-1 and SHA-256
//   - RFC 1982 serial arithmetic for inception/expiration window validation
//   - wildcard synthesis detection via the RRSIG Labels field
//
// Denial of existence:
//   - RFC 5155 iterated NSEC3 hashing (SHA-1, arbitrary salt/iterations) and
//     unpadded base32hex codec
//   - circular NSEC3 interval coverage including owner > nextHashed wrap
//   - the three-part NXDOMAIN proof: exact-match closest encloser, next
//     closer interval coverage, and wildcard interval coverage
//   - NODATA proofs with exact-match bitmap checks (qtype and CNAME absent)
//   - Opt-Out handling that classifies unsigned DS delegations as Insecure
//     rather than Bogus
//   - legacy NSEC name-order proofs
//
// # Typical flow
//
// A resolver parses a wire message with ParseMessage, builds a Validator,
// verifies positive RRsets through Validator.VerifyRRSet (combined with
// Validator.VerifyRRSetWithWildcard when the answer is wildcard synthesized),
// and authenticates negative answers through Validator.VerifyDenial.
// Failures that demand SERVFAIL are surfaced as *BogusError; test them with
// IsBogus. An unsigned but cryptographically sound delegation is reported as
// StatusInsecure.
package dnssec
