// Package netsec implements host-only URI name-constraint matching.
//
// URI constraints apply to the host component only. The path, query,
// fragment, and user information of a URI never participate in a match.
// Hosts are compared using the DNS name-constraint rules from RFC 5280:
// matching is case-insensitive, a constraint matches the named host and its
// subdomains, and a leading period requires at least one subdomain label.
//
// The package is intended to be imported by certificate path validation code
// that needs to enforce permitted and excluded URI name constraints.
package netsec
