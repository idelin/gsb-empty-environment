// Package netsec plans safe register writes for firmware permission and lock
// bits.
//
// A Problem is a bit field plus a set of masked writes. A write is applicable
// only when its RequireMask/RequireValue precondition holds, and it may not
// clear a set lock bit. By default, setting any lock bit freezes every
// non-lock bit, matching the rule that a locked register can no longer change
// permissions. Explicit LockRule values provide finer-grained freezing.
//
// Plan performs a breadth-first search over register states, so the first
// goal it reaches is a shortest valid write sequence. Searches may be bounded
// by MaxSteps and MaxStates; exhausting a bound returns StatusIncomplete
// rather than claiming the goal is unreachable.
//
// Analyze exhaustively enumerates a small register state space (up to
// DefaultAnalysisLimit states). It computes both forward reachability from the
// initial state and reverse reachability from the goal, which identifies
// unrecoverable dead ends: states the firmware can enter but can never leave
// safely on a path to the requested permissions.
package netsec
