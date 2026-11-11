// Package netsec contains the implementation for UID 1251.
package netsec

// Solver overview
//
// Solve selects a minimum-cost set of candidate patches that makes a target
// attack node unreachable in an AND/OR dependency graph. Every patch subset
// (at most 2^20) is evaluated with a least-fixpoint reachability computation
// over packed node bitsets, so a precondition shared by several gates is a
// single closure fact and is never mistaken for an independent path.
//
// The returned Solution carries a blocking proof (the disabled actions and
// the residual reachable closure, excluding the target) plus, for every
// selected patch, a reachability witness showing the activation sequence that
// re-opens the target when that one patch is omitted. Equal-cost answers are
// tie-broken by the lexicographically smallest sorted patch-ID list.
