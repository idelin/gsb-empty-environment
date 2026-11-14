// Package netsec implements cache admission control ("bailiwick" checking)
// for recursive DNS resolvers.
//
// A recursive resolver must not blindly cache every record an authoritative
// server returns: a malicious server can stuff its additional or authority
// sections with records for victim domains and poison the cache. This
// package decides, for every response, which records fall inside the
// answering zone's bailiwick and may be cached, and which must be treated
// as one-time hints only.
//
// The admission rules are:
//   - The bailiwick is derived per response from the query name, the qtype,
//     the authoritative zone that answered, and wildcard expansion.
//   - Records in the additional and authority sections outside that scope
//     are returned as hints and never enter the cache.
//   - Every hop of a CNAME/DNAME chain recomputes the scope; a hop that
//     leaves the zone invalidates the rest of the chain for caching.
//   - A referral (delegation NS below the zone) narrows the scope: data
//     below the delegation point is not authoritative from this server,
//     except the delegation records themselves and matching glue.
//   - DNSSEC validation only covers the signed zone; unsigned
//     out-of-bailiwick records gain no trust from a validated parent zone.
//   - Owner names, names inside RDATA, compression pointers and duplicate
//     records all go through one canonicalization path before comparison.
package netsec
