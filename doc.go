// Package netsec allocates stable pseudonyms for a de-identification service.
//
// A pseudonym's short code is an index, not an identity. The allocator computes
// a full keyed MAC (HMAC-SHA-256 by default) for every identifier and stores
// that full MAC internally. Short codes are prefixes of the encoded MAC.
//
// If two different MACs share a short-code prefix, the allocator never merges
// them. The identity that already owns the short code keeps it forever; the new
// identity receives a longer prefix. Every shorter prefix it passed through is
// recorded as an explicit alias, so an old event that carries both its old
// short code and its identifier can be resolved to the current code.
//
// A short code by itself may therefore be ambiguous. Callers that need to
// disambiguate must use Resolve with the original identifier; doing so compares
// the full MAC and refuses to return another person's code.
package netsec
