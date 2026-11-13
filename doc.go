// Package netsec implements expiry-bound WireGuard-style key lifecycle
// management for a coordination service.
//
// The Store owns node keys, their optional expiry, and the network graph
// derived from them. Expired keys are never emitted in a graph snapshot.
// Deltas computed against a graph already distributed to peers always include
// explicit delete entries for keys that expired, were removed, or were
// replaced by a rotation.
//
// Rotation requests are authenticated by a caller-supplied SignatureVerifier.
// A rotation is accepted only while the old key is valid and only once per old
// key. Access policy is retained, while the old key is removed immediately.
package netsec
