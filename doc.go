// Package netsec provides source-network admission and cache-probing
// protection for a Go recursive DNS resolver.
//
// Handler checks the transport-observed client address before consulting any
// resolver data. Clients outside Policy's registered networks always receive
// REFUSED with empty answer, authority, and additional sections. Admitted
// RD=0 questions are answered only from local authoritative zones; cache and
// upstream resolvers are never consulted. Every record emitted by Handler has
// the single configured TTL, regardless of cache hit or miss.
package netsec
