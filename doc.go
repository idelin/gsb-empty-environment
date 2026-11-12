// Package taskpkg verifies authenticated alias and denial proof paths for a
// cached DNS-style resolver.
//
// The package deliberately does not perform signature verification. Every
// Proof passed to a Resolver is assumed to have a valid cryptographic
// signature. What the Resolver checks is the part a cache can get wrong even
// after signatures are verified:
//
//   - the signer zone named by a proof must be the zone that is actually
//     authoritative for the proof's subject in the configured finite zone
//     tree, not merely a zone that claims authority;
//   - every proof must still be valid at the resolution time;
//   - a denial applies to the terminal (endpoint) name and query type, never to
//     the starting name or to unrelated types at the starting name;
//   - every alias hop in the returned authorization path must be justified by
//     the authority of the alias owner; and
//   - a missing, expired, untrusted, or ambiguous proof produces Unknown, not a
//     negative answer.
//
// A Resolver can enumerate every distinct trusted path (see Enumerate). This is
// useful both for auditing and for proving that no alternate path yields a
// different answer. When trusted evidence is inconsistent, Resolve returns
// Unknown rather than choosing one of the paths.
package taskpkg
