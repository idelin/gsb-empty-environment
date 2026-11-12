// Package taskpkg implements target binding for an outbound dialer that
// otherwise trusts a domain name checked before dialing.
//
// A DNS-based attack can change the answer returned for a canonical host name
// between the moment the host name was validated and the moment the platform
// dialer resolves it again, steering an outbound connection into an internal
// management network. The Binder in this package removes that second
// resolution: a Policy pins the canonical host name, port, the set of
// verified IP addresses and the policy generation. Every dial resolves the
// host once through the configured resolver, keeps only verified answers and
// dials the resulting IP literals in parallel.
//
// A connection is only delivered when its actual peer belongs to the verified
// set, matches the pinned port and the policy generation still current, and is
// not covered by any currently forbidden CIDR. Revocation and delivery share a
// single mutex so they have one global order: a connection that loses the
// race, or arrives after revocation or a policy change, is closed and never
// handed to the host.
package taskpkg
