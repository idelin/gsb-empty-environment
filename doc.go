// Package netsec provides finite-domain type inference for teaching
// protocol scripts, so the symbolic verifier never treats all keys as
// interchangeable atoms.
//
// Key types carry three finite dimensions:
//   - owner: the principal the key belongs to;
//   - purpose: encrypt, verify or derive;
//   - session: the session generation (epoch) of the key.
//
// Message construction distinguishes KindEncrypt, KindSign and KindDerive.
// Each constructor forces the corresponding purpose on its key term, while
// KindNewKey declares a key's type as a model fact and KindBindKey asserts
// that two terms are the same key atom (an alias).
//
// The solver posts owner/purpose/session constraints into union-find
// equivalence classes (one variable per term per dimension), propagates the
// finite domains, and reports a Clash whenever one class is forced to take
// incompatible values. Report.ConflictOps and Report.MinConflictSets give the
// minimum-size sets of removable script operations (constructors and aliases,
// never NewKey facts) whose removal eliminates every clash, computed by a
// bounded branch-and-bound minimum hitting-set search.
//
// Polymorphic helper functions instantiate fresh type variables at every call
// site, and recursive role templates unroll up to an explicit depth with the
// epoch re-bound on each frame so per-session key types stay distinct.
package netsec
