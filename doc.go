// Package netsec plans safe in-memory updates of switchable network edges.
//
// A Plan keeps every state, including the initial and target states, free of
// the forbidden reachability pairs. Each step flips exactly one mutable edge.
// The result is a shortest update sequence; equal-length candidates are
// resolved by the lexicographically smallest edge-ID sequence.
package netsec
