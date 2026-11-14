// Package netsec implements RFC 8659 CAA authorization checks for a
// certificate issuance service.
//
// A Checker starts at the complete certificate DNS name, follows CNAME and
// DNAME aliases, and climbs the DNS name tree one label at a time until it
// finds the first non-empty CAA RRset. An empty RRset does not stop the
// climb. If an alias target has no CAA, the climb continues at the parent of
// the original name, never at the parent of the alias target. The relevant
// RRset's issue, issuewild, and iodef properties are then evaluated.
// issuewild takes precedence for wildcard requests only; exact names ignore
// it.
//
// DNS lookup failures and timeouts never permit issuance. DNSSEC policy is
// explicit: a bogus status always denies, and callers can optionally require
// secure validation or reject unknown validation states. Unknown CAA
// properties are logged and ignored unless they carry the issuer-critical
// flag, in which case issuance is denied as required by RFC 8659.
//
// The package is transport-independent through the Resolver interface.
// DNSResolver is a small CAA-specific DNS client for deployments that do not
// already have a suitable resolver.
package netsec
