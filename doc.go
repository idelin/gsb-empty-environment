// Package netsec finds reachable mutex deadlocks of two finite-state
// threads over at most six mutexes.
//
// A Spec describes two thread state machines whose edges carry a lock
// operation (OpNone, OpLock, OpUnlock) and an optional Boolean Guard built
// from Var, Not, And and Or. Condition variables are global: within one
// execution every occurrence of a variable keeps the same truth value.
//
// Analyze enumerates every fixed assignment of the condition variables and,
// per assignment, breadth-first walks the concrete product of the two
// machines together with the current mutex ownership. Because the walk only
// follows edges that are enabled in one concrete state under one fixed
// assignment, acquisition orders observed on mutually exclusive branches are
// never spliced into an artificial cycle.
//
// Three deadlock kinds are reported:
//   - DeadlockRecursive: a thread requests a non-reentrant mutex it holds.
//   - DeadlockCycle: blocked threads form a wait-for cycle (A/B versus B/A).
//   - DeadlockOrphaned: a blocked request waits on a mutex whose owner has
//     already terminated, so the request can never be granted.
//
// A state whose threads have all reached terminal states without blocking is
// a normal exit, not a deadlock. When a deadlock is reachable, Result carries
// the fixed condition assignment and the shortest interleaving of executed
// edges that reaches it.
package netsec
