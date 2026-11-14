// Package netsec classifies outbound destinations for an HTTP proxy.
//
// Addresses are normalized to the endpoint that is actually reached before
// range checks are applied. IPv4-mapped and IPv4-compatible IPv6 addresses,
// 6to4, Teredo, NAT64, ISATAP, and zone suffixes are handled, as are the
// traditional decimal, octal, and hexadecimal IPv4 spellings.
//
// ParseAddress classifies one address. Resolver additionally classifies every
// DNS answer, and Resolver.CheckRedirect revalidates each redirect hop.
// Non-public destinations fail closed with an error wrapping ErrBlocked.
package netsec
