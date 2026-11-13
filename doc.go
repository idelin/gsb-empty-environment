// Package netsec implements a concrete event model for epoch-based
// reclamation and an exhaustive witness search over unknown thread
// interleavings.
//
// The global epoch is E0..E3 and wraps modulo four. A state has at most three
// threads. Enter publishes the current epoch and marks a thread active.
// Dereference is legal only while the thread is active. Exit deactivates a
// thread but deliberately does not erase its locals: a thread that exits while
// holding a root can re-enter and later dereference the stale generation.
// Quiescent is legal only while inactive and never substitutes for Exit.
//
// The model only imposes the LoadRoot precondition stated by the specification
// (Dereference requires an active thread). Set
// Options.RequireActiveForLoadRoot to additionally reject inactive loads.
//
// A retired generation records the epoch and successful-advance counter at
// retirement. TryAdvance succeeds only if every active thread published the
// current epoch; it never updates a sleeping thread's published epoch. A
// successful advance counts as safe for an object only when no active thread
// holds that exact ID/generation. Reclamation requires two safe advances and
// no active holder at reclamation time. Eligibility therefore uses a
// per-object advance counter, not the numeric epoch difference, so a wrapped
// epoch cannot hide or invent completed grace periods.
//
// Program search enumerates all legal interleavings of per-thread scripts
// (each at most eight events). FindShortestWitness returns a shortest
// use-after-free witness and breaks equal-length ties by the lexicographically
// smallest sequence of thread numbers. ReviewProposed replays a pending
// implementation's explicit advance/reclaim decisions and reports the first
// decision that the correct model does not permit.
package netsec
