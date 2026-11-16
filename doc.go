// Package netsec safely restores attacker-controlled object graphs containing
// capability references.
//
// A Graph is a bounded, index-addressed wire object graph. Decode validates
// every node and reference, atomically leases every distinct reachable
// capability token for the caller's tenant and generation, and only then
// materializes the object graph. Object aliases and cycles retain pointer
// identity, while duplicate capability tokens share one lease and one
// Capability value. Bearer tokens never appear in decoded values.
package netsec
